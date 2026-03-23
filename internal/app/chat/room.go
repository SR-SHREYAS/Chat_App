package chat

import (
	"encoding/json"
	"fmt"
	"log"
)

type Room struct {
	name string

	clients map[*Client]bool

	join  chan *Client
	leave chan *Client

	forward chan []byte

	registry *Registry
}

func newRoom(name string, registry *Registry) *Room {
	return &Room{
		name:     name,
		forward:  make(chan []byte, MessageBufferSize),
		join:     make(chan *Client),
		leave:    make(chan *Client),
		clients:  make(map[*Client]bool),
		registry: registry,
	}
}

func (r *Room) Join(c *Client) {
	r.join <- c
}

func (r *Room) Leave(c *Client) {
	r.leave <- c
}

func (r *Room) Name() string {
	return r.name
}

func (r *Room) run() {
	for {
		select {
		case client := <-r.join:
			r.clients[client] = true
			sysMsg := map[string]string{"name": "System", "message": fmt.Sprintf("%s joined the room", client.name)}
			if msg, err := json.Marshal(sysMsg); err == nil {
				r.broadcast(msg)
			}
		case client := <-r.leave:
			removed := r.removeClient(client)

			if r.closeIfEmpty() {
				return
			}
			if !removed {
				continue
			}

			sysMsg := map[string]string{"name": "System", "message": fmt.Sprintf("%s left the room", client.name)}
			if msg, err := json.Marshal(sysMsg); err == nil {
				r.broadcast(msg)
			}
		case msg := <-r.forward:
			r.broadcast(msg)
			if r.closeIfEmpty() {
				return
			}
		}
	}
}

func (r *Room) removeClient(c *Client) bool {
	if _, ok := r.clients[c]; !ok {
		return false
	}

	delete(r.clients, c)
	close(c.receive)
	c.closeSocket()
	return true
}

func (r *Room) broadcast(msg []byte) {
	var slowClients []*Client

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

func (r *Room) closeIfEmpty() bool {
	if len(r.clients) != 0 {
		return false
	}

	r.registry.Remove(r.name)
	log.Printf("Room closed and cleaned up: %s", r.name)
	return true
}
