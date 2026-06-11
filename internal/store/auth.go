package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"real_time_chat_app/internal/model"

	"github.com/lib/pq"
	"github.com/oklog/ulid/v2"
)

var (
	ErrDuplicateEmail    = errors.New("duplicate email")
	ErrDuplicateUsername = errors.New("duplicate username")
)

type AuthStore struct {
	db *sql.DB
}

func NewAuthStore(db *sql.DB) *AuthStore {
	return &AuthStore{db: db}
}

func (s *AuthStore) EnsureSchema(ctx context.Context) error {
	userTable := `
	CREATE TABLE IF NOT EXISTS users (
		id TEXT PRIMARY KEY CHECK (char_length(id) = 26),
		email VARCHAR(255) UNIQUE NOT NULL,
		username VARCHAR(64) UNIQUE NOT NULL,
		password_hash TEXT NOT NULL,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);
	`

	sessionTable := `
	CREATE TABLE IF NOT EXISTS user_sessions (
		id TEXT PRIMARY KEY CHECK (char_length(id) = 26),
		user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		token_hash CHAR(64) UNIQUE NOT NULL,
		expires_at TIMESTAMPTZ NOT NULL,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);
	`

	sessionIndexes := `
	CREATE INDEX IF NOT EXISTS idx_user_sessions_user_id ON user_sessions(user_id);
	CREATE INDEX IF NOT EXISTS idx_user_sessions_token_hash ON user_sessions(token_hash);
	CREATE INDEX IF NOT EXISTS idx_user_sessions_expires_at ON user_sessions(expires_at);
	`

	if _, err := s.db.ExecContext(ctx, userTable); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, sessionTable); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, sessionIndexes); err != nil {
		return err
	}

	return nil
}

func (s *AuthStore) CreateUser(ctx context.Context, email, username, passwordHash string) (model.User, error) {
	var user model.User
	userID := ulid.Make().String()
	query := `
		INSERT INTO users (id, email, username, password_hash)
		VALUES ($1, $2, $3, $4)
		RETURNING id, email, username, created_at, updated_at
	`
	err := s.db.QueryRowContext(ctx, query, userID, email, username, passwordHash).
		Scan(&user.ID, &user.Email, &user.UserName, &user.CreatedAt, &user.UpdatedAt)
	if err != nil {
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Code == "23505" {
			if pqErr.Constraint == "users_username_key" {
				return model.User{}, ErrDuplicateUsername
			}
			return model.User{}, ErrDuplicateEmail
		}
		return model.User{}, err
	}
	return user, nil
}

func (s *AuthStore) GetUserCredentialsByEmail(ctx context.Context, email string) (model.User, string, error) {
	var user model.User
	var passwordHash string

	query := `
		SELECT id, email, username, password_hash, created_at, updated_at
		FROM users
		WHERE email = $1
	`
	err := s.db.QueryRowContext(ctx, query, email).
		Scan(&user.ID, &user.Email, &user.UserName, &passwordHash, &user.CreatedAt, &user.UpdatedAt)
	if err != nil {
		return model.User{}, "", err
	}

	return user, passwordHash, nil
}

func (s *AuthStore) CreateSession(ctx context.Context, userID string, tokenHash string, expiresAt time.Time) error {
	sessionID := ulid.Make().String()
	query := `
		INSERT INTO user_sessions (id, user_id, token_hash, expires_at)
		VALUES ($1, $2, $3, $4)
	`
	_, err := s.db.ExecContext(ctx, query, sessionID, userID, tokenHash, expiresAt)
	return err
}

func (s *AuthStore) GetUserBySessionHash(ctx context.Context, tokenHash string) (model.User, error) {
	var user model.User
	query := `
		SELECT u.id, u.email, u.username, u.created_at, u.updated_at
		FROM user_sessions s
		INNER JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = $1 AND s.expires_at > NOW()
	`

	err := s.db.QueryRowContext(ctx, query, tokenHash).
		Scan(&user.ID, &user.Email, &user.UserName, &user.CreatedAt, &user.UpdatedAt)
	if err != nil {
		return model.User{}, err
	}
	return user, nil
}

func (s *AuthStore) UpdateDisplayName(ctx context.Context, userID string, username string) (model.User, error) {
	var user model.User
	query := `
		UPDATE users
		SET username = $1, updated_at = NOW()
		WHERE id = $2
		RETURNING id, email, username, created_at, updated_at
	`

	err := s.db.QueryRowContext(ctx, query, username, userID).
		Scan(&user.ID, &user.Email, &user.UserName, &user.CreatedAt, &user.UpdatedAt)
	if err != nil {
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Code == "23505" && pqErr.Constraint == "users_username_key" {
			return model.User{}, ErrDuplicateUsername
		}
		return model.User{}, err
	}
	return user, nil
}

func (s *AuthStore) DeleteSession(ctx context.Context, tokenHash string) error {
	query := `DELETE FROM user_sessions WHERE token_hash = $1`
	_, err := s.db.ExecContext(ctx, query, tokenHash)
	return err
}

func (s *AuthStore) DeleteExpiredSessions(ctx context.Context) error {
	query := `DELETE FROM user_sessions WHERE expires_at <= NOW()`
	_, err := s.db.ExecContext(ctx, query)
	return err
}
