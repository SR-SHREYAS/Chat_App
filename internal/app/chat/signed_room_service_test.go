package chat

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"

	"real_time_chat_app/internal/model"
)

type fakeSignedRoomStore struct {
	mu    sync.Mutex
	rooms map[string]model.SignedRoom
}

func newFakeSignedRoomStore() *fakeSignedRoomStore {
	return &fakeSignedRoomStore{rooms: make(map[string]model.SignedRoom)}
}

func (s *fakeSignedRoomStore) CreateSignedRoom(_ context.Context, roomName string, ownerUserID int64, ownerDisplayName string, expiresAt time.Time) (model.SignedRoom, error) {
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

func (s *fakeSignedRoomStore) UpdateSignedRoomExpiry(_ context.Context, roomName string, ownerUserID int64, ownerDisplayName string, expiresAt time.Time) (model.SignedRoom, error) {
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

	var removed int64
	for name, room := range s.rooms {
		if !room.ExpiresAt.After(now) {
			delete(s.rooms, name)
			removed++
		}
	}
	return removed, nil
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

func TestHandleGetSignedRoomStatus_ExpiredRoom(t *testing.T) {
	store := newFakeSignedRoomStore()
	store.rooms["expired"] = model.SignedRoom{
		RoomName:         "expired",
		OwnerUserID:      1,
		OwnerDisplayName: "owner",
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
