package main

import (
	"database/sql"
	"encoding/json"
	"log"

	"github.com/gorilla/websocket"
)

// client represents a single connected chat user.
type client struct {
	socket  *websocket.Conn
	receive chan []byte
	room    *room
	name    string
	db      *sql.DB
}

// read listens for incoming messages from the user's browser,
// persists them to the database, and forwards them to the room.
func (c *client) read() {
	defer c.socket.Close()

	for {
		_, msg, err := c.socket.ReadMessage()
		if err != nil {
			return
		}

		// Persist message
		_, dbErr := c.db.Exec(
			`INSERT INTO messages (room_name, user_name, message) VALUES ($1, $2, $3)`,
			c.room.name, c.name, string(msg),
		)
		if dbErr != nil {
			log.Printf("Failed to save message to DB: %v", dbErr)
		}

		// Wrap in JSON with username
		outgoing, err := json.Marshal(map[string]string{
			"name":    c.name,
			"message": string(msg),
		})
		if err != nil {
			log.Printf("JSON encoding failed: %v", err)
			continue
		}

		c.room.forward <- outgoing
	}
}

// write sends messages from the room to the user's browser.
func (c *client) write() {
	defer c.socket.Close()

	for msg := range c.receive {
		if err := c.socket.WriteMessage(websocket.TextMessage, msg); err != nil {
			return
		}
	}
}
