package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

const (
	writeWait      = 10 * time.Second
	pongWait       = 60 * time.Second
	pingPeriod     = (pongWait * 9) / 10
	maxMessageSize = 4 * 1024
)

// client represents a single chatting user
type client struct {
	// a socket connection for this user
	socket *websocket.Conn

	// receive is a channel to receive messages from other clients
	receive chan []byte

	room *room

	name string

	db *sql.DB
}

// send message function
func (c *client) read() {
	defer c.socket.Close()

	c.socket.SetReadLimit(maxMessageSize)
	_ = c.socket.SetReadDeadline(time.Now().Add(pongWait))
	c.socket.SetPongHandler(func(string) error {
		return c.socket.SetReadDeadline(time.Now().Add(pongWait))
	})

	// infinite loop, keep reading
	for {
		_, msg, err := c.socket.ReadMessage()
		if err != nil {
			return
		}

		cleanMessage := strings.TrimSpace(string(msg))
		if cleanMessage == "" {
			continue
		}

		// Save to database before forwarding
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_, err = c.db.ExecContext(ctx, `
	            INSERT INTO messages (room_name, user_name, message)
	            VALUES ($1, $2, $3)
	        `, c.room.name, c.name, cleanMessage)
		cancel()
		if err != nil {
			log.Printf("Failed to save message to DB: %v", err)
		}

		// incoming message from the client into json
		outgoing := map[string]string{
			"name":    c.name,
			"message": cleanMessage,
		}

		jsMessage, err := json.Marshal(outgoing)
		if err != nil {
			log.Println("Encoding failed:", err)
			continue
		}

		// forward message to the room
		c.room.forward <- jsMessage
	}
}

func (c *client) write() {
	defer c.socket.Close()

	ticker := time.NewTicker(pingPeriod)
	defer ticker.Stop()

	for {
		select {
		case msg, ok := <-c.receive:
			_ = c.socket.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				_ = c.socket.WriteMessage(websocket.CloseMessage, []byte{})
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
