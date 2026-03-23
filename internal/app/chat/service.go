package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/gorilla/websocket"
	"real_time_chat_app/internal/model"
)

const (
	SocketBufferSize  = 1024
	MessageBufferSize = 256
)

type MessageStore interface {
	SaveMessage(ctx context.Context, roomName, userName, message string) error
	GetRecentMessages(ctx context.Context, roomName string, limit int) ([]model.Message, error)
	Ping(ctx context.Context) error
}

type Service struct {
	rooms *Registry
	store MessageStore
}

func NewService(store MessageStore) *Service {
	return &Service{
		rooms: NewRegistry(),
		store: store,
	}
}

func (s *Service) newClient(socket *websocket.Conn, room *Room, userID string) *Client {
	return &Client{
		socket:   socket,
		room:     room,
		receive:  make(chan []byte, MessageBufferSize),
		name:     fmt.Sprintf("user-%s", userID),
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

func (s *Service) HandleRoom(ctx context.Context, socket *websocket.Conn, roomName, userID string) (*Room, *Client) {
	room := s.rooms.GetOrCreate(roomName)
	client := s.newClient(socket, room, userID)
	s.sendRecentMessages(ctx, client)
	return room, client
}

func (s *Service) HandleHealth(ctx context.Context) error {
	return s.store.Ping(ctx)
}
