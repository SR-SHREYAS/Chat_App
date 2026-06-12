package api

import (
	"errors"
	"log"
	"net/http"
	"net/url"
	"strings"
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

type reviveSignedRoomRequest struct {
	RoomID     string `json:"room_id"`
	TTLMinutes int    `json:"ttl_minutes"`
}

type extendSignedRoomRequest struct {
	RoomID     string `json:"room_id"`
	TTLMinutes int    `json:"ttl_minutes"`
}

type joinSignedRoomRequest struct {
	RoomID    string `json:"room_id"`
	EntryCode string `json:"entry_code"`
}

type signedRoomEnvelope struct {
	RoomID           string `json:"-"`
	RoomName         string `json:"room_name"`
	OwnerUsername    string `json:"owner_username,omitempty"`
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

type roomHistoryEnvelope struct {
	Owned  []roomHistoryItemEnvelope `json:"owned"`
	Joined []roomHistoryItemEnvelope `json:"joined"`
}

type roomHistoryItemEnvelope struct {
	RoomID           string `json:"room_id"`
	RoomName         string `json:"room_name"`
	Role             string `json:"role"`
	OwnerUsername    string `json:"owner_username,omitempty"`
	EntryCode        string `json:"entry_code,omitempty"`
	ExpiresAt        string `json:"expires_at,omitempty"`
	ExpiresInSeconds int64  `json:"expires_in_seconds"`
	LastVisitedAt    string `json:"last_visited_at"`
	Active           bool   `json:"active"`
	ChatURL          string `json:"chat_url,omitempty"`
}

type signedRoomConfigEnvelope struct {
	DefaultTTLMinutes  int `json:"default_ttl_minutes"`
	MaxTTLMinutes      int `json:"max_ttl_minutes"`
	MaxCapacityMinutes int `json:"max_capacity_minutes"`
	EntryCodeLength    int `json:"entry_code_length"`
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

	ttl, ok := parseSignedRoomTTLMinutes(w, req.TTLMinutes)
	if !ok {
		return
	}

	room, err := h.chatService.HandleCreateSignedRoom(r.Context(), util.SanitizeQueryValue(req.RoomName, 64), authUser.ID, ttl)
	if err != nil {
		h.writeSignedRoomError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, signedRoomEnvelopeFromModel(room, true, true))
}

func (h *Handler) handleReviveSignedRoom(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	authUser, ok := h.requireAuthenticatedUser(w, r)
	if !ok {
		return
	}

	var req reviveSignedRoomRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorEnvelope{Error: "invalid request body"})
		return
	}

	ttl, ok := parseSignedRoomTTLMinutes(w, req.TTLMinutes)
	if !ok {
		return
	}

	room, err := h.chatService.HandleReviveSignedRoom(r.Context(), util.SanitizeQueryValue(req.RoomID, 64), authUser.ID, ttl)
	if err != nil {
		h.writeSignedRoomError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, signedRoomEnvelopeFromModel(room, true, true))
}

func (h *Handler) handleExtendSignedRoom(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	authUser, ok := h.requireAuthenticatedUser(w, r)
	if !ok {
		return
	}

	var req extendSignedRoomRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorEnvelope{Error: "invalid request body"})
		return
	}

	ttl, ok := parseSignedRoomTTLMinutes(w, req.TTLMinutes)
	if !ok {
		return
	}

	room, err := h.chatService.HandleExtendSignedRoom(r.Context(), util.SanitizeQueryValue(req.RoomID, 64), authUser.ID, ttl)
	if err != nil {
		h.writeSignedRoomError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, signedRoomEnvelopeFromModel(room, true, true))
}

func (h *Handler) handleSignedRoomConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	writeJSON(w, http.StatusOK, signedRoomConfigEnvelope{
		DefaultTTLMinutes:  int(chat.DefaultSignedRoomTTL / time.Minute),
		MaxTTLMinutes:      int(chat.MaxSignedRoomTTL / time.Minute),
		MaxCapacityMinutes: int(chat.MaxSignedRoomCapacity / time.Minute),
		EntryCodeLength:    chat.SignedRoomCodeLength,
	})
}

func (h *Handler) joinSignedRoom(w http.ResponseWriter, r *http.Request) {
	// panic recovery
	defer func() {
		if err := recover(); err != nil {
			log.Printf("[PANIC RECOVERED] joinSignedRoom: %v", err)
			writeJSON(w, http.StatusInternalServerError, errorEnvelope{Error: "internal server error"})
		}
	}()

	// Validate HTTP method directly in the handler
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	// Authentication
	authUser, ok := h.requireAuthenticatedUser(w, r)
	if !ok {
		return
	}

	// Verify incoming request structure
	var req joinSignedRoomRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorEnvelope{Error: "invalid request body"})
		return
	}

	roomName := util.SanitizeQueryValue(req.RoomID, 64)
	entryCode := strings.TrimSpace(req.EntryCode)

	if roomName == "" {
		writeJSON(w, http.StatusBadRequest, errorEnvelope{Error: "room name is required"})
		return
	}
	if len(entryCode) != chat.SignedRoomCodeLength {
		writeJSON(w, http.StatusBadRequest, errorEnvelope{Error: "invalid entry code format"})
		return
	}

	joinScope := h.newSignedRoomJoinScope(r, roomName, authUser)
	if h.isSignedRoomJoinBlocked(joinScope) {
		writeJSON(w, http.StatusTooManyRequests, errorEnvelope{Error: "too many failed entry code attempts; retry later"})
		return
	}

	// Call to service layer with request context
	room, err := h.chatService.JoinSignedRoom(r.Context(), roomName, entryCode)
	if err != nil {
		var appErr *util.AppError
		if errors.As(err, &appErr) {
			// Handle the rate-limit failure logic for forbidden entry attempts
			if appErr.StatusCode == http.StatusForbidden {
				h.recordSignedRoomJoinFailure(joinScope)
				if h.isSignedRoomJoinBlocked(joinScope) {
					writeJSON(w, http.StatusTooManyRequests, errorEnvelope{Error: "too many failed entry code attempts; retry later"})
					return
				}
			}

			// Log internal DB errors to terminal
			if appErr.Internal != nil {
				log.Printf("[ERROR] joinSignedRoom: %v", appErr.Internal)
			}

			// Return the error response
			writeJSON(w, appErr.StatusCode, errorEnvelope{Error: appErr.Message})
			return
		}

		// Fallback for unexpected standard errors
		h.writeSignedRoomError(w, err)
		return
	}

	// Reset failed attempts
	h.resetSignedRoomJoinFailures(joinScope)
	if err := h.chatService.HandleRecordSignedRoomJoin(r.Context(), room.ID, authUser.ID); err != nil {
		log.Printf("Could not record joined signed room user=%s room=%s: %v", authUser.ID, room.ID, err)
	}

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

func (h *Handler) handleRoomHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	authUser, ok := h.requireAuthenticatedUser(w, r)
	if !ok {
		return
	}

	history, err := h.chatService.HandleListRoomHistory(r.Context(), authUser.ID)
	if err != nil {
		h.writeSignedRoomError(w, err)
		return
	}

	response := roomHistoryEnvelope{
		Owned:  make([]roomHistoryItemEnvelope, 0),
		Joined: make([]roomHistoryItemEnvelope, 0),
	}
	for _, item := range history {
		envelope := roomHistoryEnvelopeFromModel(item)
		switch item.Role {
		case "owner":
			response.Owned = append(response.Owned, envelope)
		case "member":
			response.Joined = append(response.Joined, envelope)
		}
	}

	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) handleDeleteSignedRoom(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	authUser, ok := h.requireAuthenticatedUser(w, r)
	if !ok {
		return
	}

	roomID := util.SanitizeQueryValue(r.URL.Query().Get("room_id"), 64)
	if roomID == "" {
		writeJSON(w, http.StatusBadRequest, errorEnvelope{Error: "room_id is required"})
		return
	}

	if err := h.chatService.HandleDeleteSignedRoom(r.Context(), roomID, authUser.ID); err != nil {
		h.writeSignedRoomError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]bool{"expired": true})
}

func (h *Handler) handlePurgeSignedRoom(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	authUser, ok := h.requireAuthenticatedUser(w, r)
	if !ok {
		return
	}

	roomID := util.SanitizeQueryValue(r.URL.Query().Get("room_id"), 64)
	if roomID == "" {
		writeJSON(w, http.StatusBadRequest, errorEnvelope{Error: "room_id is required"})
		return
	}

	if err := h.chatService.HandlePurgeSignedRoom(r.Context(), roomID, authUser.ID); err != nil {
		h.writeSignedRoomError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]bool{"purged": true})
}

func (h *Handler) handleSignedRoomStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	// This endpoint is used by chat clients (including guests) to resolve
	// whether a room has signed-room TTL metadata. Keep it unauthenticated.

	roomID := util.SanitizeQueryValue(r.URL.Query().Get("room_id"), 64)
	unsignedRoomName := util.SanitizeQueryValue(r.URL.Query().Get("room"), 64)
	if roomID != "" && unsignedRoomName != "" {
		writeJSON(w, http.StatusBadRequest, errorEnvelope{Error: "ambiguous room parameters; specify only room_id"})
		return
	}
	if unsignedRoomName != "" && roomID == "" {
		writeJSON(w, http.StatusBadRequest, errorEnvelope{Error: "room status is only available for signed rooms by room_id"})
		return
	}

	room, exists, err := h.chatService.HandleGetSignedRoomStatus(r.Context(), roomID)
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
	var appErr *util.AppError
	if errors.As(err, &appErr) {
		if appErr.Internal != nil {
			log.Printf("[ERROR] signed room error: kind=internal message=%q", appErr.Message)
		}
		writeJSON(w, appErr.StatusCode, errorEnvelope{Error: appErr.Message})
		return
	}

	switch {
	case errors.Is(err, chat.ErrSignedRoomUnavailable):
		writeJSON(w, http.StatusServiceUnavailable, errorEnvelope{Error: err.Error()})
	case errors.Is(err, chat.ErrInvalidRoomName), errors.Is(err, chat.ErrInvalidRoomOwner), errors.Is(err, chat.ErrSignedRoomTTLTooLarge), errors.Is(err, chat.ErrSignedRoomCapacityTooLarge):
		writeJSON(w, http.StatusBadRequest, errorEnvelope{Error: err.Error()})
	case errors.Is(err, chat.ErrInvalidRoomEntryCode):
		writeJSON(w, http.StatusForbidden, errorEnvelope{Error: err.Error()})
	case errors.Is(err, chat.ErrSignedRoomNotFound):
		writeJSON(w, http.StatusNotFound, errorEnvelope{Error: err.Error()})
	case errors.Is(err, chat.ErrSignedRoomExpired):
		writeJSON(w, http.StatusGone, errorEnvelope{Error: err.Error()})
	case errors.Is(err, chat.ErrSignedRoomAlreadyActive):
		writeJSON(w, http.StatusConflict, errorEnvelope{Error: err.Error()})
	case errors.Is(err, chat.ErrRoomOwnedByAnotherUser):
		writeJSON(w, http.StatusConflict, errorEnvelope{Error: err.Error()})
	case errors.Is(err, chat.ErrRoomEntryCodeUnavailable):
		writeJSON(w, http.StatusServiceUnavailable, errorEnvelope{Error: err.Error()})
	default:
		writeJSON(w, http.StatusInternalServerError, errorEnvelope{Error: "signed room operation failed"})
	}
}

func parseSignedRoomTTLMinutes(w http.ResponseWriter, ttlMinutes int) (time.Duration, bool) {
	if ttlMinutes < 0 {
		writeJSON(w, http.StatusBadRequest, errorEnvelope{Error: "ttl must be non-negative"})
		return 0, false
	}

	maxMinutes := int(chat.MaxSignedRoomTTL / time.Minute)
	if ttlMinutes > maxMinutes {
		writeJSON(w, http.StatusBadRequest, errorEnvelope{Error: chat.ErrSignedRoomTTLTooLarge.Error()})
		return 0, false
	}

	return time.Duration(ttlMinutes) * time.Minute, true
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
		RoomID:           room.ID,
		RoomName:         room.RoomName,
		OwnerUsername:    room.OwnerUsername,
		ExpiresAt:        room.ExpiresAt.UTC().Format(time.RFC3339),
		ExpiresInSeconds: expiresIn,
	}
	if includeEntryCode {
		out.EntryCode = room.EntryCode
	}

	if includeChatURL {
		query := url.Values{}
		query.Set("room_id", room.ID)
		query.Set("expires_at", room.ExpiresAt.UTC().Format(time.RFC3339))
		out.ChatURL = "/chat?" + query.Encode()
	}

	return out
}

func roomHistoryEnvelopeFromModel(item model.RoomHistory) roomHistoryItemEnvelope {
	expiresAt := ""
	expiresIn := int64(0)
	if !item.ExpiresAt.IsZero() {
		expiresAt = item.ExpiresAt.UTC().Format(time.RFC3339)
		expiresIn = int64(time.Until(item.ExpiresAt).Seconds())
		if expiresIn < 0 {
			expiresIn = 0
		}
	}

	out := roomHistoryItemEnvelope{
		RoomID:           item.RoomID,
		RoomName:         item.RoomName,
		Role:             item.Role,
		OwnerUsername:    item.OwnerUsername,
		ExpiresAt:        expiresAt,
		ExpiresInSeconds: expiresIn,
		LastVisitedAt:    item.LastVisitedAt.UTC().Format(time.RFC3339),
		Active:           item.Active,
	}

	if item.Active {
		out.EntryCode = item.EntryCode
		query := url.Values{}
		query.Set("room_id", item.RoomID)
		if expiresAt != "" {
			query.Set("expires_at", expiresAt)
		}
		out.ChatURL = "/chat?" + query.Encode()
	}

	return out
}
