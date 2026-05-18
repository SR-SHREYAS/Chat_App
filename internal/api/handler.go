package api

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/gorilla/websocket"
	"real_time_chat_app/internal/app/auth"
	"real_time_chat_app/internal/app/chat"
	"real_time_chat_app/internal/util"
)

type Handler struct {
	chatService *chat.Service
	authService *auth.Service
	upgrader    *websocket.Upgrader
}

func NewHandler(chatService *chat.Service, authService *auth.Service) *Handler {
	return &Handler{
		chatService: chatService,
		authService: authService,
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
		entryCode := r.URL.Query().Get("code")
		if _, err := h.chatService.HandleJoinSignedRoom(r.Context(), roomName, entryCode); err != nil {
			if errors.Is(err, chat.ErrInvalidRoomEntryCode) {
				http.Error(w, "Invalid room entry code", http.StatusForbidden)
				return
			}
			if errors.Is(err, chat.ErrSignedRoomExpired) {
				http.Error(w, "Room expired", http.StatusGone)
				return
			}
			if errors.Is(err, chat.ErrSignedRoomNotFound) {
				http.Error(w, "Room not found", http.StatusNotFound)
				return
			}
			log.Printf("Could not validate signed room entry for room %s: %v", roomName, err)
			http.Error(w, "Room access denied", http.StatusForbidden)
			return
		}
	}

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

	realRoom, client := h.chatService.HandleRoom(r.Context(), socket, roomName, userID, userName)

	realRoom.Join(client)
	defer realRoom.Leave(client)

	go client.Write()
	client.Read()
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
