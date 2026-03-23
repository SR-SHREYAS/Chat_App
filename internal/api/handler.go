package api

import (
	"log"
	"net/http"

	"github.com/gorilla/websocket"
	"real_time_chat_app/internal/app/chat"
	"real_time_chat_app/internal/util"
)

type Handler struct {
	service  *chat.Service
	upgrader *websocket.Upgrader
}

func NewHandler(service *chat.Service) *Handler {
	return &Handler{
		service: service,
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

	socket, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("Upgrade error:", err)
		return
	}

	userID := util.SanitizeQueryValue(r.URL.Query().Get("user_id"), 32)
	realRoom, client := h.service.HandleRoom(r.Context(), socket, roomName, userID)

	realRoom.Join(client)
	defer realRoom.Leave(client)

	go client.Write()
	client.Read()
}

func (h *Handler) handleHealth(w http.ResponseWriter, r *http.Request) {
	if err := h.service.HandleHealth(r.Context()); err != nil {
		log.Printf("Health check failed: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Database Unreachable"))
		return
	}

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("OK"))
}
