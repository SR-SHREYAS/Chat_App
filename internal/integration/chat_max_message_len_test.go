package integration

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"real_time_chat_app/internal/api"
	authapp "real_time_chat_app/internal/app/auth"
	"real_time_chat_app/internal/app/chat"
	"real_time_chat_app/internal/store"

	"github.com/gorilla/websocket"
	_ "github.com/lib/pq"
)

// NOTE: This test assumes a Postgres instance is reachable via TEST_DATABASE_URL.
// Example value: postgres://user:pass@localhost:5432/chat_app_test?sslmode=disable
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()

	dsn := getenvOrSkip(t, "TEST_DATABASE_URL")
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(10)
	db.SetConnMaxLifetime(5 * time.Minute)

	if err := db.Ping(); err != nil {
		t.Fatalf("failed to ping test database: %v", err)
	}
	return db
}

func getenvOrSkip(t *testing.T, name string) string {
	t.Helper()
	value := strings.TrimSpace(strings.TrimSpace(getenv(name)))
	if value == "" {
		t.Skipf("%s not set; skipping integration test", name)
	}
	return value
}

// tiny wrapper to decouple from os.Getenv in case we add helpers later.
func getenv(name string) string {
	return strings.TrimSpace(os.Getenv(name))
}

// TestMaxMessageLengthOversizedMessageEmitsErrorAndIsNotPersisted
// boots the HTTP+WS+DB stack and verifies that an oversized message:
//
//   - results in a `{type:"error", code:"message_too_long"}` event over WebSocket, and
//   - is NOT persisted in the messages store.
func TestMaxMessageLengthOversizedMessageEmitsErrorAndIsNotPersisted(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	// Ensure DB schema.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	authStore := store.NewAuthStore(db)
	if err := authStore.EnsureSchema(ctx); err != nil {
		t.Fatalf("EnsureSchema(auth): %v", err)
	}

	signedRoomStore := store.NewSignedRoomStore(db)
	if err := signedRoomStore.EnsureSchema(ctx); err != nil {
		t.Fatalf("EnsureSchema(signed_rooms): %v", err)
	}

	messageStore := store.NewMessageStore(db)
	if err := messageStore.EnsureSchema(ctx); err != nil {
		t.Fatalf("EnsureSchema(messages): %v", err)
	}

	chatService := chat.NewService(messageStore)
	chatService.BindSignedRoomStore(signedRoomStore)
	authService := authapp.NewService(authStore)
	handler := api.NewHandler(chatService, authService)

	// Use an httptest.Server so we can connect via real WebSocket.
	mux := http.NewServeMux()
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))
	api.RegisterRoutes(mux, handler)

	testServer := httptest.NewServer(mux)
	defer testServer.Close()

	// Create a signed room via HTTP so /room has something to bind to.
	roomID, chatURL := createSignedRoomForTest(t, testServer)

	// Connect WebSocket: /room?room_id=...&user_id=...
	wsURL := makeWebSocketURL(t, testServer, "/room?room_id="+roomID+"&user_id=test-user")
	wsConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer wsConn.Close()

	// Send an oversized message (MaxMessageLen is 2000; we send 2001 chars).
	oversized := strings.Repeat("x", chat.MaxMessageLen+1)
	if err := wsConn.WriteMessage(websocket.TextMessage, []byte(oversized)); err != nil {
		t.Fatalf("write oversized message: %v", err)
	}

	// Expect an error envelope.
	type errorEvent struct {
		Type    string `json:"type"`
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	_ = chatURL // not used but helpful for debugging if needed.

	_ = wsConn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, payload, err := wsConn.ReadMessage()
	if err != nil {
		t.Fatalf("read error event: %v", err)
	}

	var ev errorEvent
	if err := json.Unmarshal(payload, &ev); err != nil {
		t.Fatalf("unmarshal error event: %v (raw=%s)", err, string(payload))
	}
	if ev.Type != "error" {
		t.Fatalf("expected error event type, got %q", ev.Type)
	}
	if ev.Code != "message_too_long" {
		t.Fatalf("expected error code %q, got %q", "message_too_long", ev.Code)
	}
	if ev.Message == "" {
		t.Fatalf("expected non-empty error message")
	}

	// Finally, assert the oversized message was NOT persisted.
	assertNoMessageWithContent(t, messageStore, ctx, oversized)
}

func createSignedRoomForTest(t *testing.T, srv *httptest.Server) (roomID, chatURL string) {
	t.Helper()

	// (VALID_DIRECTORY) 1) Sign up a test user to obtain a session cookie.
	client := signUpAndGetClient(t, srv)

	// (VALID_DIRECTORY) 2) Create a signed room as that authenticated user.
	body := `{"room_name":"integration-test-room"}`
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/rooms/create", strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("create room request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected create room status: %d", resp.StatusCode)
	}

	var payload struct {
		RoomID  string `json:"room_id"`
		ChatURL string `json:"chat_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode create room response: %v", err)
	}
	if payload.RoomID == "" || payload.ChatURL == "" {
		t.Fatalf("missing room_id or chat_url in create room response: %+v", payload)
	}
	return payload.RoomID, payload.ChatURL
}

// (VALID_DIRECTORY) signUpAndGetClient performs a signup request and returns an HTTP client
// (VALID_DIRECTORY) that carries the session cookie for subsequent authenticated requests.
func signUpAndGetClient(t *testing.T, srv *httptest.Server) *http.Client {
	t.Helper()

	// (VALID_DIRECTORY) Use a cookie jar to store the session cookie set by /api/auth/signup.
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("failed to create cookie jar: %v", err)
	}
	client := &http.Client{
		Jar: jar,
	}

	body := `{"email":"testuser@example.com","username":"testuser","password":"password123"}`
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/auth/signup", strings.NewReader(body))
	if err != nil {
		t.Fatalf("new signup request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("signup request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		var payload map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&payload)
		t.Fatalf("unexpected signup status: %d payload=%v", resp.StatusCode, payload)
	}

	// (VALID_DIRECTORY) jar is automatically populated with cookies from the signup response.
	return client
}

func makeWebSocketURL(t *testing.T, srv *httptest.Server, pathAndQuery string) string {
	t.Helper()

	u := srv.URL
	// httptest.Server uses http://127.0.0.1:port
	// convert to ws://
	u = strings.Replace(u, "http://", "ws://", 1)

	if !strings.HasPrefix(pathAndQuery, "/") {
		pathAndQuery = "/" + pathAndQuery
	}
	if strings.HasSuffix(u, "/") {
		return u[:len(u)-1] + pathAndQuery
	}
	return u + pathAndQuery
}

func assertNoMessageWithContent(t *testing.T, messageStore *store.MessageStore, ctx context.Context, content string) {
	t.Helper()

	// This assumes MessageStore exposes a method to query messages.
	// If not, you can query the DB directly via messageStore.DB or a dedicated helper.
	rows, err := messageStore.DB().QueryContext(ctx, `SELECT message FROM messages WHERE message = $1`, content)
	if err != nil {
		t.Fatalf("query messages: %v", err)
	}
	defer rows.Close()

	if rows.Next() {
		var msg string
		if err := rows.Scan(&msg); err != nil {
			t.Fatalf("scan message: %v", err)
		}
		t.Fatalf("unexpected persisted message with oversized content: %q", msg)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows error: %v", err)
	}
}
