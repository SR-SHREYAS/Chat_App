package api

import (
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	signedRoomJoinAttemptLimit  = 5
	signedRoomJoinAttemptWindow = 2 * time.Minute

	signedRoomJoinAttemptLimitEnv         = "SIGNED_ROOM_JOIN_ATTEMPT_LIMIT"
	signedRoomJoinAttemptWindowSecondsEnv = "SIGNED_ROOM_JOIN_ATTEMPT_WINDOW_SECONDS"
)

type signedRoomJoinLimiter struct {
	mu              sync.Mutex
	entries         map[string]joinAttemptEntry
	limit           int
	window          time.Duration
	lastCleanup     time.Time
	cleanupInterval time.Duration
	opCount         uint64
}

type joinAttemptEntry struct {
	failures    int
	windowStart time.Time
	lastSeen    time.Time
}

func newSignedRoomJoinLimiter(limit int, window time.Duration) *signedRoomJoinLimiter {
	if limit < 1 {
		limit = 1
	}
	if window <= 0 {
		window = time.Minute
	}

	return &signedRoomJoinLimiter{
		entries:         make(map[string]joinAttemptEntry),
		limit:           limit,
		window:          window,
		cleanupInterval: window,
	}
}

func newDefaultSignedRoomJoinLimiter() *signedRoomJoinLimiter {
	return newSignedRoomJoinLimiter(
		signedRoomJoinAttemptLimitFromEnv(),
		signedRoomJoinAttemptWindowFromEnv(),
	)
}

func signedRoomJoinAttemptLimitFromEnv() int {
	raw := strings.TrimSpace(os.Getenv(signedRoomJoinAttemptLimitEnv))
	if raw == "" {
		return signedRoomJoinAttemptLimit
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 {
		return signedRoomJoinAttemptLimit
	}
	return value
}

func signedRoomJoinAttemptWindowFromEnv() time.Duration {
	raw := strings.TrimSpace(os.Getenv(signedRoomJoinAttemptWindowSecondsEnv))
	if raw == "" {
		return signedRoomJoinAttemptWindow
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 {
		return signedRoomJoinAttemptWindow
	}
	return time.Duration(value) * time.Second
}

func (l *signedRoomJoinLimiter) IsBlocked(ip, roomName, subject string) bool {
	return l.isBlockedAt(ip, roomName, subject, time.Now())
}

func (l *signedRoomJoinLimiter) RecordFailure(ip, roomName, subject string) {
	l.recordFailureAt(ip, roomName, subject, time.Now())
}

func (l *signedRoomJoinLimiter) Reset(ip, roomName, subject string) {
	key := joinAttemptKey(ip, roomName, subject)

	l.mu.Lock()
	delete(l.entries, key)
	l.mu.Unlock()
}

func (l *signedRoomJoinLimiter) isBlockedAt(ip, roomName, subject string, now time.Time) bool {
	key := joinAttemptKey(ip, roomName, subject)

	l.mu.Lock()
	defer l.mu.Unlock()

	l.maybeCleanup(now)

	entry, ok := l.entries[key]
	if !ok {
		return false
	}
	if now.Sub(entry.windowStart) >= l.window {
		delete(l.entries, key)
		return false
	}

	return entry.failures >= l.limit
}

func (l *signedRoomJoinLimiter) recordFailureAt(ip, roomName, subject string, now time.Time) {
	key := joinAttemptKey(ip, roomName, subject)

	l.mu.Lock()
	defer l.mu.Unlock()

	l.maybeCleanup(now)

	entry := l.entries[key]
	if entry.windowStart.IsZero() || now.Sub(entry.windowStart) >= l.window {
		entry.failures = 0
		entry.windowStart = now
	}
	entry.failures++
	entry.lastSeen = now
	l.entries[key] = entry
}

func (l *signedRoomJoinLimiter) maybeCleanup(now time.Time) {
	l.opCount++
	if l.cleanupInterval <= 0 {
		l.cleanupInterval = l.window
	}
	if l.lastCleanup.IsZero() {
		l.cleanupExpired(now)
		l.lastCleanup = now
		return
	}

	// Run full cleanup periodically by time and occasionally by operation count,
	// instead of on every request path.
	if now.Sub(l.lastCleanup) < l.cleanupInterval && l.opCount%64 != 0 {
		return
	}

	l.cleanupExpired(now)
	l.lastCleanup = now
}

func (l *signedRoomJoinLimiter) cleanupExpired(now time.Time) {
	// Keep data a bit longer than one window to avoid map churn.
	threshold := now.Add(-2 * l.window)
	for key, entry := range l.entries {
		if entry.lastSeen.Before(threshold) {
			delete(l.entries, key)
		}
	}
}

func joinAttemptKey(ip, roomName, subject string) string {
	normalizedIP := strings.TrimSpace(ip)
	normalizedRoom := strings.TrimSpace(roomName)
	normalizedSubject := strings.TrimSpace(subject)
	if normalizedRoom == "" {
		normalizedRoom = "unknown-room"
	}

	switch {
	case normalizedIP != "" && normalizedSubject != "":
		return "ip:" + normalizedIP + "|subject:" + normalizedSubject + "|room:" + normalizedRoom
	case normalizedIP != "":
		return "ip-only:" + normalizedIP + "|room:" + normalizedRoom
	case normalizedSubject != "":
		return "subject:" + normalizedSubject + "|room:" + normalizedRoom
	default:
		return "room-only|" + normalizedRoom
	}
}
