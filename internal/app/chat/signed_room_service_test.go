package chat

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"real_time_chat_app/internal/model"
)

type fakeSignedRoomStore struct {
	mu                 sync.Mutex
	rooms              map[string]model.SignedRoom
	deleteExpiredCalls int
}

func newFakeSignedRoomStore() *fakeSignedRoomStore {
	return &fakeSignedRoomStore{rooms: make(map[string]model.SignedRoom)}
}

func (s *fakeSignedRoomStore) CreateSignedRoom(_ context.Context, roomName string, ownerUserID int64, ownerDisplayName, entryCode string, expiresAt time.Time) (model.SignedRoom, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.rooms[roomName]; exists {
		return model.SignedRoom{}, errors.New("room already exists")
	}

	now := time.Now().UTC()
	room := model.SignedRoom{
		RoomName:         roomName,
		OwnerUserID:      ownerUserID,
		OwnerDisplayName: ownerDisplayName,
		EntryCode:        entryCode,
		ExpiresAt:        expiresAt,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	s.rooms[roomName] = room
	return room, nil
}

func (s *fakeSignedRoomStore) GetSignedRoomByName(_ context.Context, roomName string) (model.SignedRoom, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	room, ok := s.rooms[roomName]
	if !ok {
		return model.SignedRoom{}, sql.ErrNoRows
	}
	return room, nil
}

func (s *fakeSignedRoomStore) UpdateSignedRoomExpiry(_ context.Context, roomName string, ownerUserID int64, ownerDisplayName, entryCode string, expiresAt time.Time) (model.SignedRoom, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	room, ok := s.rooms[roomName]
	if !ok {
		return model.SignedRoom{}, sql.ErrNoRows
	}
	if room.OwnerUserID != ownerUserID {
		return model.SignedRoom{}, sql.ErrNoRows
	}

	room.OwnerDisplayName = ownerDisplayName
	room.EntryCode = entryCode
	room.ExpiresAt = expiresAt
	room.UpdatedAt = time.Now().UTC()
	s.rooms[roomName] = room
	return room, nil
}

func (s *fakeSignedRoomStore) ListOwnedSignedRooms(_ context.Context, ownerUserID int64) ([]model.SignedRoom, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var out []model.SignedRoom
	for _, room := range s.rooms {
		if room.OwnerUserID == ownerUserID {
			out = append(out, room)
		}
	}
	return out, nil
}

func (s *fakeSignedRoomStore) DeleteSignedRoomByName(_ context.Context, roomName string) error {
	s.mu.Lock()
	delete(s.rooms, roomName)
	s.mu.Unlock()
	return nil
}

func (s *fakeSignedRoomStore) DeleteExpiredSignedRooms(_ context.Context, now time.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.deleteExpiredCalls++

	var removed int64
	for name, room := range s.rooms {
		if !room.ExpiresAt.After(now) {
			delete(s.rooms, name)
			removed++
		}
	}
	return removed, nil
}

func (s *fakeSignedRoomStore) DeleteExpiredCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deleteExpiredCalls
}

type noopMessageStore struct{}

func (noopMessageStore) SaveMessage(context.Context, string, string, string) error { return nil }
func (noopMessageStore) GetRecentMessages(context.Context, string, int) ([]model.Message, error) {
	return nil, nil
}
func (noopMessageStore) Ping(context.Context) error { return nil }

func TestHandleCreateSignedRoom_DefaultTTL(t *testing.T) {
	service := NewService(noopMessageStore{})
	service.BindSignedRoomStore(newFakeSignedRoomStore())

	room, err := service.HandleCreateSignedRoom(context.Background(), "alpha", 1, "owner", 0)
	if err != nil {
		t.Fatalf("create signed room: %v", err)
	}

	remaining := time.Until(room.ExpiresAt)
	if remaining < 9*time.Minute || remaining > 11*time.Minute {
		t.Fatalf("expected default TTL around 10 minutes, got %v", remaining)
	}
	if len(room.EntryCode) != 4 {
		t.Fatalf("expected 4-digit entry code, got %q", room.EntryCode)
	}
}

func TestHandleCreateSignedRoom_InvalidRoomName(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		service := NewService(noopMessageStore{})
		service.BindSignedRoomStore(newFakeSignedRoomStore())

		if _, err := service.HandleCreateSignedRoom(context.Background(), "", 1, "owner", 0); !errors.Is(err, ErrInvalidRoomName) {
			t.Fatalf("expected ErrInvalidRoomName, got %v", err)
		}
	})

	t.Run("whitespace", func(t *testing.T) {
		service := NewService(noopMessageStore{})
		service.BindSignedRoomStore(newFakeSignedRoomStore())

		if _, err := service.HandleCreateSignedRoom(context.Background(), "   ", 1, "owner", 0); !errors.Is(err, ErrInvalidRoomName) {
			t.Fatalf("expected ErrInvalidRoomName, got %v", err)
		}
	})
}

func TestHandleCreateSignedRoom_InvalidOwner(t *testing.T) {
	service := NewService(noopMessageStore{})
	service.BindSignedRoomStore(newFakeSignedRoomStore())

	if _, err := service.HandleCreateSignedRoom(context.Background(), "alpha", 0, "owner", 0); !errors.Is(err, ErrInvalidRoomOwner) {
		t.Fatalf("expected ErrInvalidRoomOwner, got %v", err)
	}
}

func TestHandleCreateSignedRoom_InvalidTTL(t *testing.T) {
	service := NewService(noopMessageStore{})
	service.BindSignedRoomStore(newFakeSignedRoomStore())

	if _, err := service.HandleCreateSignedRoom(context.Background(), "alpha", 1, "owner", MaxSignedRoomTTL+time.Minute); !errors.Is(err, ErrSignedRoomTTLTooLarge) {
		t.Fatalf("expected ErrSignedRoomTTLTooLarge, got %v", err)
	}
}

func TestHandleCreateSignedRoom_StoreUnavailable(t *testing.T) {
	service := NewService(noopMessageStore{})

	if _, err := service.HandleCreateSignedRoom(context.Background(), "alpha", 1, "owner", 0); !errors.Is(err, ErrSignedRoomUnavailable) {
		t.Fatalf("expected ErrSignedRoomUnavailable, got %v", err)
	}
}

func TestHandleCreateSignedRoom_UpdatesExistingRoomForSameOwner(t *testing.T) {
	store := newFakeSignedRoomStore()
	service := NewService(noopMessageStore{})
	service.BindSignedRoomStore(store)

	ctx := context.Background()

	initialRoom, err := service.HandleCreateSignedRoom(ctx, "alpha", 1, "owner-a", 5*time.Minute)
	if err != nil {
		t.Fatalf("initial create: %v", err)
	}

	originalCreatedAt := initialRoom.CreatedAt
	originalUpdatedAt := initialRoom.UpdatedAt
	originalExpiresAt := initialRoom.ExpiresAt
	originalEntryCode := initialRoom.EntryCode

	updatedRoom, err := service.HandleCreateSignedRoom(ctx, "alpha", 1, "owner-b", 10*time.Minute)
	if err != nil {
		t.Fatalf("recreate with same owner: %v", err)
	}

	if updatedRoom.RoomName != initialRoom.RoomName {
		t.Fatalf("expected room name %q, got %q", initialRoom.RoomName, updatedRoom.RoomName)
	}
	if updatedRoom.OwnerUserID != initialRoom.OwnerUserID {
		t.Fatalf("expected owner id %d, got %d", initialRoom.OwnerUserID, updatedRoom.OwnerUserID)
	}
	if len(store.rooms) != 1 {
		t.Fatalf("expected one room record, got %d", len(store.rooms))
	}
	if !updatedRoom.ExpiresAt.After(originalExpiresAt) {
		t.Fatalf("expected ExpiresAt to move forward, before=%v after=%v", originalExpiresAt, updatedRoom.ExpiresAt)
	}
	if updatedRoom.EntryCode == originalEntryCode {
		t.Fatalf("expected entry code rotation on recreate, got same code %q", updatedRoom.EntryCode)
	}
	if updatedRoom.OwnerDisplayName != "owner-b" {
		t.Fatalf("expected updated display name owner-b, got %q", updatedRoom.OwnerDisplayName)
	}
	if !updatedRoom.CreatedAt.Equal(originalCreatedAt) {
		t.Fatalf("expected CreatedAt unchanged: before=%v after=%v", originalCreatedAt, updatedRoom.CreatedAt)
	}
	if !updatedRoom.UpdatedAt.After(originalUpdatedAt) {
		t.Fatalf("expected UpdatedAt to move forward: before=%v after=%v", originalUpdatedAt, updatedRoom.UpdatedAt)
	}
}

func TestHandleCreateSignedRoom_RejectsOtherOwner(t *testing.T) {
	service := NewService(noopMessageStore{})
	service.BindSignedRoomStore(newFakeSignedRoomStore())

	if _, err := service.HandleCreateSignedRoom(context.Background(), "alpha", 1, "owner-a", 10*time.Minute); err != nil {
		t.Fatalf("initial create: %v", err)
	}

	if _, err := service.HandleCreateSignedRoom(context.Background(), "alpha", 2, "owner-b", 10*time.Minute); !errors.Is(err, ErrRoomOwnedByAnotherUser) {
		t.Fatalf("expected ErrRoomOwnedByAnotherUser, got %v", err)
	}
}

func TestHandleGetSignedRoomStatus_EdgeCases(t *testing.T) {
	t.Run("invalid room name", func(t *testing.T) {
		service := NewService(noopMessageStore{})
		service.BindSignedRoomStore(newFakeSignedRoomStore())

		for _, name := range []string{"", " ", "\t"} {
			_, exists, err := service.HandleGetSignedRoomStatus(context.Background(), name)
			if !errors.Is(err, ErrInvalidRoomName) {
				t.Fatalf("room %q: expected ErrInvalidRoomName, got %v", name, err)
			}
			if exists {
				t.Fatalf("room %q: expected exists false", name)
			}
		}
	})

	t.Run("unbound store", func(t *testing.T) {
		service := NewService(noopMessageStore{})

		_, exists, err := service.HandleGetSignedRoomStatus(context.Background(), "alpha")
		if !errors.Is(err, ErrSignedRoomUnavailable) {
			t.Fatalf("expected ErrSignedRoomUnavailable, got %v", err)
		}
		if exists {
			t.Fatalf("expected exists false")
		}
	})

	t.Run("store error propagated", func(t *testing.T) {
		service := NewService(noopMessageStore{})
		service.BindSignedRoomStore(&errorSignedRoomStore{err: fmt.Errorf("boom")})

		_, exists, err := service.HandleGetSignedRoomStatus(context.Background(), "alpha")
		if err == nil {
			t.Fatalf("expected non-nil error")
		}
		if errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("expected non-sql.ErrNoRows error, got %v", err)
		}
		if exists {
			t.Fatalf("expected exists false")
		}
	})

	t.Run("sql.ErrNoRows as not exists", func(t *testing.T) {
		service := NewService(noopMessageStore{})
		service.BindSignedRoomStore(&errorSignedRoomStore{err: sql.ErrNoRows})

		_, exists, err := service.HandleGetSignedRoomStatus(context.Background(), "alpha")
		if err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}
		if exists {
			t.Fatalf("expected exists false")
		}
	})
}

func TestHandleJoinSignedRoom(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		store := newFakeSignedRoomStore()
		store.rooms["alpha"] = model.SignedRoom{
			RoomName:         "alpha",
			OwnerUserID:      1,
			OwnerDisplayName: "owner",
			EntryCode:        "1234",
			ExpiresAt:        time.Now().UTC().Add(5 * time.Minute),
		}

		service := NewService(noopMessageStore{})
		service.BindSignedRoomStore(store)

		room, err := service.HandleJoinSignedRoom(context.Background(), "alpha", "1234")
		if err != nil {
			t.Fatalf("join signed room: %v", err)
		}
		if room.RoomName != "alpha" {
			t.Fatalf("expected room alpha, got %s", room.RoomName)
		}
	})

	t.Run("missing", func(t *testing.T) {
		service := NewService(noopMessageStore{})
		service.BindSignedRoomStore(newFakeSignedRoomStore())

		if _, err := service.HandleJoinSignedRoom(context.Background(), "missing", "1234"); !errors.Is(err, ErrSignedRoomNotFound) {
			t.Fatalf("expected ErrSignedRoomNotFound, got %v", err)
		}
	})

	t.Run("expired", func(t *testing.T) {
		store := newFakeSignedRoomStore()
		store.rooms["expired"] = model.SignedRoom{
			RoomName:         "expired",
			OwnerUserID:      1,
			OwnerDisplayName: "owner",
			EntryCode:        "1234",
			ExpiresAt:        time.Now().UTC().Add(-1 * time.Minute),
		}

		service := NewService(noopMessageStore{})
		service.BindSignedRoomStore(store)

		if _, err := service.HandleJoinSignedRoom(context.Background(), "expired", "1234"); !errors.Is(err, ErrSignedRoomExpired) {
			t.Fatalf("expected ErrSignedRoomExpired, got %v", err)
		}
	})

	t.Run("wrong code", func(t *testing.T) {
		store := newFakeSignedRoomStore()
		store.rooms["alpha"] = model.SignedRoom{
			RoomName:         "alpha",
			OwnerUserID:      1,
			OwnerDisplayName: "owner",
			EntryCode:        "1234",
			ExpiresAt:        time.Now().UTC().Add(5 * time.Minute),
		}

		service := NewService(noopMessageStore{})
		service.BindSignedRoomStore(store)

		if _, err := service.HandleJoinSignedRoom(context.Background(), "alpha", "9999"); !errors.Is(err, ErrInvalidRoomEntryCode) {
			t.Fatalf("expected ErrInvalidRoomEntryCode, got %v", err)
		}
	})
}

func TestHandleListOwnedSignedRooms(t *testing.T) {
	t.Run("invalid owner", func(t *testing.T) {
		service := NewService(noopMessageStore{})
		service.BindSignedRoomStore(newFakeSignedRoomStore())

		if _, err := service.HandleListOwnedSignedRooms(context.Background(), 0); !errors.Is(err, ErrInvalidRoomOwner) {
			t.Fatalf("expected ErrInvalidRoomOwner, got %v", err)
		}
	})

	t.Run("filters owner and cleans expired", func(t *testing.T) {
		store := newFakeSignedRoomStore()
		store.rooms["active-owner-1"] = model.SignedRoom{
			RoomName:         "active-owner-1",
			OwnerUserID:      1,
			OwnerDisplayName: "owner-1",
			EntryCode:        "1111",
			ExpiresAt:        time.Now().UTC().Add(10 * time.Minute),
		}
		store.rooms["expired-owner-1"] = model.SignedRoom{
			RoomName:         "expired-owner-1",
			OwnerUserID:      1,
			OwnerDisplayName: "owner-1",
			EntryCode:        "2222",
			ExpiresAt:        time.Now().UTC().Add(-1 * time.Minute),
		}
		store.rooms["active-owner-2"] = model.SignedRoom{
			RoomName:         "active-owner-2",
			OwnerUserID:      2,
			OwnerDisplayName: "owner-2",
			EntryCode:        "3333",
			ExpiresAt:        time.Now().UTC().Add(10 * time.Minute),
		}

		service := NewService(noopMessageStore{})
		service.BindSignedRoomStore(store)
		service.signedRoomCleanupEvery = 0

		rooms, err := service.HandleListOwnedSignedRooms(context.Background(), 1)
		if err != nil {
			t.Fatalf("list owned signed rooms: %v", err)
		}
		if len(rooms) != 1 {
			t.Fatalf("expected 1 active room for owner 1, got %d", len(rooms))
		}
		if rooms[0].RoomName != "active-owner-1" {
			t.Fatalf("unexpected room returned: %s", rooms[0].RoomName)
		}
		if calls := store.DeleteExpiredCalls(); calls < 1 {
			t.Fatalf("expected cleanup to run before listing, calls=%d", calls)
		}
	})
}

func TestHandleGetSignedRoomStatus_ExpiredRoom(t *testing.T) {
	store := newFakeSignedRoomStore()
	store.rooms["expired"] = model.SignedRoom{
		RoomName:         "expired",
		OwnerUserID:      1,
		OwnerDisplayName: "owner",
		EntryCode:        "1234",
		ExpiresAt:        time.Now().UTC().Add(-1 * time.Minute),
	}

	service := NewService(noopMessageStore{})
	service.BindSignedRoomStore(store)

	_, exists, err := service.HandleGetSignedRoomStatus(context.Background(), "expired")
	if !errors.Is(err, ErrSignedRoomExpired) {
		t.Fatalf("expected ErrSignedRoomExpired, got %v", err)
	}
	if exists {
		t.Fatalf("expected room to not exist after expiry")
	}
}

type errorSignedRoomStore struct {
	err error
}

func (s *errorSignedRoomStore) CreateSignedRoom(context.Context, string, int64, string, string, time.Time) (model.SignedRoom, error) {
	return model.SignedRoom{}, s.err
}

func (s *errorSignedRoomStore) GetSignedRoomByName(context.Context, string) (model.SignedRoom, error) {
	return model.SignedRoom{}, s.err
}

func (s *errorSignedRoomStore) UpdateSignedRoomExpiry(context.Context, string, int64, string, string, time.Time) (model.SignedRoom, error) {
	return model.SignedRoom{}, s.err
}

func (s *errorSignedRoomStore) ListOwnedSignedRooms(context.Context, int64) ([]model.SignedRoom, error) {
	return nil, s.err
}

func (s *errorSignedRoomStore) DeleteSignedRoomByName(context.Context, string) error {
	return s.err
}

func (s *errorSignedRoomStore) DeleteExpiredSignedRooms(context.Context, time.Time) (int64, error) {
	return 0, s.err
}
