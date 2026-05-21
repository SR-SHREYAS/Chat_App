package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"real_time_chat_app/internal/model"
)

type SignedRoomStore struct {
	db *sql.DB
}

func NewSignedRoomStore(db *sql.DB) *SignedRoomStore {
	return &SignedRoomStore{db: db}
}

func (s *SignedRoomStore) EnsureSchema(ctx context.Context) (err error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin signed_rooms schema transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	createTableQuery := fmt.Sprintf(`
	CREATE TABLE IF NOT EXISTS signed_rooms (
		room_name VARCHAR(64) PRIMARY KEY,
		owner_user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		owner_display_name VARCHAR(64) NOT NULL,
		entry_code VARCHAR(%d) NOT NULL,
		expires_at TIMESTAMPTZ NOT NULL,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);
	`, model.SignedRoomEntryCodeLength)

	indexQuery := `
	CREATE INDEX IF NOT EXISTS idx_signed_rooms_owner_user_id ON signed_rooms(owner_user_id);
	CREATE INDEX IF NOT EXISTS idx_signed_rooms_expires_at ON signed_rooms(expires_at);
	`

	historyTableQuery := `
	CREATE TABLE IF NOT EXISTS room_memberships (
		user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		room_name VARCHAR(64) NOT NULL,
		role VARCHAR(16) NOT NULL CHECK (role IN ('owned', 'joined')),
		last_visited_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		PRIMARY KEY (user_id, room_name, role)
	);
	`

	historyIndexQuery := `
	CREATE INDEX IF NOT EXISTS idx_room_memberships_user_last_visited ON room_memberships(user_id, last_visited_at DESC);
	CREATE INDEX IF NOT EXISTS idx_room_memberships_room_name ON room_memberships(room_name);
	`

	minCode := model.SignedRoomEntryCodeMinValue()
	rangeSize := model.SignedRoomEntryCodeRangeSize()
	migrateEntryCodeQuery := fmt.Sprintf(`
	ALTER TABLE signed_rooms
		ADD COLUMN IF NOT EXISTS entry_code VARCHAR(%d);

	UPDATE signed_rooms
	SET entry_code = LPAD((%d + FLOOR(RANDOM() * %d))::INT::TEXT, %d, '0')
	WHERE entry_code IS NULL OR entry_code !~ '^[0-9]{%d}$';

	ALTER TABLE signed_rooms
		ALTER COLUMN entry_code SET NOT NULL;
	`, model.SignedRoomEntryCodeLength, minCode, rangeSize, model.SignedRoomEntryCodeLength, model.SignedRoomEntryCodeLength)

	if _, err = tx.ExecContext(ctx, createTableQuery); err != nil {
		return fmt.Errorf("create signed_rooms table: %w", err)
	}
	if _, err = tx.ExecContext(ctx, indexQuery); err != nil {
		return fmt.Errorf("create signed_rooms indexes: %w", err)
	}
	if _, err = tx.ExecContext(ctx, historyTableQuery); err != nil {
		return fmt.Errorf("create room_memberships table: %w", err)
	}
	if _, err = tx.ExecContext(ctx, historyIndexQuery); err != nil {
		return fmt.Errorf("create room_memberships indexes: %w", err)
	}
	if _, err = tx.ExecContext(ctx, migrateEntryCodeQuery); err != nil {
		return fmt.Errorf("migrate signed_rooms entry_code: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit signed_rooms schema transaction: %w", err)
	}
	return nil
}

func (s *SignedRoomStore) CreateSignedRoom(ctx context.Context, roomName string, ownerUserID int64, ownerDisplayName, entryCode string, expiresAt time.Time) (model.SignedRoom, error) {
	var room model.SignedRoom
	query := `
		INSERT INTO signed_rooms (room_name, owner_user_id, owner_display_name, entry_code, expires_at)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING room_name, owner_user_id, owner_display_name, entry_code, expires_at, created_at, updated_at
	`
	err := s.db.QueryRowContext(ctx, query, roomName, ownerUserID, ownerDisplayName, entryCode, expiresAt).
		Scan(&room.RoomName, &room.OwnerUserID, &room.OwnerDisplayName, &room.EntryCode, &room.ExpiresAt, &room.CreatedAt, &room.UpdatedAt)
	if err != nil {
		return model.SignedRoom{}, err
	}
	return room, nil
}

func (s *SignedRoomStore) GetSignedRoomByName(ctx context.Context, roomName string) (model.SignedRoom, error) {
	var room model.SignedRoom
	query := `
		SELECT room_name, owner_user_id, owner_display_name, entry_code, expires_at, created_at, updated_at
		FROM signed_rooms
		WHERE room_name = $1
	`
	err := s.db.QueryRowContext(ctx, query, roomName).
		Scan(&room.RoomName, &room.OwnerUserID, &room.OwnerDisplayName, &room.EntryCode, &room.ExpiresAt, &room.CreatedAt, &room.UpdatedAt)
	if err != nil {
		return model.SignedRoom{}, err
	}
	return room, nil
}

func (s *SignedRoomStore) UpdateSignedRoomExpiry(ctx context.Context, roomName string, ownerUserID int64, ownerDisplayName, entryCode string, expiresAt time.Time) (model.SignedRoom, error) {
	var room model.SignedRoom
	query := `
		UPDATE signed_rooms
		SET owner_display_name = $1, entry_code = $2, expires_at = $3, updated_at = NOW()
		WHERE room_name = $4 AND owner_user_id = $5
		RETURNING room_name, owner_user_id, owner_display_name, entry_code, expires_at, created_at, updated_at
	`
	err := s.db.QueryRowContext(ctx, query, ownerDisplayName, entryCode, expiresAt, roomName, ownerUserID).
		Scan(&room.RoomName, &room.OwnerUserID, &room.OwnerDisplayName, &room.EntryCode, &room.ExpiresAt, &room.CreatedAt, &room.UpdatedAt)
	if err != nil {
		return model.SignedRoom{}, err
	}
	return room, nil
}

func (s *SignedRoomStore) ListOwnedSignedRooms(ctx context.Context, ownerUserID int64) ([]model.SignedRoom, error) {
	query := `
		SELECT room_name, owner_user_id, owner_display_name, entry_code, expires_at, created_at, updated_at
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
		if err := rows.Scan(&room.RoomName, &room.OwnerUserID, &room.OwnerDisplayName, &room.EntryCode, &room.ExpiresAt, &room.CreatedAt, &room.UpdatedAt); err != nil {
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

func (s *SignedRoomStore) RecordRoomMembership(ctx context.Context, userID int64, roomName, role string) error {
	query := `
		INSERT INTO room_memberships (user_id, room_name, role, last_visited_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (user_id, room_name, role)
		DO UPDATE SET last_visited_at = EXCLUDED.last_visited_at
	`
	if _, err := s.db.ExecContext(ctx, query, userID, roomName, role); err != nil {
		return err
	}
	return nil
}

func (s *SignedRoomStore) PruneRoomMemberships(ctx context.Context, userID int64, limit int) error {
	if limit < 1 {
		limit = 1
	}

	query := `
		DELETE FROM room_memberships
		WHERE user_id = $1
			AND room_name NOT IN (
				SELECT room_name
				FROM (
					SELECT room_name, MAX(last_visited_at) AS latest_visit
					FROM room_memberships
					WHERE user_id = $1
					GROUP BY room_name
					ORDER BY latest_visit DESC
					LIMIT $2
				) kept_rooms
			)
	`
	_, err := s.db.ExecContext(ctx, query, userID, limit)
	return err
}

func (s *SignedRoomStore) ListRoomMemberships(ctx context.Context, userID int64, limit int) ([]model.RoomHistory, error) {
	if limit < 1 {
		limit = 1
	}

	query := `
		WITH kept_rooms AS (
			SELECT room_name, MAX(last_visited_at) AS latest_visit
			FROM room_memberships
			WHERE user_id = $1
			GROUP BY room_name
			ORDER BY latest_visit DESC
			LIMIT $2
		)
		SELECT
			h.room_name,
			h.role,
			COALESCE(sr.owner_display_name, ''),
			COALESCE(sr.entry_code, ''),
			sr.expires_at,
			h.last_visited_at,
			COALESCE(sr.room_name IS NOT NULL AND sr.expires_at > NOW(), false) AS active
		FROM room_memberships h
		INNER JOIN kept_rooms kr ON kr.room_name = h.room_name
		LEFT JOIN signed_rooms sr ON sr.room_name = h.room_name
		WHERE h.user_id = $1
		ORDER BY active DESC, h.last_visited_at DESC
	`
	rows, err := s.db.QueryContext(ctx, query, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var history []model.RoomHistory
	for rows.Next() {
		var item model.RoomHistory
		var expiresAt sql.NullTime
		if err := rows.Scan(&item.RoomName, &item.Role, &item.OwnerDisplayName, &item.EntryCode, &expiresAt, &item.LastVisitedAt, &item.Active); err != nil {
			return nil, err
		}
		if expiresAt.Valid {
			item.ExpiresAt = expiresAt.Time
		}
		history = append(history, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return history, nil
}
