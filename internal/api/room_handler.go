package api

import (
	"errors"
	"net/http"
	"net/url"
	"time"

	"real_time_chat_app/internal/app/chat"
	"real_time_chat_app/internal/model"
	"real_time_chat_app/internal/util"
)

type createSignedRoomRequest struct {
	RoomName   string `json:"room_name"`
	TTLMinutes int    `json:"ttl_minutes"`
}

type joinSignedRoomRequest struct {
	RoomName string `json:"room_name"`
}

type signedRoomEnvelope struct {
	RoomName         string `json:"room_name"`
	OwnerDisplayName string `json:"owner_display_name"`
	ExpiresAt        string `json:"expires_at"`
	ExpiresInSeconds int64  `json:"expires_in_seconds"`
	ChatURL          string `json:"chat_url,omitempty"`
}

type signedRoomStatusEnvelope struct {
	Exists bool                `json:"exists"`
	Room   *signedRoomEnvelope `json:"room,omitempty"`
}

type signedRoomListEnvelope struct {
	Rooms []signedRoomEnvelope `json:"rooms"`
}

func (h *Handler) handleCreateSignedRoom(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	authUser, ok := h.requireAuthenticatedUser(w, r)
	if !ok {
		return
	}

	var req createSignedRoomRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorEnvelope{Error: "invalid request body"})
		return
	}

	ttl := time.Duration(req.TTLMinutes) * time.Minute
	room, err := h.chatService.HandleCreateSignedRoom(r.Context(), util.SanitizeQueryValue(req.RoomName, 64), authUser.ID, authUser.DisplayName, ttl)
	if err != nil {
		h.writeSignedRoomError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, signedRoomEnvelopeFromModel(room, true))
}

func (h *Handler) handleJoinSignedRoom(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	if _, ok := h.requireAuthenticatedUser(w, r); !ok {
		return
	}

	var req joinSignedRoomRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorEnvelope{Error: "invalid request body"})
		return
	}

	room, err := h.chatService.HandleJoinSignedRoom(r.Context(), util.SanitizeQueryValue(req.RoomName, 64))
	if err != nil {
		h.writeSignedRoomError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, signedRoomEnvelopeFromModel(room, true))
}

func (h *Handler) handleOwnedSignedRooms(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	authUser, ok := h.requireAuthenticatedUser(w, r)
	if !ok {
		return
	}

	rooms, err := h.chatService.HandleListOwnedSignedRooms(r.Context(), authUser.ID)
	if err != nil {
		h.writeSignedRoomError(w, err)
		return
	}

	response := signedRoomListEnvelope{Rooms: make([]signedRoomEnvelope, 0, len(rooms))}
	for _, room := range rooms {
		response.Rooms = append(response.Rooms, signedRoomEnvelopeFromModel(room, false))
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) handleSignedRoomStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	if _, ok := h.requireAuthenticatedUser(w, r); !ok {
		return
	}

	roomName := util.SanitizeQueryValue(r.URL.Query().Get("room"), 64)
	room, exists, err := h.chatService.HandleGetSignedRoomStatus(r.Context(), roomName)
	if err != nil {
		if errors.Is(err, chat.ErrSignedRoomExpired) {
			writeJSON(w, http.StatusOK, signedRoomStatusEnvelope{Exists: false})
			return
		}
		h.writeSignedRoomError(w, err)
		return
	}
	if !exists {
		writeJSON(w, http.StatusOK, signedRoomStatusEnvelope{Exists: false})
		return
	}

	envelope := signedRoomEnvelopeFromModel(room, false)
	writeJSON(w, http.StatusOK, signedRoomStatusEnvelope{
		Exists: true,
		Room:   &envelope,
	})
}

func (h *Handler) writeSignedRoomError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, chat.ErrSignedRoomUnavailable):
		writeJSON(w, http.StatusServiceUnavailable, errorEnvelope{Error: err.Error()})
	case errors.Is(err, chat.ErrInvalidRoomName), errors.Is(err, chat.ErrInvalidRoomOwner), errors.Is(err, chat.ErrInvalidRoomTTL), errors.Is(err, chat.ErrSignedRoomTTLTooLarge):
		writeJSON(w, http.StatusBadRequest, errorEnvelope{Error: err.Error()})
	case errors.Is(err, chat.ErrSignedRoomNotFound):
		writeJSON(w, http.StatusNotFound, errorEnvelope{Error: err.Error()})
	case errors.Is(err, chat.ErrSignedRoomExpired):
		writeJSON(w, http.StatusGone, errorEnvelope{Error: err.Error()})
	case errors.Is(err, chat.ErrRoomOwnedByAnotherUser):
		writeJSON(w, http.StatusConflict, errorEnvelope{Error: err.Error()})
	default:
		writeJSON(w, http.StatusInternalServerError, errorEnvelope{Error: "signed room operation failed"})
	}
}

func (h *Handler) requireAuthenticatedUser(w http.ResponseWriter, r *http.Request) (authUser struct {
	ID          int64
	DisplayName string
}, ok bool) {
	if h.authService == nil {
		writeJSON(w, http.StatusServiceUnavailable, errorEnvelope{Error: "auth service unavailable"})
		return authUser, false
	}

	resolved, exists, err := h.resolveAuthenticatedUser(r)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorEnvelope{Error: "could not resolve session"})
		return authUser, false
	}
	if !exists {
		writeJSON(w, http.StatusUnauthorized, errorEnvelope{Error: "not signed in"})
		return authUser, false
	}

	authUser.ID = resolved.ID
	authUser.DisplayName = resolved.DisplayName
	return authUser, true
}

func signedRoomEnvelopeFromModel(room model.SignedRoom, includeChatURL bool) signedRoomEnvelope {
	expiresIn := int64(time.Until(room.ExpiresAt).Seconds())
	if expiresIn < 0 {
		expiresIn = 0
	}

	out := signedRoomEnvelope{
		RoomName:         room.RoomName,
		OwnerDisplayName: room.OwnerDisplayName,
		ExpiresAt:        room.ExpiresAt.UTC().Format(time.RFC3339),
		ExpiresInSeconds: expiresIn,
	}

	if includeChatURL {
		query := url.Values{}
		query.Set("room", room.RoomName)
		query.Set("expires_at", room.ExpiresAt.UTC().Format(time.RFC3339))
		out.ChatURL = "/chat?" + query.Encode()
	}

	return out
}
