package main

import (
	"log"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"text/template"
	"time"

	"github.com/joho/godotenv"
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

	// Load .env file, but don't fail if it's not present (for deployment)
	err := godotenv.Load()
	if err != nil {
		log.Println("No .env file found, using environment variables from system")
	}

	// make every randomly generated number unique
	rand.Seed(time.Now().UnixNano())

	// var addr = flag.String("addr", ":8080", "The addr of the application")
	// flag.Parse()
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	addr := ":" + port

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

	// Health check endpoint
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	//start the web server

	log.Println("starting web server on", addr)

	if err := http.ListenAndServe(addr, CORSMiddleware(http.DefaultServeMux)); err != nil {
		log.Fatal("ListenAndServe:", err)
	}

}

// CORSMiddleware adds the necessary headers to handle Cross-Origin Resource Sharing.
// This is useful if you ever decide to host your frontend on a different domain.
func CORSMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Set headers to allow cross-origin requests
		// Note: Using "*" for Access-Control-Allow-Origin is permissive.
		// For production, you should restrict this to your frontend's domain.
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		// If this is a preflight request (OPTIONS), we can just send an OK status.
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		// Otherwise, serve the request to the next handler.
		next.ServeHTTP(w, r)
	})
}
