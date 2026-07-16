package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"real_time_chat_app/internal/model"
	"real_time_chat_app/internal/repository"
)

const (
	SessionCookieName = "chat_session"
	SessionTTL        = 30 * 24 * time.Hour
	minPasswordLength = 8
	maxUsernameLen    = 32
)

var (
	ErrInvalidEmail          = errors.New("invalid email")
	ErrInvalidPassword       = errors.New("invalid password")
	ErrInvalidUsername       = errors.New("invalid username")
	ErrEmailAlreadyExists    = errors.New("email already exists")
	ErrUsernameAlreadyExists = errors.New("username already exists")
	ErrInvalidCredentials    = errors.New("invalid credentials")
	ErrUserNotFound          = errors.New("user not found")
)

type AuthRepository interface {
	CreateUser(ctx context.Context, email, username, passwordHash string) (model.User, error)
	GetUserCredentialsByEmail(ctx context.Context, email string) (model.User, string, error)
	CreateSession(ctx context.Context, userID string, tokenHash string, expiresAt time.Time) error
	GetUserBySessionHash(ctx context.Context, tokenHash string) (model.User, error)
	UpdateUsername(ctx context.Context, userID string, username string) (model.User, error)
	DeleteSession(ctx context.Context, tokenHash string) error
	DeleteExpiredSessions(ctx context.Context) error
}

type SignUpInput struct {
	Email    string
	Username string
	Password string
}

type SignInInput struct {
	Email    string
	Password string
}

type AuthUser struct {
	ID       string `json:"id"`
	Email    string `json:"email"`
	Username string `json:"username"`
}

type AuthResult struct {
	User         AuthUser
	SessionToken string
}

type Service struct {
	repository AuthRepository
}

func NewService(repository AuthRepository) *Service {
	return &Service{repository: repository}
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

	username := normalizeUsername(input.Username)
	if username == "" {
		return AuthResult{}, ErrInvalidUsername
	}

	passwordHash, err := hashPassword(password)
	if err != nil {
		return AuthResult{}, err
	}

	user, err := s.repository.CreateUser(ctx, email, username, passwordHash)
	if err != nil {
		if errors.Is(err, repository.ErrDuplicateEmail) {
			return AuthResult{}, ErrEmailAlreadyExists
		}
		if errors.Is(err, repository.ErrDuplicateUsername) {
			return AuthResult{}, ErrUsernameAlreadyExists
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

	user, storedHash, err := s.repository.GetUserCredentialsByEmail(ctx, email)
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

	user, err := s.repository.GetUserBySessionHash(ctx, hashSessionToken(sessionToken))
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
	return s.repository.DeleteSession(ctx, hashSessionToken(sessionToken))
}

func (s *Service) HandleUpdateUsername(ctx context.Context, sessionToken, username string) (AuthUser, error) {
	sessionToken = strings.TrimSpace(sessionToken)
	if sessionToken == "" {
		return AuthUser{}, ErrInvalidCredentials
	}

	username = normalizeUsername(username)
	if username == "" {
		return AuthUser{}, ErrInvalidUsername
	}

	user, err := s.repository.GetUserBySessionHash(ctx, hashSessionToken(sessionToken))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AuthUser{}, ErrInvalidCredentials
		}
		return AuthUser{}, err
	}

	updatedUser, err := s.repository.UpdateUsername(ctx, user.ID, username)
	if err != nil {
		if errors.Is(err, repository.ErrDuplicateUsername) {
			return AuthUser{}, ErrUsernameAlreadyExists
		}
		if errors.Is(err, repository.ErrUserNotFound) {
			return AuthUser{}, fmt.Errorf("user not found for valid session: %w", ErrUserNotFound)
		}
		return AuthUser{}, err
	}

	return toAuthUser(updatedUser), nil
}

func (s *Service) createSessionForUser(ctx context.Context, user model.User) (AuthResult, error) {
	if err := s.repository.DeleteExpiredSessions(ctx); err != nil {
		return AuthResult{}, err
	}

	rawToken, tokenHash, err := generateSessionTokenPair()
	if err != nil {
		return AuthResult{}, err
	}

	if err := s.repository.CreateSession(ctx, user.ID, tokenHash, time.Now().Add(SessionTTL)); err != nil {
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

func normalizeUsername(username string) string {
	username = strings.TrimSpace(username)
	if username == "" {
		return ""
	}

	runes := []rune(username)
	if len(runes) > maxUsernameLen {
		return string(runes[:maxUsernameLen])
	}
	return username
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
		ID:       user.ID,
		Email:    user.Email,
		Username: user.Username,
	}
}
