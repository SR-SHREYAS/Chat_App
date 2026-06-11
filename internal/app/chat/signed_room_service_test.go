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
	history            []model.RoomHistory
	deleteExpiredCalls int
	createConflicts    int
	updateConflicts    int
}

func newFakeSignedRoomStore() *fakeSignedRoomStore {
	return &fakeSignedRoomStore{rooms: make(map[string]model.SignedRoom)}
}

func (s *fakeSignedRoomStore) CreateSignedRoom(_ context.Context, roomName string, ownerUserID string, entryCode string, expiresAt time.Time) (model.SignedRoom, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.createConflicts > 0 {
		s.createConflicts--
		return model.SignedRoom{}, duplicateEntryCodeError{}
	}

	roomID := fmt.Sprintf("fake-%d", time.Now().UnixNano())

	now := time.Now().UTC()
	room := model.SignedRoom{
		ID:          roomID,
		RoomName:    roomName,
		OwnerUserID: ownerUserID,
		EntryCode:   entryCode,
		ExpiresAt:   expiresAt,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	s.rooms[roomID] = room
	return room, nil
}

func (s *fakeSignedRoomStore) GetSignedRoomByID(_ context.Context, roomID string) (model.SignedRoom, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	room, ok := s.rooms[roomID]
	if ok {
		return room, nil
	}
	for _, room = range s.rooms {
		if room.ID == roomID {
			return room, nil
		}
	}
	return model.SignedRoom{}, sql.ErrNoRows
}

func (s *fakeSignedRoomStore) UpdateSignedRoomExpiry(_ context.Context, roomID string, ownerUserID string, entryCode string, expiresAt time.Time) (model.SignedRoom, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.updateConflicts > 0 {
		s.updateConflicts--
		return model.SignedRoom{}, duplicateEntryCodeError{}
	}

	room, ok := s.rooms[roomID]
	if !ok {
		for key, candidate := range s.rooms {
			if candidate.ID == roomID {
				room = candidate
				roomID = key
				ok = true
				break
			}
		}
		if !ok {
			return model.SignedRoom{}, sql.ErrNoRows
		}
	}
	if room.OwnerUserID != ownerUserID {
		return model.SignedRoom{}, sql.ErrNoRows
	}

	room.EntryCode = entryCode
	room.ExpiresAt = expiresAt
	room.UpdatedAt = time.Now().UTC()
	s.rooms[roomID] = room
	return room, nil
}

type duplicateEntryCodeError struct{}

func (duplicateEntryCodeError) Error() string    { return "duplicate entry code" }
func (duplicateEntryCodeError) SQLState() string { return "23505" }

func (s *fakeSignedRoomStore) ListOwnedSignedRooms(_ context.Context, ownerUserID string) ([]model.SignedRoom, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var out []model.SignedRoom
	for _, room := range s.rooms {
		if room.OwnerUserID == ownerUserID && room.ExpiresAt.After(time.Now().UTC()) {
			out = append(out, room)
		}
	}
	return out, nil
}

func (s *fakeSignedRoomStore) DeleteSignedRoomByID(_ context.Context, roomID string) error {
	s.mu.Lock()
	for key, room := range s.rooms {
		if key == roomID || room.ID == roomID {
			delete(s.rooms, key)
		}
	}
	s.mu.Unlock()
	return nil
}

func (s *fakeSignedRoomStore) ListExpiredSignedRoomIDs(_ context.Context, now time.Time) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.deleteExpiredCalls++

	var expired []string
	for id, room := range s.rooms {
		if !room.ExpiresAt.After(now) {
			if room.ID != "" {
				expired = append(expired, room.ID)
			} else {
				expired = append(expired, id)
			}
		}
	}
	return expired, nil
}

func (s *fakeSignedRoomStore) RecordRoomMembership(_ context.Context, userID string, roomID, role string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	for i, item := range s.history {
		if item.RoomID == roomID && item.Role == role {
			s.history[i].LastVisitedAt = now
			return nil
		}
	}
	s.history = append(s.history, model.RoomHistory{
		RoomID:        roomID,
		Role:          role,
		LastVisitedAt: now,
	})
	return nil
}

func (s *fakeSignedRoomStore) GetRoomMembership(_ context.Context, _ string, roomID string) (model.RoomHistory, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, item := range s.history {
		if item.RoomID == roomID {
			return item, nil
		}
	}
	return model.RoomHistory{}, sql.ErrNoRows
}

func (s *fakeSignedRoomStore) ListRoomMemberships(_ context.Context, _ string, limit int) ([]model.RoomHistory, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if limit > len(s.history) {
		limit = len(s.history)
	}
	out := make([]model.RoomHistory, limit)
	copy(out, s.history[:limit])
	return out, nil
}

func (s *fakeSignedRoomStore) PruneRoomMemberships(_ context.Context, _ string, limit int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if limit > 0 && len(s.history) > limit {
		s.history = s.history[:limit]
	}
	return nil
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
func (noopMessageStore) DeleteRoomMessages(context.Context, string) error { return nil }
func (noopMessageStore) Ping(context.Context) error                       { return nil }

func TestHandleCreateSignedRoom_DefaultTTL(t *testing.T) {
	store := newFakeSignedRoomStore()
	service := NewService(noopMessageStore{})
	service.BindSignedRoomStore(store)

	room, err := service.HandleCreateSignedRoom(context.Background(), "alpha", "user1", 0)
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
	if len(store.history) != 1 {
		t.Fatalf("expected exactly one history record, got %d", len(store.history))
	}
}

func TestHandleCreateSignedRoom_InvalidRoomName(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		service := NewService(noopMessageStore{})
		service.BindSignedRoomStore(newFakeSignedRoomStore())

		if _, err := service.HandleCreateSignedRoom(context.Background(), "", "user1", 0); !errors.Is(err, ErrInvalidRoomName) {
			t.Fatalf("expected ErrInvalidRoomName, got %v", err)
		}
	})

	t.Run("whitespace", func(t *testing.T) {
		service := NewService(noopMessageStore{})
		service.BindSignedRoomStore(newFakeSignedRoomStore())

		if _, err := service.HandleCreateSignedRoom(context.Background(), "   ", "user1", 0); !errors.Is(err, ErrInvalidRoomName) {
			t.Fatalf("expected ErrInvalidRoomName, got %v", err)
		}
	})
}

func TestHandleCreateSignedRoom_InvalidOwner(t *testing.T) {
	service := NewService(noopMessageStore{})
	service.BindSignedRoomStore(newFakeSignedRoomStore())

	if _, err := service.HandleCreateSignedRoom(context.Background(), "alpha", "", 0); !errors.Is(err, ErrInvalidRoomOwner) {
		t.Fatalf("expected ErrInvalidRoomOwner, got %v", err)
	}
}

func TestHandleCreateSignedRoom_InvalidTTL(t *testing.T) {
	service := NewService(noopMessageStore{})
	service.BindSignedRoomStore(newFakeSignedRoomStore())

	if _, err := service.HandleCreateSignedRoom(context.Background(), "alpha", "1", MaxSignedRoomTTL+time.Minute); !errors.Is(err, ErrSignedRoomTTLTooLarge) {
		t.Fatalf("expected ErrSignedRoomTTLTooLarge, got %v", err)
	}
}

func TestHandleCreateSignedRoom_StoreUnavailable(t *testing.T) {
	service := NewService(noopMessageStore{})

	if _, err := service.HandleCreateSignedRoom(context.Background(), "alpha", "1", 0); !errors.Is(err, ErrSignedRoomUnavailable) {
		t.Fatalf("expected ErrSignedRoomUnavailable, got %v", err)
	}
}

func TestHandleCreateSignedRoom_AllowsDuplicateRoomNames(t *testing.T) {
	store := newFakeSignedRoomStore()
	service := NewService(noopMessageStore{})
	service.BindSignedRoomStore(store)

	ctx := context.Background()

	initialRoom, err := service.HandleCreateSignedRoom(ctx, "alpha", "1", 5*time.Minute)
	if err != nil {
		t.Fatalf("initial create: %v", err)
	}

	secondRoom, err := service.HandleCreateSignedRoom(ctx, "alpha", "1", 10*time.Minute)
	if err != nil {
		t.Fatalf("second create with same name: %v", err)
	}

	if secondRoom.RoomName != initialRoom.RoomName {
		t.Fatalf("expected room name %q, got %q", initialRoom.RoomName, secondRoom.RoomName)
	}
	if secondRoom.ID == initialRoom.ID {
		t.Fatalf("expected unique IDs, got %q for both", initialRoom.ID)
	}
	if len(store.rooms) != 2 {
		t.Fatalf("expected two room records, got %d", len(store.rooms))
	}
}

func TestHandleCreateSignedRoom_RetriesEntryCodeCollision(t *testing.T) {
	store := newFakeSignedRoomStore()
	store.createConflicts = 2
	service := NewService(noopMessageStore{})
	service.BindSignedRoomStore(store)

	room, err := service.HandleCreateSignedRoom(context.Background(), "alpha", "1", 5*time.Minute)
	if err != nil {
		t.Fatalf("create signed room after entry code collisions: %v", err)
	}
	if room.ID == "" {
		t.Fatalf("expected room to be created")
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
			ID:          "room-1",
			RoomName:    "alpha",
			OwnerUserID: "1",
			EntryCode:   "1234",
			ExpiresAt:   time.Now().UTC().Add(5 * time.Minute),
		}

		service := NewService(noopMessageStore{})
		service.BindSignedRoomStore(store)

		room, err := service.HandleJoinSignedRoom(context.Background(), "room-1", "1234")
		if err != nil {
			t.Fatalf("join signed room: %v", err)
		}
		if room.ID != "room-1" {
			t.Fatalf("expected room room-1, got %s", room.ID)
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
			ID:          "room-exp",
			RoomName:    "expired",
			OwnerUserID: "1",
			EntryCode:   "1234",
			ExpiresAt:   time.Now().UTC().Add(-1 * time.Minute),
		}

		service := NewService(noopMessageStore{})
		service.BindSignedRoomStore(store)

		if _, err := service.HandleJoinSignedRoom(context.Background(), "room-exp", "1234"); !errors.Is(err, ErrSignedRoomExpired) {
			t.Fatalf("expected ErrSignedRoomExpired, got %v", err)
		}
	})

	t.Run("wrong code", func(t *testing.T) {
		store := newFakeSignedRoomStore()
		store.rooms["alpha"] = model.SignedRoom{
			ID:          "room-1",
			RoomName:    "alpha",
			OwnerUserID: "1",
			EntryCode:   "1234",
			ExpiresAt:   time.Now().UTC().Add(5 * time.Minute),
		}

		service := NewService(noopMessageStore{})
		service.BindSignedRoomStore(store)

		if _, err := service.HandleJoinSignedRoom(context.Background(), "room-1", "9999"); !errors.Is(err, ErrInvalidRoomEntryCode) {
			t.Fatalf("expected ErrInvalidRoomEntryCode, got %v", err)
		}
	})

	t.Run("malformed code", func(t *testing.T) {
		store := newFakeSignedRoomStore()
		store.rooms["alpha"] = model.SignedRoom{
			ID:          "room-1",
			RoomName:    "alpha",
			OwnerUserID: "1",
			EntryCode:   "1234",
			ExpiresAt:   time.Now().UTC().Add(5 * time.Minute),
		}

		service := NewService(noopMessageStore{})
		service.BindSignedRoomStore(store)

		cases := []string{
			"",
			"123",
			"12345",
			"12a4",
			"abcd",
		}

		for _, entryCode := range cases {
			if _, err := service.HandleJoinSignedRoom(context.Background(), "room-1", entryCode); !errors.Is(err, ErrInvalidRoomEntryCode) {
				t.Fatalf("expected ErrInvalidRoomEntryCode for %q, got %v", entryCode, err)
			}
		}
	})
}

func TestHandleListOwnedSignedRooms(t *testing.T) {
	t.Run("invalid owner", func(t *testing.T) {
		service := NewService(noopMessageStore{})
		service.BindSignedRoomStore(newFakeSignedRoomStore())

		if _, err := service.HandleListOwnedSignedRooms(context.Background(), ""); !errors.Is(err, ErrInvalidRoomOwner) {
			t.Fatalf("expected ErrInvalidRoomOwner, got %v", err)
		}
	})

	t.Run("filters owner and cleans expired", func(t *testing.T) {
		store := newFakeSignedRoomStore()
		store.rooms["active-owner-1"] = model.SignedRoom{
			ID:          "room-act-1",
			RoomName:    "active-owner-1",
			OwnerUserID: "1",
			EntryCode:   "1111",
			ExpiresAt:   time.Now().UTC().Add(10 * time.Minute),
		}
		store.rooms["expired-owner-1"] = model.SignedRoom{
			ID:          "room-exp-1",
			RoomName:    "expired-owner-1",
			OwnerUserID: "1",
			EntryCode:   "2222",
			ExpiresAt:   time.Now().UTC().Add(-1 * time.Minute),
		}
		store.rooms["active-owner-2"] = model.SignedRoom{
			ID:          "room-act-2",
			RoomName:    "active-owner-2",
			OwnerUserID: "2",
			EntryCode:   "3333",
			ExpiresAt:   time.Now().UTC().Add(10 * time.Minute),
		}

		service := NewService(noopMessageStore{})
		service.BindSignedRoomStore(store)
		service.signedRoomCleanupEvery = 0

		rooms, err := service.HandleListOwnedSignedRooms(context.Background(), "1")
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

func TestHandleRecordSignedRoomJoinAndListHistory(t *testing.T) {
	store := newFakeSignedRoomStore()
	store.history = append(store.history, model.RoomHistory{
		RoomID:        "owned-room",
		RoomName:      "owned-room",
		Role:          roomHistoryRoleOwner,
		LastVisitedAt: time.Now().UTC(),
		Active:        true,
	})

	service := NewService(noopMessageStore{})
	service.BindSignedRoomStore(store)

	if err := service.HandleRecordSignedRoomJoin(context.Background(), "joined-room", "1"); err != nil {
		t.Fatalf("record signed room join: %v", err)
	}

	history, err := service.HandleListRoomHistory(context.Background(), "1")
	if err != nil {
		t.Fatalf("list room history: %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("expected 2 history records, got %d", len(history))
	}
	if history[1].Role != roomHistoryRoleMember {
		t.Fatalf("expected joined history role, got %q", history[1].Role)
	}
}

func TestHandleExtendSignedRoom(t *testing.T) {
	t.Run("owner extends active room and keeps entry code", func(t *testing.T) {
		store := newFakeSignedRoomStore()
		originalExpiry := time.Now().UTC().Add(2 * time.Hour)
		store.rooms["alpha"] = model.SignedRoom{
			ID:          "room-1",
			RoomName:    "alpha",
			OwnerUserID: "1",
			EntryCode:   "1234",
			ExpiresAt:   originalExpiry,
		}

		service := NewService(noopMessageStore{})
		service.BindSignedRoomStore(store)

		room, err := service.HandleExtendSignedRoom(context.Background(), "room-1", "1", 30*time.Minute)
		if err != nil {
			t.Fatalf("extend signed room: %v", err)
		}
		if room.EntryCode != "1234" {
			t.Fatalf("expected entry code to stay the same, got %q", room.EntryCode)
		}
		if !room.ExpiresAt.After(originalExpiry) {
			t.Fatalf("expected expiry to move forward")
		}
	})

	t.Run("rejects other owner", func(t *testing.T) {
		store := newFakeSignedRoomStore()
		store.rooms["alpha"] = model.SignedRoom{
			ID:          "room-1",
			RoomName:    "alpha",
			OwnerUserID: "1",
			EntryCode:   "1234",
			ExpiresAt:   time.Now().UTC().Add(2 * time.Hour),
		}

		service := NewService(noopMessageStore{})
		service.BindSignedRoomStore(store)

		if _, err := service.HandleExtendSignedRoom(context.Background(), "room-1", "2", 30*time.Minute); !errors.Is(err, ErrRoomOwnedByAnotherUser) {
			t.Fatalf("expected ErrRoomOwnedByAnotherUser, got %v", err)
		}
	})

	t.Run("rejects expired room", func(t *testing.T) {
		store := newFakeSignedRoomStore()
		store.rooms["alpha"] = model.SignedRoom{
			ID:          "room-1",
			RoomName:    "alpha",
			OwnerUserID: "1",
			EntryCode:   "1234",
			ExpiresAt:   time.Now().UTC().Add(-1 * time.Minute),
		}

		service := NewService(noopMessageStore{})
		service.BindSignedRoomStore(store)

		if _, err := service.HandleExtendSignedRoom(context.Background(), "room-1", "1", 30*time.Minute); !errors.Is(err, ErrSignedRoomExpired) {
			t.Fatalf("expected ErrSignedRoomExpired, got %v", err)
		}
	})

	t.Run("rejects capacity above ten days", func(t *testing.T) {
		store := newFakeSignedRoomStore()
		store.rooms["alpha"] = model.SignedRoom{
			ID:          "room-1",
			RoomName:    "alpha",
			OwnerUserID: "1",
			EntryCode:   "1234",
			ExpiresAt:   time.Now().UTC().Add(9 * 24 * time.Hour),
		}

		service := NewService(noopMessageStore{})
		service.BindSignedRoomStore(store)

		if _, err := service.HandleExtendSignedRoom(context.Background(), "room-1", "1", 2*24*time.Hour); !errors.Is(err, ErrSignedRoomCapacityTooLarge) {
			t.Fatalf("expected ErrSignedRoomCapacityTooLarge, got %v", err)
		}
	})
}

func TestHandleReviveSignedRoom_RetriesEntryCodeCollision(t *testing.T) {
	store := newFakeSignedRoomStore()
	store.updateConflicts = 2
	store.rooms["expired"] = model.SignedRoom{
		ID:          "room-expired",
		RoomName:    "expired",
		OwnerUserID: "1",
		EntryCode:   "1234",
		ExpiresAt:   time.Now().UTC().Add(-time.Minute),
	}

	service := NewService(noopMessageStore{})
	service.BindSignedRoomStore(store)

	room, err := service.HandleReviveSignedRoom(context.Background(), "room-expired", "1", 5*time.Minute)
	if err != nil {
		t.Fatalf("revive signed room after entry code collisions: %v", err)
	}
	if room.ID != "room-expired" {
		t.Fatalf("expected revived room ID room-expired, got %q", room.ID)
	}
	if !room.ExpiresAt.After(time.Now().UTC()) {
		t.Fatalf("expected revived room to be active")
	}
}

func TestHandleDeleteSignedRoom(t *testing.T) {
	t.Run("owner deletes active room", func(t *testing.T) {
		store := newFakeSignedRoomStore()
		store.rooms["alpha"] = model.SignedRoom{
			ID:          "room-1",
			RoomName:    "alpha",
			OwnerUserID: "1",
			EntryCode:   "1234",
			ExpiresAt:   time.Now().UTC().Add(5 * time.Minute),
		}

		service := NewService(noopMessageStore{})
		service.BindSignedRoomStore(store)

		if err := service.HandleDeleteSignedRoom(context.Background(), "room-1", "1"); err != nil {
			t.Fatalf("delete signed room: %v", err)
		}
		if _, ok := store.rooms["alpha"]; ok {
			t.Fatalf("expected room to be deleted")
		}
	})

	t.Run("rejects other owner", func(t *testing.T) {
		store := newFakeSignedRoomStore()
		store.rooms["alpha"] = model.SignedRoom{
			ID:          "room-1",
			RoomName:    "alpha",
			OwnerUserID: "1",
			EntryCode:   "1234",
			ExpiresAt:   time.Now().UTC().Add(5 * time.Minute),
		}

		service := NewService(noopMessageStore{})
		service.BindSignedRoomStore(store)

		if err := service.HandleDeleteSignedRoom(context.Background(), "room-1", "2"); !errors.Is(err, ErrRoomOwnedByAnotherUser) {
			t.Fatalf("expected ErrRoomOwnedByAnotherUser, got %v", err)
		}
		if _, ok := store.rooms["alpha"]; !ok {
			t.Fatalf("expected room to remain after rejected delete")
		}
	})

	t.Run("owner deletes expired room", func(t *testing.T) {
		store := newFakeSignedRoomStore()
		store.rooms["expired"] = model.SignedRoom{
			ID:          "room-expired",
			RoomName:    "expired",
			OwnerUserID: "1",
			EntryCode:   "1234",
			ExpiresAt:   time.Now().UTC().Add(-time.Minute),
		}

		service := NewService(noopMessageStore{})
		service.BindSignedRoomStore(store)

		if err := service.HandleDeleteSignedRoom(context.Background(), "room-expired", "1"); err != nil {
			t.Fatalf("delete expired signed room: %v", err)
		}
		if _, err := store.GetSignedRoomByID(context.Background(), "room-expired"); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("expected expired room to be deleted, got %v", err)
		}
	})

	t.Run("missing room", func(t *testing.T) {
		service := NewService(noopMessageStore{})
		service.BindSignedRoomStore(newFakeSignedRoomStore())

		if err := service.HandleDeleteSignedRoom(context.Background(), "missing", "1"); !errors.Is(err, ErrSignedRoomNotFound) {
			t.Fatalf("expected ErrSignedRoomNotFound, got %v", err)
		}
	})

	t.Run("invalid input", func(t *testing.T) {
		service := NewService(noopMessageStore{})
		service.BindSignedRoomStore(newFakeSignedRoomStore())

		if err := service.HandleDeleteSignedRoom(context.Background(), " ", "1"); !errors.Is(err, ErrInvalidRoomName) {
			t.Fatalf("expected ErrInvalidRoomName, got %v", err)
		}
		if err := service.HandleDeleteSignedRoom(context.Background(), "alpha", ""); !errors.Is(err, ErrInvalidRoomOwner) {
			t.Fatalf("expected ErrInvalidRoomOwner, got %v", err)
		}
	})

	t.Run("store unavailable", func(t *testing.T) {
		service := NewService(noopMessageStore{})

		if err := service.HandleDeleteSignedRoom(context.Background(), "alpha", "1"); !errors.Is(err, ErrSignedRoomUnavailable) {
			t.Fatalf("expected ErrSignedRoomUnavailable, got %v", err)
		}
	})
}

func TestHandleGetSignedRoomStatus_ExpiredRoom(t *testing.T) {
	store := newFakeSignedRoomStore()
	store.rooms["expired"] = model.SignedRoom{
		ID:          "room-exp",
		RoomName:    "expired",
		OwnerUserID: "1",
		EntryCode:   "1234",
		ExpiresAt:   time.Now().UTC().Add(-1 * time.Minute),
	}

	service := NewService(noopMessageStore{})
	service.BindSignedRoomStore(store)

	_, exists, err := service.HandleGetSignedRoomStatus(context.Background(), "room-exp")
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

func (s *errorSignedRoomStore) CreateSignedRoom(context.Context, string, string, string, time.Time) (model.SignedRoom, error) {
	return model.SignedRoom{}, s.err
}

func (s *errorSignedRoomStore) GetSignedRoomByID(context.Context, string) (model.SignedRoom, error) {
	return model.SignedRoom{}, s.err
}

func (s *errorSignedRoomStore) UpdateSignedRoomExpiry(context.Context, string, string, string, time.Time) (model.SignedRoom, error) {
	return model.SignedRoom{}, s.err
}

func (s *errorSignedRoomStore) ListOwnedSignedRooms(context.Context, string) ([]model.SignedRoom, error) {
	return nil, s.err
}

func (s *errorSignedRoomStore) DeleteSignedRoomByID(context.Context, string) error {
	return s.err
}

func (s *errorSignedRoomStore) ListExpiredSignedRoomIDs(context.Context, time.Time) ([]string, error) {
	return nil, s.err
}

func (s *errorSignedRoomStore) RecordRoomMembership(context.Context, string, string, string) error {
	return s.err
}

func (s *errorSignedRoomStore) GetRoomMembership(context.Context, string, string) (model.RoomHistory, error) {
	return model.RoomHistory{}, s.err
}

func (s *errorSignedRoomStore) PruneRoomMemberships(context.Context, string, int) error {
	return s.err
}

func (s *errorSignedRoomStore) ListRoomMemberships(context.Context, string, int) ([]model.RoomHistory, error) {
	return nil, s.err
}
