package store

import (
	"context"
	"database/sql"

	"real_time_chat_app/internal/model"

	"github.com/oklog/ulid/v2"
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
		id TEXT PRIMARY KEY CHECK (char_length(id) = 26),
		room_id TEXT NOT NULL REFERENCES signed_rooms(id) ON DELETE CASCADE,
		sender_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		message TEXT NOT NULL CHECK (char_length(message) <= 2000),
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);
	CREATE INDEX IF NOT EXISTS idx_messages_room_id ON messages(room_id);
	CREATE INDEX IF NOT EXISTS idx_messages_sender_user_id ON messages(sender_user_id);
	`
	_, err := s.db.ExecContext(ctx, exec)
	return err
}

func (s *MessageStore) SaveMessage(ctx context.Context, roomID, senderUserID, message string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO messages (id, room_id, sender_user_id, message)
		VALUES ($1, $2, $3, $4)
	`, ulid.Make().String(), roomID, senderUserID, message)
	return err
}

func (s *MessageStore) GetRecentMessages(ctx context.Context, roomID string, limit int) ([]model.Message, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, room_id, sender_user_id, users.username, message, messages.created_at
		FROM messages
		INNER JOIN users ON users.id = messages.sender_user_id
		WHERE room_id = $1
		ORDER BY messages.created_at DESC
		LIMIT $2
	`, roomID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	messages := make([]model.Message, 0, limit)
	for rows.Next() {
		var msg model.Message
		if err := rows.Scan(&msg.ID, &msg.RoomID, &msg.SenderUserID, &msg.UserName, &msg.Message, &msg.CreatedAt); err != nil {
			return nil, err
		}
		messages = append(messages, msg)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	for left, right := 0, len(messages)-1; left < right; left, right = left+1, right-1 {
		messages[left], messages[right] = messages[right], messages[left]
	}
	return messages, nil
}

func (s *MessageStore) DeleteRoomMessages(ctx context.Context, roomID string) error {
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM messages
		WHERE room_id = $1
	`, roomID)
	return err
}

func (s *MessageStore) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}
