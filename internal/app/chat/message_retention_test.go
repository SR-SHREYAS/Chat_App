package chat

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"real_time_chat_app/internal/model"
)

type messageRetentionStore struct {
	mu             sync.Mutex
	recent         []model.Message
	recentCalls    int
	deletedRooms   []string
	savedMessages  []model.Message
	savedRoomNames []string
}

func (s *messageRetentionStore) SaveMessage(_ context.Context, roomName, userName, message string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.savedRoomNames = append(s.savedRoomNames, roomName)
	s.savedMessages = append(s.savedMessages, model.Message{
		Username: userName,
		Message:  message,
	})
	return nil
}

func (s *messageRetentionStore) GetRecentMessages(_ context.Context, _ string, _ int) ([]model.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.recentCalls++
	out := make([]model.Message, len(s.recent))
	copy(out, s.recent)
	return out, nil
}

func (s *messageRetentionStore) DeleteRoomMessages(_ context.Context, roomName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.deletedRooms = append(s.deletedRooms, roomName)
	return nil
}

func (s *messageRetentionStore) Ping(context.Context) error { return nil }

func (s *messageRetentionStore) RecentCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.recentCalls
}

func (s *messageRetentionStore) DeletedRooms() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]string, len(s.deletedRooms))
	copy(out, s.deletedRooms)
	return out
}

func TestHandleRoom_ReplaysHistoryOnlyWhenPersistenceEnabled(t *testing.T) {
	store := &messageRetentionStore{
		recent: []model.Message{
			{Username: "owner", Message: "hello signed room"},
		},
	}
	service := NewService(store, nil)

	_, signedClient := service.HandleRoom(context.Background(), nil, "alpha", "u1", "owner", true)

	var payload map[string]string
	select {
	case raw := <-signedClient.receive:
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatalf("unmarshal replayed payload: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatalf("expected persisted signed-room history to be replayed")
	}

	if payload["message"] != "hello signed room" {
		t.Fatalf("expected signed-room history message, got %+v", payload)
	}
	if store.RecentCalls() != 1 {
		t.Fatalf("expected one recent-message query, got %d", store.RecentCalls())
	}

	_, guestClient := service.HandleRoom(context.Background(), nil, "beta", "u2", "", false)
	select {
	case raw := <-guestClient.receive:
		t.Fatalf("expected unsigned room to skip replay, got %s", string(raw))
	default:
	}
	if store.RecentCalls() != 1 {
		t.Fatalf("expected unsigned room to avoid recent-message query, got %d calls", store.RecentCalls())
	}
}

func TestHandleDeleteSignedRoom_ReliesOnCascadeForStoredMessages(t *testing.T) {
	messageStore := &messageRetentionStore{}
	roomStore := newFakeSignedRoomStore()
	roomStore.rooms["room-alpha"] = model.SignedRoom{
		ID:          "room-alpha",
		RoomName:    "alpha",
		OwnerUserID: "1",
		EntryCode:   "1234",
		ExpiresAt:   time.Now().UTC().Add(5 * time.Minute),
	}

	service := NewService(messageStore, roomStore)

	if err := service.HandleDeleteSignedRoom(context.Background(), "room-alpha", "1"); err != nil {
		t.Fatalf("delete signed room: %v", err)
	}

	deleted := messageStore.DeletedRooms()
	if len(deleted) != 0 {
		t.Fatalf("expected database cascade to delete stored messages, got manual deletes %+v", deleted)
	}
}

func TestHandleGetSignedRoomStatus_DeletesMessagesForExpiredRoom(t *testing.T) {
	messageStore := &messageRetentionStore{}
	roomStore := newFakeSignedRoomStore()
	roomStore.rooms["room-expired"] = model.SignedRoom{
		ID:          "room-expired",
		RoomName:    "expired",
		OwnerUserID: "1",
		EntryCode:   "1234",
		ExpiresAt:   time.Now().UTC().Add(-time.Minute),
	}

	service := NewService(messageStore, roomStore)

	_, exists, err := service.HandleGetSignedRoomStatus(context.Background(), "room-expired")
	if err == nil {
		t.Fatalf("expected expired room error")
	}
	if exists {
		t.Fatalf("expected expired room to no longer exist")
	}

	deleted := messageStore.DeletedRooms()
	if len(deleted) != 1 || deleted[0] != "room-expired" {
		t.Fatalf("expected stored messages for expired room ID to be deleted, got %+v", deleted)
	}
}

func TestExpiredRoomCleanupWithoutIntervalDeletesMessages(t *testing.T) {
	messageStore := &messageRetentionStore{}
	roomStore := newFakeSignedRoomStore()
	roomStore.rooms["room-expired"] = model.SignedRoom{
		ID:          "room-expired",
		RoomName:    "expired",
		OwnerUserID: "1",
		EntryCode:   "1234",
		ExpiresAt:   time.Now().UTC().Add(-time.Minute),
	}

	service := NewService(messageStore, roomStore)
	service.signedRoomCleanupEvery = 0

	if _, err := service.HandleListOwnedSignedRooms(context.Background(), "1"); err != nil {
		t.Fatalf("list owned signed rooms: %v", err)
	}

	deleted := messageStore.DeletedRooms()
	if len(deleted) != 1 || deleted[0] != "room-expired" {
		t.Fatalf("expected unthrottled cleanup to delete expired room messages, got %+v", deleted)
	}
}
