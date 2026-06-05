package store

import (
	"context"
	"database/sql"

	"real_time_chat_app/internal/model"
)

type MessageStore struct {
	db *sql.DB
}

func NewMessageStore(db *sql.DB) *MessageStore {
	return &MessageStore{db: db}
}

func (s *MessageStore) EnsureSchema(ctx context.Context) error {
	exec := `
	CREATE TABLE IF NOT EXISTS messages (
		id SERIAL PRIMARY KEY,
		room_name VARCHAR(255) NOT NULL,
		user_name VARCHAR(255) NOT NULL,
		message TEXT NOT NULL,
		created_at TIMESTAMPTZ DEFAULT NOW()
	);
	`
	_, err := s.db.ExecContext(ctx, exec)
	return err
}

func (s *MessageStore) SaveMessage(ctx context.Context, roomName, userName, message string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO messages (room_name, user_name, message)
		VALUES ($1, $2, $3)
	`, roomName, userName, message)
	return err
}

func (s *MessageStore) GetRecentMessages(ctx context.Context, roomName string, limit int) ([]model.Message, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT user_name, message FROM messages
		WHERE room_name = $1
		ORDER BY created_at ASC
		LIMIT $2
	`, roomName, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	messages := make([]model.Message, 0, limit)
	for rows.Next() {
		var msg model.Message
		if err := rows.Scan(&msg.UserName, &msg.Message); err != nil {
			return nil, err
		}
		messages = append(messages, msg)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return messages, nil
}

func (s *MessageStore) DeleteRoomMessages(ctx context.Context, roomName string) error {
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM messages
		WHERE room_name = $1
	`, roomName)
	return err
}

func (s *MessageStore) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}
