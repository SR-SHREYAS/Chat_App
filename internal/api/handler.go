package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"real_time_chat_app/internal/app/auth"
	"real_time_chat_app/internal/app/chat"
	"real_time_chat_app/internal/util"
)

type Handler struct {
	chatService     *chat.Service
	authService     *auth.Service
	upgrader        *websocket.Upgrader
	signedRoomJoins *signedRoomJoinLimiter
}

type signedRoomJoinScope struct {
	clientIP string
	roomName string
	subject  string
}

func NewHandler(chatService *chat.Service, authService *auth.Service) *Handler {
	return &Handler{
		chatService:     chatService,
		authService:     authService,
		signedRoomJoins: newDefaultSignedRoomJoinLimiter(),
		upgrader: &websocket.Upgrader{
			ReadBufferSize:  chat.SocketBufferSize,
			WriteBufferSize: chat.SocketBufferSize,
			CheckOrigin:     util.CheckSameOrigin,
		},
	}
}

func (h *Handler) handleRoom(w http.ResponseWriter, r *http.Request) {
	roomName := util.SanitizeQueryValue(r.URL.Query().Get("room"), 64)
	if roomName == "" {
		http.Error(w, "Missing or invalid room parameter", http.StatusBadRequest)
		return
	}

	var (
		authUser auth.AuthUser
		isAuth   bool
	)
	if h.authService != nil {
		resolved, ok, err := h.resolveAuthenticatedUser(r)
		if err != nil {
			log.Printf("Could not resolve authenticated user: %v", err)
		} else if ok {
			authUser = resolved
			isAuth = true
		}
	}

	_, hasSignedRoom, err := h.chatService.HandleGetSignedRoomStatus(r.Context(), roomName)
	if err != nil {
		switch {
		case errors.Is(err, chat.ErrSignedRoomExpired):
			http.Error(w, "Room expired", http.StatusGone)
			return
		case errors.Is(err, chat.ErrSignedRoomUnavailable):
			log.Printf("Signed room service unavailable: %v", err)
		default:
			log.Printf("Could not resolve signed room status: %v", err)
		}
	}
	if hasSignedRoom {
		if !isAuth {
			http.Error(w, "Sign-in required for this room", http.StatusUnauthorized)
			return
		}
	}
	joinScope := h.newSignedRoomJoinScope(r, roomName, authUser)

	userID := util.SanitizeQueryValue(r.URL.Query().Get("user_id"), 32)
	if userID == "" {
		userID = randomGuestID()
	}

	userName := ""
	if isAuth {
		userName = authUser.DisplayName
	}

	socket, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("Upgrade error:", err)
		return
	}

	if hasSignedRoom {
		if h.isSignedRoomJoinBlocked(joinScope) {
			closeWithPolicyViolation(socket, "too many failed entry code attempts")
			return
		}

		entryCode, err := readSignedRoomEntryCodeHandshake(socket)
		if err != nil {
			log.Printf("Missing/invalid signed room auth handshake for room %s: %v", roomName, err)
			closeWithPolicyViolation(socket, "entry code required")
			return
		}

		if _, err := h.chatService.HandleJoinSignedRoom(r.Context(), roomName, entryCode); err != nil {
			switch {
			case errors.Is(err, chat.ErrInvalidRoomEntryCode):
				h.recordSignedRoomJoinFailure(joinScope)
				if h.isSignedRoomJoinBlocked(joinScope) {
					closeWithPolicyViolation(socket, "too many failed entry code attempts")
					return
				}
				closeWithPolicyViolation(socket, "invalid entry code")
				return
			case errors.Is(err, chat.ErrSignedRoomExpired):
				closeWithPolicyViolation(socket, "room expired")
				return
			case errors.Is(err, chat.ErrSignedRoomNotFound):
				closeWithPolicyViolation(socket, "room not found")
				return
			default:
				log.Printf("Could not validate signed room entry for room %s: %v", roomName, err)
				closeWithPolicyViolation(socket, "room access denied")
				return
			}
		}
		h.resetSignedRoomJoinFailures(joinScope)
	}

	realRoom, client := h.chatService.HandleRoom(r.Context(), socket, roomName, userID, userName)

	realRoom.Join(client)
	defer realRoom.Leave(client)

	go client.Write()
	client.Read()
}

type signedRoomHandshakeMessage struct {
	Type      string `json:"type"`
	EntryCode string `json:"entry_code"`
}

func readSignedRoomEntryCodeHandshake(socket *websocket.Conn) (string, error) {
	_ = socket.SetReadDeadline(time.Now().Add(5 * time.Second))
	defer func() {
		_ = socket.SetReadDeadline(time.Time{})
	}()

	messageType, payload, err := socket.ReadMessage()
	if err != nil {
		return "", err
	}
	if messageType != websocket.TextMessage {
		return "", errors.New("handshake must be a text message")
	}
	if len(payload) > 1024 {
		return "", errors.New("handshake payload too large")
	}

	var msg signedRoomHandshakeMessage
	if err := json.Unmarshal(payload, &msg); err != nil {
		return "", err
	}
	if msg.Type != "auth" {
		return "", errors.New("invalid handshake message type")
	}
	return strings.TrimSpace(msg.EntryCode), nil
}

func (h *Handler) handleHealth(w http.ResponseWriter, r *http.Request) {
	if err := h.chatService.HandleHealth(r.Context()); err != nil {
		log.Printf("Health check failed: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Database Unreachable"))
		return
	}

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("OK"))
}

func randomGuestID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "guest"
	}
	return hex.EncodeToString(b)
}

func sessionTokenFromRequest(r *http.Request) string {
	cookie, err := r.Cookie(auth.SessionCookieName)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(cookie.Value)
}

func (h *Handler) resolveAuthenticatedUser(r *http.Request) (auth.AuthUser, bool, error) {
	token := sessionTokenFromRequest(r)
	return h.authService.HandleMe(r.Context(), token)
}

func (h *Handler) newSignedRoomJoinScope(r *http.Request, roomName string, authUser auth.AuthUser) signedRoomJoinScope {
	return signedRoomJoinScope{
		clientIP: util.ClientIP(r),
		roomName: roomName,
		subject:  signedRoomJoinSubject(authUser),
	}
}

func (h *Handler) isSignedRoomJoinBlocked(scope signedRoomJoinScope) bool {
	return h.signedRoomJoins.IsBlocked(scope.clientIP, scope.roomName, scope.subject)
}

func (h *Handler) recordSignedRoomJoinFailure(scope signedRoomJoinScope) {
	h.signedRoomJoins.RecordFailure(scope.clientIP, scope.roomName, scope.subject)
}

func (h *Handler) resetSignedRoomJoinFailures(scope signedRoomJoinScope) {
	h.signedRoomJoins.Reset(scope.clientIP, scope.roomName, scope.subject)
}

func signedRoomJoinSubject(authUser auth.AuthUser) string {
	if authUser.ID <= 0 {
		return ""
	}
	return "user-" + strconv.FormatInt(authUser.ID, 10)
}

func closeWithPolicyViolation(socket *websocket.Conn, reason string) {
	if socket == nil {
		return
	}
	_ = socket.WriteControl(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.ClosePolicyViolation, reason),
		time.Now().Add(time.Second),
	)
	_ = socket.Close()
}
