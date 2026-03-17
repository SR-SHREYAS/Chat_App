package main

import (
	"encoding/json"
	"fmt"
	"log"
	"sync"
)

type room struct {
	name string

	// hold all current clients in room as a map
	clients map[*client]bool

	// join channel for all clients wishing to join
	join  chan *client
	leave chan *client

	// broadcast channel for sending messages to all clients
	forward chan []byte
}

func newRoom(name string) *room {
	return &room{
		name:    name,
		forward: make(chan []byte, messageBufferSize),
		join:    make(chan *client),
		leave:   make(chan *client),
		clients: make(map[*client]bool),
	}
}

// each room is a separate thread that should be run independently of the main thread
func (r *room) run() {
	for {
		select {
		// adding a user to the room/channel
		case client := <-r.join:
			// Add the client to the room first.
			r.clients[client] = true
			// Then, broadcast the join message to everyone, including the new client.
			sysMsg := map[string]string{"name": "System", "message": fmt.Sprintf("%s joined the room", client.name)}
			if msg, err := json.Marshal(sysMsg); err == nil {
				r.broadcast(msg)
			}
		//removing a user from the room/channel
		case client := <-r.leave:
			removed := r.removeClient(client)

			if r.closeIfEmpty() {
				return
			}
			if !removed {
				continue
			}

			// Broadcast system message: User left
			sysMsg := map[string]string{"name": "System", "message": fmt.Sprintf("%s left the room", client.name)}
			if msg, err := json.Marshal(sysMsg); err == nil {
				r.broadcast(msg)
			}
		// forward message to all clients
		case msg := <-r.forward:
			r.broadcast(msg)
			if r.closeIfEmpty() {
				return
			}
		}
	}
}

func (r *room) removeClient(c *client) bool {
	if _, ok := r.clients[c]; !ok {
		return false
	}

	delete(r.clients, c)
	close(c.receive)
	c.closeSocket()
	return true
}

func (r *room) broadcast(msg []byte) {
	var slowClients []*client

	for c := range r.clients {
		select {
		case c.receive <- msg:
		default:
			slowClients = append(slowClients, c)
		}
	}

	for _, c := range slowClients {
		log.Printf("Dropping slow client %s from room %s", c.name, r.name)
		r.removeClient(c)
	}
}

func (r *room) closeIfEmpty() bool {
	if len(r.clients) != 0 {
		return false
	}

	mu.Lock()
	delete(rooms, r.name)
	mu.Unlock()
	log.Printf("Room closed and cleaned up: %s", r.name)
	return true
}

var rooms = make(map[string]*room)
var mu sync.Mutex

func getRoom(name string) *room {

	// prevent creating a room with same name when multiple users do that st the same time
	mu.Lock()
	defer mu.Unlock()

	// if the room name already exists
	if room, ok := rooms[name]; ok {
		return room
	}
	// else create a new room
	room := newRoom(name)
	rooms[name] = room

	go room.run()
	log.Printf("New room created: %s", name)
	return room
}
