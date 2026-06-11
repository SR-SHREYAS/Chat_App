package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"real_time_chat_app/internal/model"

	"github.com/oklog/ulid/v2"
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

	createTableQuery := `
	CREATE TABLE IF NOT EXISTS signed_rooms (
		id TEXT PRIMARY KEY CHECK (char_length(id) = 26),
		room_name TEXT NOT NULL CHECK (char_length(room_name) <= 64),
		owner_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		entry_code TEXT NOT NULL UNIQUE CHECK (entry_code ~ '^[0-9]{4}$'),
		expires_at TIMESTAMPTZ NOT NULL,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);
	`

	constraintQuery := `
	DO $$
	BEGIN
		IF NOT EXISTS (
			SELECT 1 FROM pg_constraint
			WHERE conname = 'signed_rooms_entry_code_key'
				AND conrelid = 'signed_rooms'::regclass
		) THEN
			ALTER TABLE signed_rooms
				ADD CONSTRAINT signed_rooms_entry_code_key UNIQUE (entry_code);
		END IF;
	END $$;
	`

	indexQuery := `
	CREATE INDEX IF NOT EXISTS idx_signed_rooms_owner_user_id ON signed_rooms(owner_user_id);
	CREATE INDEX IF NOT EXISTS idx_signed_rooms_expires_at_id ON signed_rooms(expires_at, id);
	`

	historyTableQuery := `
	CREATE TABLE IF NOT EXISTS room_memberships (
		room_id TEXT NOT NULL REFERENCES signed_rooms(id) ON DELETE CASCADE,
		user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		role TEXT NOT NULL CHECK (role IN ('owner', 'member')),
		joined_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		last_visited_at TIMESTAMPTZ,
		PRIMARY KEY (room_id, user_id)
	);
	`

	historyIndexQuery := `
	CREATE INDEX IF NOT EXISTS idx_room_memberships_user_id ON room_memberships(user_id);
	CREATE INDEX IF NOT EXISTS idx_room_memberships_room_id ON room_memberships(room_id);
	`

	if _, err = tx.ExecContext(ctx, createTableQuery); err != nil {
		return fmt.Errorf("create signed_rooms table: %w", err)
	}
	if _, err = tx.ExecContext(ctx, constraintQuery); err != nil {
		return fmt.Errorf("create signed_rooms constraints: %w", err)
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
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit signed_rooms schema transaction: %w", err)
	}
	return nil
}

func (s *SignedRoomStore) CreateSignedRoom(ctx context.Context, roomName string, ownerUserID string, entryCode string, expiresAt time.Time) (model.SignedRoom, error) {
	var room model.SignedRoom
	roomID := ulid.Make().String()
	query := `
		INSERT INTO signed_rooms (id, room_name, owner_user_id, entry_code, expires_at)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, room_name, owner_user_id, entry_code, expires_at, created_at, updated_at
	`
	err := s.db.QueryRowContext(ctx, query, roomID, roomName, ownerUserID, entryCode, expiresAt).
		Scan(&room.ID, &room.RoomName, &room.OwnerUserID, &room.EntryCode, &room.ExpiresAt, &room.CreatedAt, &room.UpdatedAt)
	if err != nil {
		return model.SignedRoom{}, err
	}
	return room, nil
}

func (s *SignedRoomStore) GetSignedRoomByID(ctx context.Context, roomID string) (model.SignedRoom, error) {
	var room model.SignedRoom
	query := `
		SELECT sr.id, sr.room_name, sr.owner_user_id, u.username, sr.entry_code, sr.expires_at, sr.created_at, sr.updated_at
		FROM signed_rooms sr
		INNER JOIN users u ON u.id = sr.owner_user_id
		WHERE sr.id = $1
	`
	err := s.db.QueryRowContext(ctx, query, roomID).
		Scan(&room.ID, &room.RoomName, &room.OwnerUserID, &room.OwnerUserName, &room.EntryCode, &room.ExpiresAt, &room.CreatedAt, &room.UpdatedAt)
	if err != nil {
		return model.SignedRoom{}, err
	}
	return room, nil
}

func (s *SignedRoomStore) UpdateSignedRoomExpiry(ctx context.Context, roomID string, ownerUserID string, entryCode string, expiresAt time.Time) (model.SignedRoom, error) {
	var room model.SignedRoom
	query := `
		UPDATE signed_rooms
		SET entry_code = $1, expires_at = $2, updated_at = NOW()
		WHERE id = $3 AND owner_user_id = $4
		RETURNING id, room_name, owner_user_id, entry_code, expires_at, created_at, updated_at
	`
	err := s.db.QueryRowContext(ctx, query, entryCode, expiresAt, roomID, ownerUserID).
		Scan(&room.ID, &room.RoomName, &room.OwnerUserID, &room.EntryCode, &room.ExpiresAt, &room.CreatedAt, &room.UpdatedAt)
	if err != nil {
		return model.SignedRoom{}, err
	}
	return room, nil
}

func (s *SignedRoomStore) ListOwnedSignedRooms(ctx context.Context, ownerUserID string) ([]model.SignedRoom, error) {
	query := `
		SELECT sr.id, sr.room_name, sr.owner_user_id, u.username, sr.entry_code, sr.expires_at, sr.created_at, sr.updated_at
		FROM signed_rooms sr
		INNER JOIN users u ON u.id = sr.owner_user_id
		WHERE sr.owner_user_id = $1 AND sr.expires_at > NOW()
		ORDER BY sr.expires_at DESC, sr.updated_at DESC
	`
	rows, err := s.db.QueryContext(ctx, query, ownerUserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var rooms []model.SignedRoom
	for rows.Next() {
		var room model.SignedRoom
		if err := rows.Scan(&room.ID, &room.RoomName, &room.OwnerUserID, &room.OwnerUserName, &room.EntryCode, &room.ExpiresAt, &room.CreatedAt, &room.UpdatedAt); err != nil {
			return nil, err
		}
		rooms = append(rooms, room)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return rooms, nil
}

func (s *SignedRoomStore) DeleteSignedRoomByID(ctx context.Context, roomID string) error {
	query := `DELETE FROM signed_rooms WHERE id = $1`
	_, err := s.db.ExecContext(ctx, query, roomID)
	return err
}

func (s *SignedRoomStore) DeleteExpiredSignedRooms(ctx context.Context, now time.Time) ([]string, error) {
	const batchSize = 1000

	var roomIDs []string
	for {
		query := `
			WITH expired AS (
				SELECT id
				FROM signed_rooms
				WHERE expires_at <= $1
				ORDER BY expires_at, id
				LIMIT $2
			)
			DELETE FROM signed_rooms sr
			USING expired e
			WHERE sr.id = e.id
			RETURNING sr.id
		`
		rows, err := s.db.QueryContext(ctx, query, now, batchSize)
		if err != nil {
			return nil, err
		}

		// Keep batches deterministic and aligned with idx_signed_rooms_expires_at_id.
		batchIDs := make([]string, 0, batchSize)
		for rows.Next() {
			var roomID string
			if err := rows.Scan(&roomID); err != nil {
				rows.Close()
				return nil, err
			}
			batchIDs = append(batchIDs, roomID)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}

		roomIDs = append(roomIDs, batchIDs...)
		if len(batchIDs) < batchSize {
			return roomIDs, nil
		}
	}
}

func (s *SignedRoomStore) RecordRoomMembership(ctx context.Context, userID string, roomID string, role string) error {
	query := `
		INSERT INTO room_memberships (room_id, user_id, role, joined_at, last_visited_at)
		VALUES ($1, $2, $3, NOW(), NOW())
		ON CONFLICT (room_id, user_id)
		DO UPDATE SET last_visited_at = EXCLUDED.last_visited_at
	`
	if _, err := s.db.ExecContext(ctx, query, roomID, userID, role); err != nil {
		return err
	}
	return nil
}

func (s *SignedRoomStore) GetRoomMembership(ctx context.Context, userID string, roomID string) (model.RoomHistory, error) {
	var item model.RoomHistory
	query := `
		SELECT room_id, role, joined_at, COALESCE(last_visited_at, joined_at)
		FROM room_memberships
		WHERE user_id = $1 AND room_id = $2
	`
	err := s.db.QueryRowContext(ctx, query, userID, roomID).
		Scan(&item.RoomID, &item.Role, &item.JoinedAt, &item.LastVisitedAt)
	if err != nil {
		return model.RoomHistory{}, err
	}
	return item, nil
}

func (s *SignedRoomStore) PruneRoomMemberships(ctx context.Context, userID string, limit int) error {
	if limit < 1 {
		limit = 1
	}

	query := `
		DELETE FROM room_memberships
		WHERE user_id = $1
			AND room_id NOT IN (
				SELECT room_id
				FROM (
					SELECT room_id, MAX(COALESCE(last_visited_at, joined_at)) AS latest_visit
					FROM room_memberships
					WHERE user_id = $1
					GROUP BY room_id
					ORDER BY latest_visit DESC
					LIMIT $2
				) kept_rooms
			)
	`
	_, err := s.db.ExecContext(ctx, query, userID, limit)
	return err
}

func (s *SignedRoomStore) ListRoomMemberships(ctx context.Context, userID string, limit int) ([]model.RoomHistory, error) {
	if limit < 1 {
		limit = 1
	}

	query := `
		WITH kept_rooms AS (
			SELECT room_id, MAX(COALESCE(last_visited_at, joined_at)) AS latest_visit
			FROM room_memberships
			WHERE user_id = $1
			GROUP BY room_id
			ORDER BY latest_visit DESC
			LIMIT $2
		)
		SELECT
			h.room_id,
			sr.room_name,
			h.role,
			sr.owner_user_id,
			u.username AS owner_username,
			sr.entry_code,
			sr.expires_at,
			h.joined_at,
			COALESCE(h.last_visited_at, h.joined_at),
			sr.expires_at > NOW() AS active
		FROM room_memberships h
		INNER JOIN kept_rooms kr ON kr.room_id = h.room_id
		INNER JOIN signed_rooms sr ON sr.id = h.room_id
		INNER JOIN users u ON u.id = sr.owner_user_id
		WHERE h.user_id = $1
		ORDER BY active DESC, COALESCE(h.last_visited_at, h.joined_at) DESC
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
		if err := rows.Scan(&item.RoomID, &item.RoomName, &item.Role, &item.OwnerUserID, &item.OwnerUserName, &item.EntryCode, &expiresAt, &item.JoinedAt, &item.LastVisitedAt, &item.Active); err != nil {
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
