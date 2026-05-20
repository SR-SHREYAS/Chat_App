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

func TestDefaultSignedRoomJoinLimiter_UsesEnvironmentOverrides(t *testing.T) {
	t.Setenv(signedRoomJoinAttemptLimitEnv, "2")
	t.Setenv(signedRoomJoinAttemptWindowSecondsEnv, "3")

	limiter := newDefaultSignedRoomJoinLimiter()

	if limiter.limit != 2 {
		t.Fatalf("expected env limit override, got %d", limiter.limit)
	}
	if limiter.window != 3*time.Second {
		t.Fatalf("expected env window override, got %v", limiter.window)
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

	limiter.recordFailureAt(ip, room, "", afterWindow)
	if limiter.isBlockedAt(ip, room, "", afterWindow.Add(time.Second)) {
		t.Fatalf("expected to remain unblocked after first post-reset failure")
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

func TestSignedRoomJoinLimiter_UsesBothIPAndSubjectWhenAvailable(t *testing.T) {
	limiter := newSignedRoomJoinLimiter(5, 2*time.Minute)
	now := time.Now()
	ip := "1.2.3.4"
	room := "alpha"

	for i := 0; i < 5; i++ {
		limiter.recordFailureAt(ip, room, "user-1", now.Add(time.Duration(i)*time.Second))
	}
	if !limiter.isBlockedAt(ip, room, "user-1", now.Add(10*time.Second)) {
		t.Fatalf("expected user-1 to be blocked")
	}
	if limiter.isBlockedAt(ip, room, "user-2", now.Add(10*time.Second)) {
		t.Fatalf("expected different subject on same IP to remain unblocked")
	}
}

func TestSignedRoomJoinLimiter_PreservesCaseForRoomAndSubject(t *testing.T) {
	limiter := newSignedRoomJoinLimiter(5, 2*time.Minute)
	now := time.Now()
	ip := "1.2.3.4"

	for i := 0; i < 5; i++ {
		limiter.recordFailureAt(ip, "RoomA", "UserA", now.Add(time.Duration(i)*time.Second))
	}
	if !limiter.isBlockedAt(ip, "RoomA", "UserA", now.Add(10*time.Second)) {
		t.Fatalf("expected original case key to be blocked")
	}
	if limiter.isBlockedAt(ip, "rooma", "UserA", now.Add(10*time.Second)) {
		t.Fatalf("expected differently-cased room name to use separate bucket")
	}
	if limiter.isBlockedAt(ip, "RoomA", "usera", now.Add(10*time.Second)) {
		t.Fatalf("expected differently-cased subject to use separate bucket")
	}
}
