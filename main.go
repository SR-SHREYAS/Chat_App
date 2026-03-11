package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"text/template"
	"time"

	"github.com/gorilla/websocket"
	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
	"golang.org/x/crypto/bcrypt"
)

type templateHandler struct {
	once     sync.Once
	filename string
	templ    *template.Template
}

var (
	db       *sql.DB
	upgrader = &websocket.Upgrader{ReadBufferSize: 1024, WriteBufferSize: 1024}
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

	CREATE TABLE IF NOT EXISTS rooms (
		name VARCHAR(255) PRIMARY KEY,
		password_hash VARCHAR(255) NOT NULL,
		expires_at TIMESTAMPTZ NOT NULL,
		created_at TIMESTAMPTZ DEFAULT NOW()
	);
	`
	_, err := db.Exec(exec)
	if err != nil {
		log.Fatalf("Could not create tables: %v", err)
	}
	log.Println("Database tables created or already exist.")
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

	http.HandleFunc("/room", handleRoom)

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

func handleRoom(w http.ResponseWriter, r *http.Request) {
	roomName := r.URL.Query().Get("room")
	if roomName == "" {
		http.Error(w, "Missing room parameter", http.StatusBadRequest)
		return
	}

	// Upgrade to WebSocket immediately, postponing auth to the first message
	socket, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("Upgrade error:", err)
		return
	}
	defer socket.Close()

	// Set a deadline and size limit for reading the auth message to prevent abuse
	socket.SetReadDeadline(time.Now().Add(10 * time.Second))
	socket.SetReadLimit(1024) // Limit auth message to 1KB

	// Expect the first message to be authentication
	var authData struct {
		Type     string `json:"type"`
		Password string `json:"password"`
	}
	if err := socket.ReadJSON(&authData); err != nil {
		log.Printf("Failed to read auth message: %v", err)
		return
	}

	// Reset read constraints for normal chat operation (0 = no limit)
	socket.SetReadDeadline(time.Time{})
	socket.SetReadLimit(0)

	if authData.Type != "auth" {
		log.Println("Expected auth message type")
		socket.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "Expected auth message"))
		return
	}

	expiresAt, err := authenticateOrCreateRoom(db, roomName, authData.Password)
	if err != nil {
		log.Printf("Authentication failed for room %s: %v", roomName, err)
		socket.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "Authentication failed"))
		return
	}

	// Auth successful, proceed with setup
	realRoom := getRoom(roomName)
	client := &client{
		socket:  socket,
		room:    realRoom,
		receive: make(chan []byte, messageBufferSize),
		name:    fmt.Sprintf("user-%s", r.URL.Query().Get("user_id")),
		db:      db,
	}

	sendRecentMessages(client)

	// Send expiry info
	expirationMsg := fmt.Sprintf("Welcome! This private room will expire at %s", expiresAt.Format(time.RFC1123))
	sysMsg := map[string]string{"name": "System", "message": expirationMsg}
	if msgBytes, err := json.Marshal(sysMsg); err == nil {
		client.receive <- msgBytes
	}

	realRoom.join <- client
	defer func() { realRoom.leave <- client }()
	go client.write()
	client.read()
}

func authenticateOrCreateRoom(db *sql.DB, roomName, password string) (time.Time, error) {
	var passwordHash string
	var expiresAt time.Time
	err := db.QueryRow("SELECT password_hash, expires_at FROM rooms WHERE name = $1", roomName).Scan(&passwordHash, &expiresAt)

	// Check if the room has expired
	if err == nil && time.Now().After(expiresAt) {
		log.Printf("Room '%s' has expired. Deleting old data.", roomName)
		tx, err := db.Begin()
		if err != nil {
			return time.Time{}, fmt.Errorf("begin tx: %w", err)
		}
		defer tx.Rollback() // Rollback if Commit is not called or fails

		if _, err := tx.Exec("DELETE FROM messages WHERE room_name = $1", roomName); err != nil {
			return time.Time{}, fmt.Errorf("delete messages: %w", err)
		}
		if _, err := tx.Exec("DELETE FROM rooms WHERE name = $1", roomName); err != nil {
			return time.Time{}, fmt.Errorf("delete rooms: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return time.Time{}, fmt.Errorf("commit tx: %w", err)
		}
		err = sql.ErrNoRows // Treat as non-existent to trigger recreation
	}

	if err == sql.ErrNoRows {
		if password == "" {
			return time.Time{}, fmt.Errorf("password required for creation")
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			return time.Time{}, fmt.Errorf("hash password: %w", err)
		}
		expiresAt = time.Now().Add(24 * time.Hour)
		if _, err := db.Exec("INSERT INTO rooms (name, password_hash, expires_at) VALUES ($1, $2, $3)", roomName, string(hash), expiresAt); err != nil {
			return time.Time{}, fmt.Errorf("create room: %w", err)
		}
		log.Printf("New private room created: %s", roomName)
		return expiresAt, nil
	} else if err != nil {
		return time.Time{}, fmt.Errorf("query room: %w", err)
	}

	// Room exists, check password
	if password == "" {
		return time.Time{}, fmt.Errorf("password required")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(password)); err != nil {
		return time.Time{}, fmt.Errorf("invalid password")
	}

	return expiresAt, nil
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
