package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"real_time_chat_app/internal/app/auth"
)

type signUpRequest struct {
	Email       string `json:"email"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name"`
}

type signInRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type authEnvelope struct {
	Authenticated bool           `json:"authenticated"`
	User          *auth.AuthUser `json:"user,omitempty"`
}

type errorEnvelope struct {
	Error string `json:"error"`
}

func (h *Handler) handleSignUp(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.authService == nil {
		writeJSON(w, http.StatusServiceUnavailable, errorEnvelope{Error: "auth service unavailable"})
		return
	}

	var req signUpRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorEnvelope{Error: "invalid request body"})
		return
	}

	result, err := h.authService.HandleSignUp(r.Context(), auth.SignUpInput{
		Email:       req.Email,
		Password:    req.Password,
		DisplayName: req.DisplayName,
	})
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrInvalidEmail), errors.Is(err, auth.ErrInvalidPassword):
			writeJSON(w, http.StatusBadRequest, errorEnvelope{Error: err.Error()})
		case errors.Is(err, auth.ErrEmailAlreadyExists):
			writeJSON(w, http.StatusConflict, errorEnvelope{Error: err.Error()})
		default:
			writeJSON(w, http.StatusInternalServerError, errorEnvelope{Error: "could not create account"})
		}
		return
	}

	setSessionCookie(w, r, result.SessionToken)
	writeJSON(w, http.StatusCreated, authEnvelope{
		Authenticated: true,
		User:          &result.User,
	})
}

func (h *Handler) handleSignIn(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.authService == nil {
		writeJSON(w, http.StatusServiceUnavailable, errorEnvelope{Error: "auth service unavailable"})
		return
	}

	var req signInRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorEnvelope{Error: "invalid request body"})
		return
	}

	result, err := h.authService.HandleSignIn(r.Context(), auth.SignInInput{
		Email:    req.Email,
		Password: req.Password,
	})
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrInvalidCredentials):
			writeJSON(w, http.StatusUnauthorized, errorEnvelope{Error: err.Error()})
		default:
			writeJSON(w, http.StatusInternalServerError, errorEnvelope{Error: "could not sign in"})
		}
		return
	}

	setSessionCookie(w, r, result.SessionToken)
	writeJSON(w, http.StatusOK, authEnvelope{
		Authenticated: true,
		User:          &result.User,
	})
}

func (h *Handler) handleSignOut(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	if h.authService != nil {
		_ = h.authService.HandleSignOut(r.Context(), sessionTokenFromRequest(r))
	}
	clearSessionCookie(w, r)

	writeJSON(w, http.StatusOK, authEnvelope{Authenticated: false})
}

func (h *Handler) handleMe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.authService == nil {
		writeJSON(w, http.StatusOK, authEnvelope{Authenticated: false})
		return
	}

	user, ok, err := h.authService.HandleMe(r.Context(), sessionTokenFromRequest(r))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorEnvelope{Error: "could not resolve session"})
		return
	}
	if !ok {
		writeJSON(w, http.StatusOK, authEnvelope{Authenticated: false})
		return
	}

	writeJSON(w, http.StatusOK, authEnvelope{
		Authenticated: true,
		User:          &user,
	})
}

func decodeJSON(r *http.Request, dst any) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("request body must contain a single JSON object")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func setSessionCookie(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     auth.SessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   isSecureRequest(r),
		Expires:  time.Now().Add(auth.SessionTTL),
		MaxAge:   int(auth.SessionTTL.Seconds()),
	})
}

func clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     auth.SessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   isSecureRequest(r),
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
	})
}

func isSecureRequest(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}
