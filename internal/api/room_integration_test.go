package api

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"real_time_chat_app/internal/app/chat"
	"real_time_chat_app/internal/model"
)

type savedMessage struct {
	roomName string
	userName string
	message  string
}

type integrationMessageStore struct {
	mu     sync.Mutex
	recent map[string][]model.Message
	saved  []savedMessage

	pingErr error
}

func newIntegrationMessageStore(recent map[string][]model.Message) *integrationMessageStore {
	if recent == nil {
		recent = make(map[string][]model.Message)
	}
	return &integrationMessageStore{recent: recent}
}

func (s *integrationMessageStore) SaveMessage(_ context.Context, roomName, userName, message string) error {
	s.mu.Lock()
	s.saved = append(s.saved, savedMessage{
		roomName: roomName,
		userName: userName,
		message:  message,
	})
	s.mu.Unlock()
	return nil
}

func (s *integrationMessageStore) GetRecentMessages(_ context.Context, roomName string, _ int) ([]model.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	src := s.recent[roomName]
	out := make([]model.Message, len(src))
	copy(out, src)
	return out, nil
}

func (s *integrationMessageStore) Ping(context.Context) error {
	return s.pingErr
}

func (s *integrationMessageStore) SavedMessages() []savedMessage {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]savedMessage, len(s.saved))
	copy(out, s.saved)
	return out
}

type chatPayload struct {
	Name    string `json:"name"`
	Message string `json:"message"`
}

func TestRoomBroadcastAndPersistenceIntegration(t *testing.T) {
	store := newIntegrationMessageStore(nil)
	server := startTestServer(t, store)

	clientA := dialRoomSocket(t, server.URL, "integration-room", "u1")
	defer clientA.Close()

	clientB := dialRoomSocket(t, server.URL, "integration-room", "u2")
	defer clientB.Close()

	const sent = "hello from client A"
	if err := clientA.WriteMessage(websocket.TextMessage, []byte(sent)); err != nil {
		t.Fatalf("write message: %v", err)
	}

	payloadA := readUntil(t, clientA, 4*time.Second, func(p chatPayload) bool {
		return p.Name == "user-u1" && p.Message == sent
	})
	payloadB := readUntil(t, clientB, 4*time.Second, func(p chatPayload) bool {
		return p.Name == "user-u1" && p.Message == sent
	})

	if payloadA.Message != sent || payloadB.Message != sent {
		t.Fatalf("message mismatch after broadcast: clientA=%+v clientB=%+v", payloadA, payloadB)
	}

	assertEventually(t, 4*time.Second, func() error {
		saved := store.SavedMessages()
		if len(saved) != 1 {
			return errors.New("waiting for saved message")
		}
		if saved[0].roomName != "integration-room" {
			return errors.New("unexpected room name in saved message")
		}
		if saved[0].userName != "user-u1" {
			return errors.New("unexpected user name in saved message")
		}
		if saved[0].message != sent {
			return errors.New("unexpected body in saved message")
		}
		return nil
	})
}

func startTestServer(t *testing.T, messageStore *integrationMessageStore) *httptest.Server {
	t.Helper()

	chatService := chat.NewService(messageStore)
	handler := NewHandler(chatService, nil)

	mux := http.NewServeMux()
	mux.HandleFunc("/room", handler.handleRoom)
	mux.HandleFunc("/health", handler.handleHealth)

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func dialRoomSocket(t *testing.T, serverURL, roomName, userID string) *websocket.Conn {
	t.Helper()

	wsURL := "ws" + strings.TrimPrefix(serverURL, "http") + "/room?room=" + url.QueryEscape(roomName) + "&user_id=" + url.QueryEscape(userID)
	headers := http.Header{}
	headers.Set("Origin", serverURL)

	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, headers)
	if err != nil {
		if resp != nil {
			t.Fatalf("dial websocket failed (%d): %v", resp.StatusCode, err)
		}
		t.Fatalf("dial websocket failed: %v", err)
	}
	return conn
}

func readUntil(t *testing.T, conn *websocket.Conn, timeout time.Duration, match func(chatPayload) bool) chatPayload {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		_ = conn.SetReadDeadline(time.Now().Add(250 * time.Millisecond))
		_, raw, err := conn.ReadMessage()
		if err != nil {
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				continue
			}
			t.Fatalf("read websocket message: %v", err)
		}

		var payload chatPayload
		if err := json.Unmarshal(raw, &payload); err != nil {
			continue
		}
		if match(payload) {
			return payload
		}
	}

	t.Fatalf("timed out waiting for expected websocket payload")
	return chatPayload{}
}

func assertEventually(t *testing.T, timeout time.Duration, check func() error) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		if err := check(); err == nil {
			return
		} else {
			lastErr = err
		}
		time.Sleep(50 * time.Millisecond)
	}

	t.Fatalf("condition not met before timeout: %v", lastErr)
}
