package main

import (
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
)

type room struct {

	// hold all current clients in room as a map
	clients map[*client]bool

	// join channel for all clients wishing to join
	join  chan *client
	leave chan *client

	// broadcast channel for sending messages to all clients
	forward chan []byte
}

func newRoom() *room {
	return &room{
		forward: make(chan []byte),
		join:    make(chan *client),
		leave:   make(chan *client),
		clients: make(map[*client]bool),
	}
}

// each room is a separete thread that should be run independently of the main thread
func (r *room) run() {
	for {
		select {
		// adding a user to the room/channel
		case client := <-r.join:
			r.clients[client] = true
		//removing a user from the room/channel
		case client := <-r.leave:
			delete(r.clients, client)
			close(client.receive)
		// forward message to all clients
		case msg := <-r.forward:
			for client := range r.clients {
				client.receive <- msg
			}
		}
	}
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
	room := newRoom()
	rooms[name] = room

	go room.run()
	return room
}

// upgrade a basic http connection to websocket connection
const (
	socketBufferSize  = 1024
	messageBufferSize = 256
)

var upgrader = &websocket.Upgrader{ReadBufferSize: socketBufferSize, WriteBufferSize: socketBufferSize}

func (r *room) ServeHTTP(w http.ResponseWriter, req *http.Request) {

	roomName := req.URL.Query().Get("room")
	if roomName == "" {
		http.Error(w, "Missing room parameter", http.StatusBadRequest)
		return
	}

	realRoom := getRoom(roomName)

	socket, err := upgrader.Upgrade(w, req, nil)
	if err != nil {
		log.Println("Upgrade error:", err)
		return
	}
	client := &client{
		socket:  socket,
		room:    realRoom,
		receive: make(chan []byte, messageBufferSize),
		name:    fmt.Sprintf("user%d", rand.Intn(1000)),
	}
	realRoom.join <- client

	defer func() {
		realRoom.leave <- client
	}()
	go client.write()
	client.read()
}
