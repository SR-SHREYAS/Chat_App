package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/mail"
	"strings"
	"time"

	"real_time_chat_app/internal/model"
	"real_time_chat_app/internal/store"
)

const (
	SessionCookieName = "chat_session"
	SessionTTL        = 30 * 24 * time.Hour
	minPasswordLength = 8
	maxDisplayNameLen = 32
)

var (
	ErrInvalidEmail       = errors.New("invalid email")
	ErrInvalidPassword    = errors.New("invalid password")
	ErrInvalidDisplayName = errors.New("invalid display name")
	ErrEmailAlreadyExists = errors.New("email already exists")
	ErrInvalidCredentials = errors.New("invalid credentials")
)

type AuthStore interface {
	CreateUser(ctx context.Context, email, displayName, passwordHash string) (model.User, error)
	GetUserCredentialsByEmail(ctx context.Context, email string) (model.User, string, error)
	CreateSession(ctx context.Context, userID int64, tokenHash string, expiresAt time.Time) error
	GetUserBySessionHash(ctx context.Context, tokenHash string) (model.User, error)
	UpdateDisplayName(ctx context.Context, userID int64, displayName string) (model.User, error)
	DeleteSession(ctx context.Context, tokenHash string) error
	DeleteExpiredSessions(ctx context.Context) error
}

type SignUpInput struct {
	Email       string
	Password    string
	DisplayName string
}

type SignInInput struct {
	Email    string
	Password string
}

type AuthUser struct {
	ID          int64  `json:"id"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
}

type AuthResult struct {
	User         AuthUser
	SessionToken string
}

type Service struct {
	store AuthStore
}

func NewService(store AuthStore) *Service {
	return &Service{store: store}
}

func (s *Service) HandleSignUp(ctx context.Context, input SignUpInput) (AuthResult, error) {
	email, err := normalizeEmail(input.Email)
	if err != nil {
		return AuthResult{}, err
	}

	password := strings.TrimSpace(input.Password)
	if len(password) < minPasswordLength {
		return AuthResult{}, ErrInvalidPassword
	}

	displayName := normalizeDisplayName(input.DisplayName)
	if displayName == "" {
		return AuthResult{}, ErrInvalidDisplayName
	}

	passwordHash, err := hashPassword(password)
	if err != nil {
		return AuthResult{}, err
	}

	user, err := s.store.CreateUser(ctx, email, displayName, passwordHash)
	if err != nil {
		if errors.Is(err, store.ErrDuplicateEmail) {
			return AuthResult{}, ErrEmailAlreadyExists
		}
		return AuthResult{}, err
	}

	return s.createSessionForUser(ctx, user)
}

func (s *Service) HandleSignIn(ctx context.Context, input SignInInput) (AuthResult, error) {
	email, err := normalizeEmail(input.Email)
	if err != nil {
		return AuthResult{}, ErrInvalidCredentials
	}

	user, storedHash, err := s.store.GetUserCredentialsByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AuthResult{}, ErrInvalidCredentials
		}
		return AuthResult{}, err
	}

	ok, err := verifyPassword(strings.TrimSpace(input.Password), storedHash)
	if err != nil {
		return AuthResult{}, err
	}
	if !ok {
		return AuthResult{}, ErrInvalidCredentials
	}

	return s.createSessionForUser(ctx, user)
}

func (s *Service) HandleMe(ctx context.Context, sessionToken string) (AuthUser, bool, error) {
	sessionToken = strings.TrimSpace(sessionToken)
	if sessionToken == "" {
		return AuthUser{}, false, nil
	}

	user, err := s.store.GetUserBySessionHash(ctx, hashSessionToken(sessionToken))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AuthUser{}, false, nil
		}
		return AuthUser{}, false, err
	}

	return toAuthUser(user), true, nil
}

func (s *Service) HandleSignOut(ctx context.Context, sessionToken string) error {
	sessionToken = strings.TrimSpace(sessionToken)
	if sessionToken == "" {
		return nil
	}
	return s.store.DeleteSession(ctx, hashSessionToken(sessionToken))
}

func (s *Service) HandleUpdateDisplayName(ctx context.Context, sessionToken, displayName string) (AuthUser, error) {
	sessionToken = strings.TrimSpace(sessionToken)
	if sessionToken == "" {
		return AuthUser{}, ErrInvalidCredentials
	}

	displayName = normalizeDisplayName(displayName)
	if displayName == "" {
		return AuthUser{}, ErrInvalidDisplayName
	}

	user, err := s.store.GetUserBySessionHash(ctx, hashSessionToken(sessionToken))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AuthUser{}, ErrInvalidCredentials
		}
		return AuthUser{}, err
	}

	updatedUser, err := s.store.UpdateDisplayName(ctx, user.ID, displayName)
	if err != nil {
		return AuthUser{}, err
	}

	return toAuthUser(updatedUser), nil
}

func (s *Service) createSessionForUser(ctx context.Context, user model.User) (AuthResult, error) {
	if err := s.store.DeleteExpiredSessions(ctx); err != nil {
		return AuthResult{}, err
	}

	rawToken, tokenHash, err := generateSessionTokenPair()
	if err != nil {
		return AuthResult{}, err
	}

	if err := s.store.CreateSession(ctx, user.ID, tokenHash, time.Now().Add(SessionTTL)); err != nil {
		return AuthResult{}, err
	}

	return AuthResult{
		User:         toAuthUser(user),
		SessionToken: rawToken,
	}, nil
}

func normalizeEmail(email string) (string, error) {
	trimmed := strings.TrimSpace(strings.ToLower(email))
	if trimmed == "" {
		return "", ErrInvalidEmail
	}
	if _, err := mail.ParseAddress(trimmed); err != nil {
		return "", ErrInvalidEmail
	}
	return trimmed, nil
}

func normalizeDisplayName(displayName string) string {
	displayName = strings.TrimSpace(displayName)
	if displayName == "" {
		return ""
	}

	runes := []rune(displayName)
	if len(runes) > maxDisplayNameLen {
		return string(runes[:maxDisplayNameLen])
	}
	return displayName
}

func generateSessionTokenPair() (rawToken, tokenHash string, err error) {
	tokenBytes := make([]byte, 32)
	if _, err = rand.Read(tokenBytes); err != nil {
		return "", "", err
	}

	rawToken = base64.RawURLEncoding.EncodeToString(tokenBytes)
	tokenHash = hashSessionToken(rawToken)
	return rawToken, tokenHash, nil
}

func hashSessionToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func toAuthUser(user model.User) AuthUser {
	return AuthUser{
		ID:          user.ID,
		Email:       user.Email,
		DisplayName: user.DisplayName,
	}
}
