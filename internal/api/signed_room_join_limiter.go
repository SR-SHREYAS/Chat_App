package api

import (
	"strings"
	"sync"
	"time"
)

const (
	signedRoomJoinAttemptLimit  = 5
	signedRoomJoinAttemptWindow = 2 * time.Minute
)

type signedRoomJoinLimiter struct {
	mu      sync.Mutex
	entries map[string]joinAttemptEntry
	limit   int
	window  time.Duration
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
		entries: make(map[string]joinAttemptEntry),
		limit:   limit,
		window:  window,
	}
}

func (l *signedRoomJoinLimiter) IsBlocked(ip, roomName string) bool {
	return l.isBlockedAt(ip, roomName, time.Now().UTC())
}

func (l *signedRoomJoinLimiter) RecordFailure(ip, roomName string) {
	l.recordFailureAt(ip, roomName, time.Now().UTC())
}

func (l *signedRoomJoinLimiter) Reset(ip, roomName string) {
	key := joinAttemptKey(ip, roomName)

	l.mu.Lock()
	delete(l.entries, key)
	l.mu.Unlock()
}

func (l *signedRoomJoinLimiter) isBlockedAt(ip, roomName string, now time.Time) bool {
	key := joinAttemptKey(ip, roomName)

	l.mu.Lock()
	defer l.mu.Unlock()

	l.cleanupExpired(now)

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

func (l *signedRoomJoinLimiter) recordFailureAt(ip, roomName string, now time.Time) {
	key := joinAttemptKey(ip, roomName)

	l.mu.Lock()
	defer l.mu.Unlock()

	l.cleanupExpired(now)

	entry := l.entries[key]
	if entry.windowStart.IsZero() || now.Sub(entry.windowStart) >= l.window {
		entry.failures = 0
		entry.windowStart = now
	}
	entry.failures++
	entry.lastSeen = now
	l.entries[key] = entry
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

func joinAttemptKey(ip, roomName string) string {
	normalizedIP := strings.TrimSpace(strings.ToLower(ip))
	if normalizedIP == "" {
		normalizedIP = "unknown-ip"
	}

	normalizedRoom := strings.TrimSpace(strings.ToLower(roomName))
	if normalizedRoom == "" {
		normalizedRoom = "unknown-room"
	}

	return normalizedIP + "|" + normalizedRoom
}
