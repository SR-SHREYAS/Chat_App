package chat

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"real_time_chat_app/internal/model"
)

const (
	SocketBufferSize     = 1024
	MessageBufferSize    = 256
	DefaultSignedRoomTTL = 10 * time.Minute
	MaxSignedRoomTTL     = 7 * 24 * time.Hour
)

var (
	ErrSignedRoomUnavailable  = errors.New("signed room service unavailable")
	ErrInvalidRoomName        = errors.New("invalid room name")
	ErrInvalidRoomOwner       = errors.New("invalid room owner")
	ErrInvalidRoomTTL         = errors.New("invalid room ttl")
	ErrSignedRoomNotFound     = errors.New("signed room not found")
	ErrSignedRoomExpired      = errors.New("signed room expired")
	ErrRoomOwnedByAnotherUser = errors.New("room is owned by another user")
)

type MessageStore interface {
	SaveMessage(ctx context.Context, roomName, userName, message string) error
	GetRecentMessages(ctx context.Context, roomName string, limit int) ([]model.Message, error)
	Ping(ctx context.Context) error
}

type SignedRoomStore interface {
	CreateSignedRoom(ctx context.Context, roomName string, ownerUserID int64, ownerDisplayName string, expiresAt time.Time) (model.SignedRoom, error)
	GetSignedRoomByName(ctx context.Context, roomName string) (model.SignedRoom, error)
	UpdateSignedRoomExpiry(ctx context.Context, roomName string, ownerUserID int64, ownerDisplayName string, expiresAt time.Time) (model.SignedRoom, error)
	ListOwnedSignedRooms(ctx context.Context, ownerUserID int64) ([]model.SignedRoom, error)
	DeleteSignedRoomByName(ctx context.Context, roomName string) error
	DeleteExpiredSignedRooms(ctx context.Context, now time.Time) (int64, error)
}

type Service struct {
	rooms     *Registry
	store     MessageStore
	roomStore SignedRoomStore
}

func NewService(store MessageStore) *Service {
	return &Service{
		rooms: NewRegistry(),
		store: store,
	}
}

func (s *Service) BindSignedRoomStore(store SignedRoomStore) {
	s.roomStore = store
}

func (s *Service) newClient(socket *websocket.Conn, room *Room, userID, userName string) *Client {
	name := strings.TrimSpace(userName)
	if name == "" {
		name = fmt.Sprintf("user-%s", userID)
	}

	return &Client{
		socket:   socket,
		room:     room,
		receive:  make(chan []byte, MessageBufferSize),
		name:     name,
		messages: s.store,
	}
}

func (s *Service) sendRecentMessages(ctx context.Context, c *Client) {
	queryCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	messages, err := s.store.GetRecentMessages(queryCtx, c.room.name, 50)
	if err != nil {
		log.Printf("Could not query recent messages for room %s: %v", c.room.name, err)
		return
	}

	count := 0
	for _, m := range messages {
		msgJSON, err := json.Marshal(map[string]string{
			"name":    m.UserName,
			"message": m.Message,
		})
		if err != nil {
			log.Printf("Error marshaling message for user %s: %v", m.UserName, err)
			continue
		}
		c.receive <- msgJSON
		count++
	}

	log.Printf("Sent %d recent messages to %s in room %s", count, c.name, c.room.name)
}

func (s *Service) HandleRoom(ctx context.Context, socket *websocket.Conn, roomName, userID, userName string) (*Room, *Client) {
	room := s.rooms.GetOrCreate(roomName)
	client := s.newClient(socket, room, userID, userName)
	s.sendRecentMessages(ctx, client)
	return room, client
}

func (s *Service) HandleHealth(ctx context.Context) error {
	return s.store.Ping(ctx)
}

func (s *Service) HandleCreateSignedRoom(ctx context.Context, roomName string, ownerUserID int64, ownerDisplayName string, ttl time.Duration) (model.SignedRoom, error) {
	if s.roomStore == nil {
		return model.SignedRoom{}, ErrSignedRoomUnavailable
	}

	roomName = strings.TrimSpace(roomName)
	if roomName == "" {
		return model.SignedRoom{}, ErrInvalidRoomName
	}
	if ownerUserID <= 0 {
		return model.SignedRoom{}, ErrInvalidRoomOwner
	}

	ttl = normalizeSignedRoomTTL(ttl)
	if ttl <= 0 {
		return model.SignedRoom{}, ErrInvalidRoomTTL
	}

	now := time.Now().UTC()
	if _, err := s.roomStore.DeleteExpiredSignedRooms(ctx, now); err != nil {
		return model.SignedRoom{}, err
	}

	expiresAt := now.Add(ttl)
	existing, err := s.roomStore.GetSignedRoomByName(ctx, roomName)
	if err == nil {
		if existing.OwnerUserID != ownerUserID {
			return model.SignedRoom{}, ErrRoomOwnedByAnotherUser
		}
		return s.roomStore.UpdateSignedRoomExpiry(ctx, roomName, ownerUserID, ownerDisplayName, expiresAt)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return model.SignedRoom{}, err
	}

	return s.roomStore.CreateSignedRoom(ctx, roomName, ownerUserID, ownerDisplayName, expiresAt)
}

func (s *Service) HandleJoinSignedRoom(ctx context.Context, roomName string) (model.SignedRoom, error) {
	room, found, err := s.HandleGetSignedRoomStatus(ctx, roomName)
	if err != nil {
		return model.SignedRoom{}, err
	}
	if !found {
		return model.SignedRoom{}, ErrSignedRoomNotFound
	}
	return room, nil
}

func (s *Service) HandleListOwnedSignedRooms(ctx context.Context, ownerUserID int64) ([]model.SignedRoom, error) {
	if s.roomStore == nil {
		return nil, ErrSignedRoomUnavailable
	}
	if ownerUserID <= 0 {
		return nil, ErrInvalidRoomOwner
	}

	if _, err := s.roomStore.DeleteExpiredSignedRooms(ctx, time.Now().UTC()); err != nil {
		return nil, err
	}
	return s.roomStore.ListOwnedSignedRooms(ctx, ownerUserID)
}

func (s *Service) HandleGetSignedRoomStatus(ctx context.Context, roomName string) (model.SignedRoom, bool, error) {
	if s.roomStore == nil {
		return model.SignedRoom{}, false, ErrSignedRoomUnavailable
	}

	roomName = strings.TrimSpace(roomName)
	if roomName == "" {
		return model.SignedRoom{}, false, ErrInvalidRoomName
	}

	room, err := s.roomStore.GetSignedRoomByName(ctx, roomName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.SignedRoom{}, false, nil
		}
		return model.SignedRoom{}, false, err
	}

	now := time.Now().UTC()
	if !room.ExpiresAt.After(now) {
		_ = s.roomStore.DeleteSignedRoomByName(ctx, roomName)
		return model.SignedRoom{}, false, ErrSignedRoomExpired
	}

	return room, true, nil
}

func normalizeSignedRoomTTL(ttl time.Duration) time.Duration {
	if ttl <= 0 {
		return DefaultSignedRoomTTL
	}
	if ttl > MaxSignedRoomTTL {
		return 0
	}
	return ttl
}
