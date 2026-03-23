package api

import (
	"net/http"

	"real_time_chat_app/internal/view"
)

func RegisterRoutes(mux *http.ServeMux, h *Handler) {
	mux.Handle("/", view.NewTemplateHandler("index.html"))
	mux.Handle("/chat", view.NewTemplateHandler("chat.html"))
	mux.HandleFunc("/room", h.handleRoom)
	mux.HandleFunc("/health", h.handleHealth)
}
