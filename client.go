package main

import (
	"database/sql"
	"encoding/json"
	"log"

	"github.com/gorilla/websocket"
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

	// infinite loop , keep reading
	for {
		_, msg, err := c.socket.ReadMessage()
		if err != nil {
			return
		}

		// Save to database before forwarding
		_, err = c.db.Exec(`
            INSERT INTO messages (room_name, user_name, message)
            VALUES ($1, $2, $3)
        `, c.room.name, c.name, string(msg))
		if err != nil {
			log.Printf("Failed to save message to DB: %v", err)
		}

		// incoming message from the client into json
		outgoing := map[string]string{
			"name":    c.name,
			"message": string(msg),
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
	for msg := range c.receive {
		err := c.socket.WriteMessage(websocket.TextMessage, msg)
		if err != nil {
			return
		}
	}
}
