package store

import (
	"context"
	"database/sql"
	"time"

	"real_time_chat_app/internal/model"
)

type SignedRoomStore struct {
	db *sql.DB
}

func NewSignedRoomStore(db *sql.DB) *SignedRoomStore {
	return &SignedRoomStore{db: db}
}

func (s *SignedRoomStore) EnsureSchema(ctx context.Context) error {
	query := `
	CREATE TABLE IF NOT EXISTS signed_rooms (
		room_name VARCHAR(64) PRIMARY KEY,
		owner_user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		owner_display_name VARCHAR(64) NOT NULL,
		expires_at TIMESTAMPTZ NOT NULL,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);

	CREATE INDEX IF NOT EXISTS idx_signed_rooms_owner_user_id ON signed_rooms(owner_user_id);
	CREATE INDEX IF NOT EXISTS idx_signed_rooms_expires_at ON signed_rooms(expires_at);
	`
	_, err := s.db.ExecContext(ctx, query)
	return err
}

func (s *SignedRoomStore) CreateSignedRoom(ctx context.Context, roomName string, ownerUserID int64, ownerDisplayName string, expiresAt time.Time) (model.SignedRoom, error) {
	var room model.SignedRoom
	query := `
		INSERT INTO signed_rooms (room_name, owner_user_id, owner_display_name, expires_at)
		VALUES ($1, $2, $3, $4)
		RETURNING room_name, owner_user_id, owner_display_name, expires_at, created_at, updated_at
	`
	err := s.db.QueryRowContext(ctx, query, roomName, ownerUserID, ownerDisplayName, expiresAt).
		Scan(&room.RoomName, &room.OwnerUserID, &room.OwnerDisplayName, &room.ExpiresAt, &room.CreatedAt, &room.UpdatedAt)
	if err != nil {
		return model.SignedRoom{}, err
	}
	return room, nil
}

func (s *SignedRoomStore) GetSignedRoomByName(ctx context.Context, roomName string) (model.SignedRoom, error) {
	var room model.SignedRoom
	query := `
		SELECT room_name, owner_user_id, owner_display_name, expires_at, created_at, updated_at
		FROM signed_rooms
		WHERE room_name = $1
	`
	err := s.db.QueryRowContext(ctx, query, roomName).
		Scan(&room.RoomName, &room.OwnerUserID, &room.OwnerDisplayName, &room.ExpiresAt, &room.CreatedAt, &room.UpdatedAt)
	if err != nil {
		return model.SignedRoom{}, err
	}
	return room, nil
}

func (s *SignedRoomStore) UpdateSignedRoomExpiry(ctx context.Context, roomName string, ownerUserID int64, ownerDisplayName string, expiresAt time.Time) (model.SignedRoom, error) {
	var room model.SignedRoom
	query := `
		UPDATE signed_rooms
		SET owner_display_name = $1, expires_at = $2, updated_at = NOW()
		WHERE room_name = $3 AND owner_user_id = $4
		RETURNING room_name, owner_user_id, owner_display_name, expires_at, created_at, updated_at
	`
	err := s.db.QueryRowContext(ctx, query, ownerDisplayName, expiresAt, roomName, ownerUserID).
		Scan(&room.RoomName, &room.OwnerUserID, &room.OwnerDisplayName, &room.ExpiresAt, &room.CreatedAt, &room.UpdatedAt)
	if err != nil {
		return model.SignedRoom{}, err
	}
	return room, nil
}

func (s *SignedRoomStore) ListOwnedSignedRooms(ctx context.Context, ownerUserID int64) ([]model.SignedRoom, error) {
	query := `
		SELECT room_name, owner_user_id, owner_display_name, expires_at, created_at, updated_at
		FROM signed_rooms
		WHERE owner_user_id = $1 AND expires_at > NOW()
		ORDER BY expires_at DESC, updated_at DESC
	`
	rows, err := s.db.QueryContext(ctx, query, ownerUserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var rooms []model.SignedRoom
	for rows.Next() {
		var room model.SignedRoom
		if err := rows.Scan(&room.RoomName, &room.OwnerUserID, &room.OwnerDisplayName, &room.ExpiresAt, &room.CreatedAt, &room.UpdatedAt); err != nil {
			return nil, err
		}
		rooms = append(rooms, room)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return rooms, nil
}

func (s *SignedRoomStore) DeleteSignedRoomByName(ctx context.Context, roomName string) error {
	query := `DELETE FROM signed_rooms WHERE room_name = $1`
	_, err := s.db.ExecContext(ctx, query, roomName)
	return err
}

func (s *SignedRoomStore) DeleteExpiredSignedRooms(ctx context.Context, now time.Time) (int64, error) {
	query := `DELETE FROM signed_rooms WHERE expires_at <= $1`
	result, err := s.db.ExecContext(ctx, query, now)
	if err != nil {
		return 0, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	return count, nil
}
