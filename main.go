package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/gorilla/websocket"
	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
)

type templateHandler struct {
	once     sync.Once
	filename string
	templ    *template.Template
}

var (
	db       *sql.DB
	upgrader = &websocket.Upgrader{
		ReadBufferSize:  socketBufferSize,
		WriteBufferSize: socketBufferSize,
		CheckOrigin:     checkSameOrigin,
	}
)

const (
	socketBufferSize  = 1024
	messageBufferSize = 256
)

// handling template for our server

func (t *templateHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	t.once.Do(func() {
		t.templ = template.Must(template.ParseFiles(filepath.Join("templates", t.filename)))
	})
	t.templ.Execute(w, r)
}

func createTable() {
	exec := `
	CREATE TABLE IF NOT EXISTS messages (
		id SERIAL PRIMARY KEY,
		room_name VARCHAR(255) NOT NULL,
		user_name VARCHAR(255) NOT NULL,
		message TEXT NOT NULL,
		created_at TIMESTAMPTZ DEFAULT NOW()
	);
	`
	_, err := db.Exec(exec)
	if err != nil {
		log.Fatalf("Could not create messages table: %v", err)
	}
	log.Println("Messages table created or already exists.")
}

func main() {

	// Load .env file, but don't fail if it's not present (for deployment)
	err := godotenv.Load()
	if err != nil {
		log.Println("No .env file found, using environment variables from system")
	}

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("DATABASE_URL environment variable is not set")
	}
	db, err = sql.Open("postgres", databaseURL)
	if err != nil {
		log.Fatalf("Could not connect to database: %v", err)
	}
	defer db.Close()

	// Production settings: Limit connections to prevent overwhelming the DB
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(25)
	db.SetConnMaxLifetime(5 * time.Minute)

	createTable()

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	addr := ":" + port

	http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))
	http.Handle("/", &templateHandler{filename: "index.html"})
	http.Handle("/chat", &templateHandler{filename: "chat.html"})

	http.HandleFunc("/room", func(w http.ResponseWriter, r *http.Request) {
		roomName := sanitizeQueryValue(r.URL.Query().Get("room"), 64)
		if roomName == "" {
			http.Error(w, "Missing or invalid room parameter", http.StatusBadRequest)
			return
		}
		realRoom := getRoom(roomName)

		socket, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Println("Upgrade error:", err)
			return
		}
		client := &client{
			socket:  socket,
			room:    realRoom,
			receive: make(chan []byte, messageBufferSize),
			name:    fmt.Sprintf("user-%s", sanitizeQueryValue(r.URL.Query().Get("user_id"), 32)), // A placeholder for user identity
			db:      db,
		}

		sendRecentMessages(client)

		realRoom.join <- client

		defer func() { realRoom.leave <- client }()
		go client.write()
		client.read()
	})

	// Health check endpoint
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		if err := db.Ping(); err != nil {
			log.Printf("Health check failed: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("Database Unreachable"))
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	//start the web server

	log.Println("starting web server on", addr)

	if err := http.ListenAndServe(addr, CORSMiddleware(http.DefaultServeMux)); err != nil {
		log.Fatal("ListenAndServe:", err)
	}

}

func checkSameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsedOrigin, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return strings.EqualFold(parsedOrigin.Host, r.Host)
}

func sanitizeQueryValue(v string, maxLen int) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if maxLen > 0 {
		runes := []rune(v)
		if len(runes) > maxLen {
			v = string(runes[:maxLen])
		}
	}
	return v
}

func sendRecentMessages(c *client) {
	rows, err := db.Query(`
		SELECT user_name, message FROM messages
		WHERE room_name = $1
		ORDER BY created_at ASC
		LIMIT 50
	`, c.room.name)
	if err != nil {
		log.Printf("Could not query recent messages for room %s: %v", c.room.name, err)
		return
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var userName, message string
		if err := rows.Scan(&userName, &message); err != nil {
			log.Printf("Error scanning message row: %v", err)
			continue
		}
		msgJSON, err := json.Marshal(map[string]string{
			"name":    userName,
			"message": message,
		})
		if err == nil {
			c.receive <- msgJSON
			count++
		} else {
			log.Printf("Error marshaling message for user %s: %v", userName, err)
		}
	}

	if err := rows.Err(); err != nil {
		log.Printf("Error iterating recent messages for room %s: %v", c.room.name, err)
	}

	log.Printf("Sent %d recent messages to %s in room %s", count, c.name, c.room.name)
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
