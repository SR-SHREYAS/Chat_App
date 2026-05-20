package api

import (
	"testing"
	"time"
)

func TestSignedRoomJoinLimiter_BlocksAfterFiveFailures(t *testing.T) {
	limiter := newSignedRoomJoinLimiter(5, 2*time.Minute)
	now := time.Now().UTC()
	ip := "1.2.3.4"
	room := "alpha"

	for i := 0; i < 5; i++ {
		if limiter.isBlockedAt(ip, room, "", now.Add(time.Duration(i)*time.Second)) {
			t.Fatalf("expected attempt %d to be allowed", i+1)
		}
		limiter.recordFailureAt(ip, room, "", now.Add(time.Duration(i)*time.Second))
	}

	if !limiter.isBlockedAt(ip, room, "", now.Add(6*time.Second)) {
		t.Fatalf("expected 6th attempt to be blocked after 5 failures")
	}
}

func TestSignedRoomJoinLimiter_ResetsAfterWindow(t *testing.T) {
	window := 2 * time.Minute
	limiter := newSignedRoomJoinLimiter(5, window)
	now := time.Now().UTC()
	ip := "1.2.3.4"
	room := "alpha"

	for i := 0; i < 5; i++ {
		limiter.recordFailureAt(ip, room, "", now.Add(time.Duration(i)*time.Second))
	}
	if !limiter.isBlockedAt(ip, room, "", now.Add(10*time.Second)) {
		t.Fatalf("expected blocked state before window expiry")
	}

	afterWindow := now.Add(window + time.Second)
	if limiter.isBlockedAt(ip, room, "", afterWindow) {
		t.Fatalf("expected limiter to reset after window")
	}

	if limiter.isBlockedAt(ip, room, "", afterWindow.Add(time.Second)) {
		t.Fatalf("expected first attempt after reset to be allowed")
	}
}

func TestSignedRoomJoinLimiter_IsolatedByIPAndRoom(t *testing.T) {
	limiter := newSignedRoomJoinLimiter(5, 2*time.Minute)
	now := time.Now().UTC()

	for i := 0; i < 5; i++ {
		limiter.recordFailureAt("1.2.3.4", "alpha", "", now.Add(time.Duration(i)*time.Second))
	}
	if !limiter.isBlockedAt("1.2.3.4", "alpha", "", now.Add(10*time.Second)) {
		t.Fatalf("expected blocked for original ip+room")
	}
	if limiter.isBlockedAt("1.2.3.5", "alpha", "", now.Add(10*time.Second)) {
		t.Fatalf("expected different IP to remain unblocked")
	}
	if limiter.isBlockedAt("1.2.3.4", "beta", "", now.Add(10*time.Second)) {
		t.Fatalf("expected different room to remain unblocked")
	}
}

func TestSignedRoomJoinLimiter_EmptyIPFallsBackToSubject(t *testing.T) {
	limiter := newSignedRoomJoinLimiter(5, 2*time.Minute)
	now := time.Now().UTC()
	room := "alpha"

	for i := 0; i < 5; i++ {
		limiter.recordFailureAt("", room, "user-1", now.Add(time.Duration(i)*time.Second))
	}
	if !limiter.isBlockedAt("", room, "user-1", now.Add(10*time.Second)) {
		t.Fatalf("expected subject user-1 to be blocked")
	}
	if limiter.isBlockedAt("", room, "user-2", now.Add(10*time.Second)) {
		t.Fatalf("expected different subject to remain unblocked when ip is empty")
	}
}
