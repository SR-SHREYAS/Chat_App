package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gorilla/websocket"
)

const (
	writeWait      = 10 * time.Second
	pongWait       = 60 * time.Second
	pingPeriod     = (pongWait * 9) / 10
	maxMessageSize = 8 * 1024
	MaxMessageLen  = 2000
)

// Client represents a single chatting user.
type Client struct {
	socket *websocket.Conn

	receive chan []byte

	room *Room

	userID string
	name   string

	messages MessageStore
	persist  bool

	writeMu   sync.Mutex
	closeOnce sync.Once
}

func (c *Client) Read() {
	defer c.closeSocket()

	c.socket.SetReadLimit(maxMessageSize)
	_ = c.socket.SetReadDeadline(time.Now().Add(pongWait))
	c.socket.SetPongHandler(func(string) error {
		return c.socket.SetReadDeadline(time.Now().Add(pongWait))
	})

	for {
		_, msg, err := c.socket.ReadMessage()
		if err != nil {
			return
		}

		cleanMessage := strings.TrimSpace(string(msg))
		if cleanMessage == "" {
			continue
		}
		if utf8.RuneCountInString(cleanMessage) > MaxMessageLen {
			log.Printf("Dropping oversized chat message for room=%s user=%s", c.room.name, c.userID)
			c.sendErrorMessage(
				"message_too_long",
				fmt.Sprintf("Message too long. Maximum allowed length is %d characters.", MaxMessageLen),
			)
			continue
		}

		if c.persist {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			err = c.messages.SaveMessage(ctx, c.room.name, c.userID, cleanMessage)
			cancel()
			if err != nil {
				if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
					log.Printf("DB timeout while saving message for room=%s user=%s", c.room.name, c.userID)
				} else {
					log.Printf("Failed to save message to DB: %v", err)
				}
			}
		}

		outgoing := map[string]string{
			"name":    c.name,
			"message": cleanMessage,
		}

		jsMessage, err := json.Marshal(outgoing)
		if err != nil {
			log.Println("Encoding failed:", err)
			continue
		}

		c.room.forward <- jsMessage
	}
}

func (c *Client) writeMessage(mt int, data []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	if err := c.socket.SetWriteDeadline(time.Now().Add(writeWait)); err != nil {
		log.Printf("failed to set write deadline for room=%s user=%s: %v", c.room.name, c.userID, err)
	}

	return c.socket.WriteMessage(mt, data)
}

func (c *Client) sendErrorMessage(code, message string) {
	payload := map[string]string{
		"type":    "error",
		"code":    code,
		"message": message,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		log.Printf("failed to marshal error message for room=%s user=%s: %v", c.room.name, c.userID, err)
		return
	}

	if err := c.writeMessage(websocket.TextMessage, data); err != nil {
		log.Printf("failed to send error message for room=%s user=%s: %v", c.room.name, c.userID, err)
	}
}

func (c *Client) Write() {
	defer c.closeSocket()

	ticker := time.NewTicker(pingPeriod)
	defer ticker.Stop()

	for {
		select {
		case msg, ok := <-c.receive:
			if !ok {
				return
			}
			if err := c.writeMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			if err := c.writeMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func (c *Client) closeSocket() {
	c.closeOnce.Do(func() {
		_ = c.socket.Close()
	})
}
