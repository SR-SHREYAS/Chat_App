package api

import (
	"net/http"

	"real_time_chat_app/internal/view"
)

func RegisterRoutes(mux *http.ServeMux, h *Handler) {
	mux.Handle("/", view.NewTemplateHandler("index.html"))
	mux.Handle("/signup", view.NewTemplateHandler("signup.html"))
	mux.Handle("/dashboard", view.NewTemplateHandler("dashboard.html"))
	mux.Handle("/chat", view.NewTemplateHandler("chat.html"))
	mux.HandleFunc("/room", h.handleRoom)
	mux.HandleFunc("/health", h.handleHealth)
	mux.HandleFunc("/api/auth/signup", h.handleSignUp)
	mux.HandleFunc("/api/auth/signin", h.handleSignIn)
	mux.HandleFunc("/api/auth/signout", h.handleSignOut)
	mux.HandleFunc("/api/auth/me", h.handleMe)
	mux.HandleFunc("/api/auth/display-name", h.handleUpdateDisplayName)
}
