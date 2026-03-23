package chat

import (
	"log"
	"sync"
)

type Registry struct {
	mu    sync.Mutex
	rooms map[string]*Room
}

func NewRegistry() *Registry {
	return &Registry{rooms: make(map[string]*Room)}
}

func (r *Registry) GetOrCreate(name string) *Room {
	r.mu.Lock()
	defer r.mu.Unlock()

	if room, ok := r.rooms[name]; ok {
		return room
	}

	room := newRoom(name, r)
	r.rooms[name] = room
	go room.run()
	log.Printf("New room created: %s", name)
	return room
}

func (r *Registry) Remove(name string) {
	r.mu.Lock()
	delete(r.rooms, name)
	r.mu.Unlock()
}
