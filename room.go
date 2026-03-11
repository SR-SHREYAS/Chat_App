package main

import (
	"encoding/json"
	"fmt"
	"log"
	"sync"
)

// room represents a single chat room with connected clients.
type room struct {
	name    string
	clients map[*client]bool
	join    chan *client
	leave   chan *client
	forward chan []byte
}

func newRoom(name string) *room {
	return &room{
		name:    name,
		clients: make(map[*client]bool),
		join:    make(chan *client),
		leave:   make(chan *client),
		forward: make(chan []byte),
	}
}

// run is the main event loop for a room, running in its own goroutine.
func (r *room) run() {
	for {
		select {
		// adding a user to the room/channel
		case client := <-r.join:
			r.clients[client] = true
			r.broadcastSystem(fmt.Sprintf("%s joined the room", client.name))

		//removing a user from the room/channel
		case client := <-r.leave:
			delete(r.clients, client)
			close(client.receive)

			if len(r.clients) == 0 {
				mu.Lock()
				delete(rooms, r.name)
				mu.Unlock()
				log.Printf("Room closed (empty): %s", r.name)
				return
			}
			r.broadcastSystem(fmt.Sprintf("%s left the room", client.name))

		// forward message to all clients
		case msg := <-r.forward:
			for client := range r.clients {
				client.receive <- msg
			}
		}
	}
}

// broadcastSystem sends a system message to all connected clients.
func (r *room) broadcastSystem(text string) {
	sysMsg, err := json.Marshal(map[string]string{
		"name":    "System",
		"message": text,
	})
	if err != nil {
		log.Printf("Error marshaling system message: %v", err)
		return
	}
	for c := range r.clients {
		c.receive <- sysMsg
	}
}

// ---------- Global Room Registry ----------

var (
	rooms = make(map[string]*room)
	mu    sync.Mutex
)

// getRoom returns an existing in-memory room or creates a new one.
func getRoom(name string) *room {
	mu.Lock()
	defer mu.Unlock()

	if r, ok := rooms[name]; ok {
		return r
	}

	r := newRoom(name)
	rooms[name] = r
	go r.run()
	log.Printf("In-memory room created: %s", name)
	return r
}
