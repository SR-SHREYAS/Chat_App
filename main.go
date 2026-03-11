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

// ---------- Constants ----------

const (
	socketBufferSize   = 1024
	messageBufferSize  = 256
	recentMessageLimit = 50
	roomTTL            = 24 * time.Hour
	authReadTimeout    = 10 * time.Second
	authMaxMessageSize = 1024
	defaultReadLimit   = 32768 // 32KB per chat message
)

// ---------- Globals ----------

var (
	db       *sql.DB
	upgrader = &websocket.Upgrader{
		ReadBufferSize:  socketBufferSize,
		WriteBufferSize: socketBufferSize,
	}
)

// ---------- Template Handler ----------

type templateHandler struct {
	once     sync.Once
	filename string
	templ    *template.Template
}

func (t *templateHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	t.once.Do(func() {
		t.templ = template.Must(template.ParseFiles(filepath.Join("templates", t.filename)))
	})
	t.templ.Execute(w, r)
}

// ---------- Database Setup ----------

func createTables() {
	schema := `
	CREATE TABLE IF NOT EXISTS messages (
		id         SERIAL PRIMARY KEY,
		room_name  VARCHAR(255) NOT NULL,
		user_name  VARCHAR(255) NOT NULL,
		message    TEXT NOT NULL,
		created_at TIMESTAMPTZ DEFAULT NOW()
	);

	CREATE TABLE IF NOT EXISTS rooms (
		name          VARCHAR(255) PRIMARY KEY,
		password_hash VARCHAR(255) NOT NULL,
		expires_at    TIMESTAMPTZ NOT NULL,
		created_at    TIMESTAMPTZ DEFAULT NOW()
	);
	`
	if _, err := db.Exec(schema); err != nil {
		log.Fatalf("Could not create tables: %v", err)
	}
	log.Println("Database tables verified.")
}

// ---------- Main ----------

func main() {
	// Load .env file (optional for deployed environments)
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, using system environment variables.")
	}

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("DATABASE_URL environment variable is not set.")
	}

	var err error
	db, err = sql.Open("postgres", databaseURL)
	if err != nil {
		log.Fatalf("Could not connect to database: %v", err)
	}
	defer db.Close()

	// Connection pool settings
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(25)
	db.SetConnMaxLifetime(5 * time.Minute)

	createTables()

	// Routes
	http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))
	http.Handle("/", &templateHandler{filename: "index.html"})
	http.Handle("/chat", &templateHandler{filename: "chat.html"})
	http.HandleFunc("/room", handleRoom)
	http.HandleFunc("/health", handleHealth)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	addr := ":" + port

	log.Printf("Starting web server on %s", addr)
	if err := http.ListenAndServe(addr, CORSMiddleware(http.DefaultServeMux)); err != nil {
		log.Fatalf("ListenAndServe: %v", err)
	}
}

// ---------- HTTP Handlers ----------

func handleHealth(w http.ResponseWriter, r *http.Request) {
	if err := db.Ping(); err != nil {
		log.Printf("Health check failed: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Database Unreachable"))
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

func handleRoom(w http.ResponseWriter, r *http.Request) {
	roomName := r.URL.Query().Get("room")
	if roomName == "" {
		http.Error(w, "Missing room parameter", http.StatusBadRequest)
		return
	}

	// Upgrade HTTP to WebSocket
	socket, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade error: %v", err)
		return
	}
	defer socket.Close()

	// --- Phase 1: Authentication ---
	expiresAt, err := readAndAuthenticate(socket, roomName)
	if err != nil {
		log.Printf("Auth failed for room %q: %v", roomName, err)
		socket.WriteMessage(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "Authentication failed"),
		)
		return
	}

	// --- Phase 2: Reset limits for normal chat operation ---
	socket.SetReadDeadline(time.Time{})   // No deadline
	socket.SetReadLimit(defaultReadLimit) // 32KB per message

	// --- Phase 3: Set up client ---
	chatRoom := getRoom(roomName)

	userID := r.URL.Query().Get("user_id")
	if userID == "" {
		userID = "anonymous"
	}

	c := &client{
		socket:  socket,
		room:    chatRoom,
		receive: make(chan []byte, messageBufferSize),
		name:    userID,
		db:      db,
	}

	// Send message history before joining
	sendRecentMessages(c)

	// Send room expiry notice
	expiryNotice := fmt.Sprintf("Welcome! This private room expires at %s", expiresAt.Format(time.RFC1123))
	if msgBytes, err := json.Marshal(map[string]string{"name": "System", "message": expiryNotice}); err == nil {
		c.receive <- msgBytes
	}

	// Join the room
	chatRoom.join <- c
	defer func() { chatRoom.leave <- c }()

	go c.write()
	c.read() // Blocks until the client disconnects
}

// ---------- WebSocket Authentication ----------

// authMessage represents the expected first message from the client.
type authMessage struct {
	Type     string `json:"type"`
	Password string `json:"password"`
}

// readAndAuthenticate reads the first WebSocket message and validates the room password.
func readAndAuthenticate(socket *websocket.Conn, roomName string) (time.Time, error) {
	socket.SetReadDeadline(time.Now().Add(authReadTimeout))
	socket.SetReadLimit(authMaxMessageSize)

	var auth authMessage
	if err := socket.ReadJSON(&auth); err != nil {
		return time.Time{}, fmt.Errorf("read auth message: %w", err)
	}

	if auth.Type != "auth" {
		return time.Time{}, fmt.Errorf("unexpected message type: %q", auth.Type)
	}

	return authenticateOrCreateRoom(roomName, auth.Password)
}

// ---------- Room Authentication / TTL Logic ----------

func authenticateOrCreateRoom(roomName, password string) (time.Time, error) {
	var passwordHash string
	var expiresAt time.Time

	err := db.QueryRow(
		"SELECT password_hash, expires_at FROM rooms WHERE name = $1", roomName,
	).Scan(&passwordHash, &expiresAt)

	switch {
	case err == sql.ErrNoRows:
		// Room does not exist — create it
		return createRoom(roomName, password)

	case err != nil:
		return time.Time{}, fmt.Errorf("query room: %w", err)

	case time.Now().After(expiresAt):
		// Room has expired — delete and recreate
		if err := deleteExpiredRoom(roomName); err != nil {
			return time.Time{}, fmt.Errorf("cleanup expired room: %w", err)
		}
		return createRoom(roomName, password)

	default:
		// Room exists and is valid — verify password
		if password == "" {
			return time.Time{}, fmt.Errorf("password required")
		}
		if err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(password)); err != nil {
			return time.Time{}, fmt.Errorf("invalid password")
		}
		return expiresAt, nil
	}
}

func createRoom(roomName, password string) (time.Time, error) {
	if password == "" {
		return time.Time{}, fmt.Errorf("password required for room creation")
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return time.Time{}, fmt.Errorf("hash password: %w", err)
	}

	expiresAt := time.Now().Add(roomTTL)
	_, err = db.Exec(
		"INSERT INTO rooms (name, password_hash, expires_at) VALUES ($1, $2, $3)",
		roomName, string(hash), expiresAt,
	)
	if err != nil {
		return time.Time{}, fmt.Errorf("insert room: %w", err)
	}

	log.Printf("New private room created: %s (expires %s)", roomName, expiresAt.Format(time.RFC1123))
	return expiresAt, nil
}

func deleteExpiredRoom(roomName string) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	// Rollback is a no-op if Commit succeeds
	defer tx.Rollback()

	if _, err := tx.Exec("DELETE FROM messages WHERE room_name = $1", roomName); err != nil {
		return fmt.Errorf("delete messages: %w", err)
	}
	if _, err := tx.Exec("DELETE FROM rooms WHERE name = $1", roomName); err != nil {
		return fmt.Errorf("delete room row: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}

	log.Printf("Expired room deleted: %s", roomName)
	return nil
}

// ---------- Message History ----------

func sendRecentMessages(c *client) {
	rows, err := db.Query(`
		SELECT user_name, message FROM messages
		WHERE room_name = $1
		ORDER BY created_at ASC
		LIMIT $2
	`, c.room.name, recentMessageLimit)
	if err != nil {
		log.Printf("Could not query messages for room %s: %v", c.room.name, err)
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
		if err != nil {
			log.Printf("Error marshaling message: %v", err)
			continue
		}
		c.receive <- msgJSON
		count++
	}

	if err := rows.Err(); err != nil {
		log.Printf("Error iterating messages for room %s: %v", c.room.name, err)
	}
	log.Printf("Sent %d recent messages to %s in room %s", count, c.name, c.room.name)
}

// ---------- CORS Middleware ----------

func CORSMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}
