package main

import (
	"flag"
	"log"
	"math/rand"
	"net/http"
	"path/filepath"
	"sync"
	"text/template"
	"time"
)

type templateHandler struct {
	once     sync.Once
	filename string
	templ    *template.Template
}

// handling template for our server

func (t *templateHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	t.once.Do(func() {
		t.templ = template.Must(template.ParseFiles(filepath.Join("templates", t.filename)))
	})
	t.templ.Execute(w, r)
}

func main() {

	// make every randomly generated number unique
	rand.Seed(time.Now().UnixNano())

	var addr = flag.String("addr", ":8080", "The addr of the application")
	flag.Parse()

	http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))
	http.Handle("/", &templateHandler{filename: "index.html"})
	http.Handle("/chat", &templateHandler{filename: "chat.html"})

	http.HandleFunc("/room", func(w http.ResponseWriter, r *http.Request) {
		roomName := r.URL.Query().Get("room")
		if roomName == "" {
			http.Error(w, "Missing room parameter", http.StatusBadRequest)
			return
		}
		realRoom := getRoom(roomName) // Get the room instance
		realRoom.ServeHTTP(w, r)      // Call the ServeHTTP method on the room instance
	})
	// This will be the websocket endpoint

	// run the room in a separate goroutine

	//start the web server

	log.Println("starting web server on", *addr)

	if err := http.ListenAndServe(*addr, nil); err != nil {
		log.Fatal("ListenAndServe:", err)
	}

}
