package main

import (
	"encoding/json"
	"fmt"

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

		// incoming message from the client into json
		outgoing := map[string]string{
			"name":    c.name,
			"message": string(msg),
		}

		jsMessage, err := json.Marshal(outgoing)
		if err != nil {
			fmt.Println("Enconding failed!")
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
