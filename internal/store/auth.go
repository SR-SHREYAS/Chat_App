package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/lib/pq"
	"real_time_chat_app/internal/model"
)

var ErrDuplicateEmail = errors.New("duplicate email")

type AuthStore struct {
	db *sql.DB
}

func NewAuthStore(db *sql.DB) *AuthStore {
	return &AuthStore{db: db}
}

func (s *AuthStore) EnsureSchema(ctx context.Context) error {
	userTable := `
	CREATE TABLE IF NOT EXISTS users (
		id SERIAL PRIMARY KEY,
		email VARCHAR(255) UNIQUE NOT NULL,
		display_name VARCHAR(64) NOT NULL,
		password_hash TEXT NOT NULL,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);
	`

	sessionTable := `
	CREATE TABLE IF NOT EXISTS user_sessions (
		id SERIAL PRIMARY KEY,
		user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		token_hash CHAR(64) UNIQUE NOT NULL,
		expires_at TIMESTAMPTZ NOT NULL,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);
	`

	sessionIndexes := `
	CREATE INDEX IF NOT EXISTS idx_user_sessions_user_id ON user_sessions(user_id);
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

func (s *AuthStore) CreateUser(ctx context.Context, email, displayName, passwordHash string) (model.User, error) {
	var user model.User
	query := `
		INSERT INTO users (email, display_name, password_hash)
		VALUES ($1, $2, $3)
		RETURNING id, email, display_name, created_at
	`
	err := s.db.QueryRowContext(ctx, query, email, displayName, passwordHash).
		Scan(&user.ID, &user.Email, &user.DisplayName, &user.CreatedAt)
	if err != nil {
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Code == "23505" {
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
		SELECT id, email, display_name, password_hash, created_at
		FROM users
		WHERE email = $1
	`
	err := s.db.QueryRowContext(ctx, query, email).
		Scan(&user.ID, &user.Email, &user.DisplayName, &passwordHash, &user.CreatedAt)
	if err != nil {
		return model.User{}, "", err
	}

	return user, passwordHash, nil
}

func (s *AuthStore) CreateSession(ctx context.Context, userID int64, tokenHash string, expiresAt time.Time) error {
	query := `
		INSERT INTO user_sessions (user_id, token_hash, expires_at)
		VALUES ($1, $2, $3)
	`
	_, err := s.db.ExecContext(ctx, query, userID, tokenHash, expiresAt)
	return err
}

func (s *AuthStore) GetUserBySessionHash(ctx context.Context, tokenHash string) (model.User, error) {
	var user model.User
	query := `
		SELECT u.id, u.email, u.display_name, u.created_at
		FROM user_sessions s
		INNER JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = $1 AND s.expires_at > NOW()
	`

	err := s.db.QueryRowContext(ctx, query, tokenHash).
		Scan(&user.ID, &user.Email, &user.DisplayName, &user.CreatedAt)
	if err != nil {
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
