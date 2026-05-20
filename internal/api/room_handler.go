package api

import (
	"errors"
	"net/http"
	"net/url"
	"time"

	"real_time_chat_app/internal/app/auth"
	"real_time_chat_app/internal/app/chat"
	"real_time_chat_app/internal/model"
	"real_time_chat_app/internal/util"
)

type createSignedRoomRequest struct {
	RoomName   string `json:"room_name"`
	TTLMinutes int    `json:"ttl_minutes"`
}

type joinSignedRoomRequest struct {
	RoomName  string `json:"room_name"`
	EntryCode string `json:"entry_code"`
}

type signedRoomEnvelope struct {
	RoomName         string `json:"room_name"`
	OwnerDisplayName string `json:"owner_display_name"`
	EntryCode        string `json:"entry_code,omitempty"`
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

type signedRoomConfigEnvelope struct {
	DefaultTTLMinutes int `json:"default_ttl_minutes"`
	MaxTTLMinutes     int `json:"max_ttl_minutes"`
	EntryCodeLength   int `json:"entry_code_length"`
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

	if req.TTLMinutes < 0 {
		writeJSON(w, http.StatusBadRequest, errorEnvelope{Error: "ttl must be non-negative"})
		return
	}

	maxMinutes := int(chat.MaxSignedRoomTTL / time.Minute)
	if req.TTLMinutes > maxMinutes {
		writeJSON(w, http.StatusBadRequest, errorEnvelope{Error: chat.ErrSignedRoomTTLTooLarge.Error()})
		return
	}

	ttl := time.Duration(req.TTLMinutes) * time.Minute
	room, err := h.chatService.HandleCreateSignedRoom(r.Context(), util.SanitizeQueryValue(req.RoomName, 64), authUser.ID, authUser.DisplayName, ttl)
	if err != nil {
		h.writeSignedRoomError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, signedRoomEnvelopeFromModel(room, true, true))
}

func (h *Handler) handleSignedRoomConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	writeJSON(w, http.StatusOK, signedRoomConfigEnvelope{
		DefaultTTLMinutes: int(chat.DefaultSignedRoomTTL / time.Minute),
		MaxTTLMinutes:     int(chat.MaxSignedRoomTTL / time.Minute),
		EntryCodeLength:   chat.SignedRoomCodeLength,
	})
}

func (h *Handler) handleJoinSignedRoom(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	authUser, ok := h.requireAuthenticatedUser(w, r)
	if !ok {
		return
	}

	var req joinSignedRoomRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorEnvelope{Error: "invalid request body"})
		return
	}
	roomName := util.SanitizeQueryValue(req.RoomName, 64)
	if h.isSignedRoomJoinBlocked(r, roomName, authUser) {
		writeJSON(w, http.StatusTooManyRequests, errorEnvelope{Error: "too many failed entry code attempts; retry later"})
		return
	}

	room, err := h.chatService.HandleJoinSignedRoom(r.Context(), roomName, req.EntryCode)
	if err != nil {
		if errors.Is(err, chat.ErrInvalidRoomEntryCode) {
			h.recordSignedRoomJoinFailure(r, roomName, authUser)
			if h.isSignedRoomJoinBlocked(r, roomName, authUser) {
				writeJSON(w, http.StatusTooManyRequests, errorEnvelope{Error: "too many failed entry code attempts; retry later"})
				return
			}
		}
		h.writeSignedRoomError(w, err)
		return
	}
	h.resetSignedRoomJoinFailures(r, roomName, authUser)

	writeJSON(w, http.StatusOK, signedRoomEnvelopeFromModel(room, true, true))
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
		response.Rooms = append(response.Rooms, signedRoomEnvelopeFromModel(room, false, true))
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) handleSignedRoomStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	// This endpoint is used by chat clients (including guests) to resolve
	// whether a room has signed-room TTL metadata. Keep it unauthenticated.

	roomName := util.SanitizeQueryValue(r.URL.Query().Get("room"), 64)
	room, exists, err := h.chatService.HandleGetSignedRoomStatus(r.Context(), roomName)
	if err != nil {
		h.writeSignedRoomError(w, err)
		return
	}
	if !exists {
		writeJSON(w, http.StatusOK, signedRoomStatusEnvelope{Exists: false})
		return
	}

	envelope := signedRoomEnvelopeFromModel(room, false, false)
	writeJSON(w, http.StatusOK, signedRoomStatusEnvelope{
		Exists: true,
		Room:   &envelope,
	})
}

func (h *Handler) writeSignedRoomError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, chat.ErrSignedRoomUnavailable):
		writeJSON(w, http.StatusServiceUnavailable, errorEnvelope{Error: err.Error()})
	case errors.Is(err, chat.ErrInvalidRoomName), errors.Is(err, chat.ErrInvalidRoomOwner), errors.Is(err, chat.ErrSignedRoomTTLTooLarge):
		writeJSON(w, http.StatusBadRequest, errorEnvelope{Error: err.Error()})
	case errors.Is(err, chat.ErrInvalidRoomEntryCode):
		writeJSON(w, http.StatusForbidden, errorEnvelope{Error: err.Error()})
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

func (h *Handler) requireAuthenticatedUser(w http.ResponseWriter, r *http.Request) (auth.AuthUser, bool) {
	var authUser auth.AuthUser

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

	return resolved, true
}

func signedRoomEnvelopeFromModel(room model.SignedRoom, includeChatURL, includeEntryCode bool) signedRoomEnvelope {
	// If the server returns a direct chat URL, include the entry code in JSON
	// response payload so the client can send it through a non-URL channel.
	if includeChatURL {
		includeEntryCode = true
	}

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
	if includeEntryCode {
		out.EntryCode = room.EntryCode
	}

	if includeChatURL {
		query := url.Values{}
		query.Set("room", room.RoomName)
		query.Set("expires_at", room.ExpiresAt.UTC().Format(time.RFC3339))
		out.ChatURL = "/chat?" + query.Encode()
	}

	return out
}
