package chat

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	writeWait      = 10 * time.Second
	pongWait       = 60 * time.Second
	pingPeriod     = (pongWait * 9) / 10
	maxMessageSize = 4 * 1024
)

// Client represents a single chatting user.
type Client struct {
	socket *websocket.Conn

	receive chan []byte

	room *Room

	name string

	messages MessageStore

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

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		err = c.messages.SaveMessage(ctx, c.room.name, c.name, cleanMessage)
		cancel()
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
				log.Printf("DB timeout while saving message for room=%s user=%s", c.room.name, c.name)
			} else {
				log.Printf("Failed to save message to DB: %v", err)
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

func (c *Client) Write() {
	defer c.closeSocket()

	ticker := time.NewTicker(pingPeriod)
	defer ticker.Stop()

	for {
		select {
		case msg, ok := <-c.receive:
			_ = c.socket.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				return
			}
			if err := c.socket.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.socket.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.socket.WriteMessage(websocket.PingMessage, nil); err != nil {
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
