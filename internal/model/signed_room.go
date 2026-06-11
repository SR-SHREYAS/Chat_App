package model

import "time"

const SignedRoomEntryCodeLength = 4

func SignedRoomEntryCodeMinValue() int64 {
	min := int64(1)
	for i := 1; i < SignedRoomEntryCodeLength; i++ {
		min *= 10
	}
	return min
}

func SignedRoomEntryCodeRangeSize() int64 {
	return SignedRoomEntryCodeMinValue() * 9
}

type SignedRoom struct {
	ID            string
	RoomName      string
	OwnerUserID   string
	OwnerUserName string
	EntryCode     string
	ExpiresAt     time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type RoomHistory struct {
	RoomID        string
	RoomName      string
	Role          string
	OwnerUserID   string
	OwnerUserName string
	EntryCode     string
	ExpiresAt     time.Time
	JoinedAt      time.Time
	LastVisitedAt time.Time
	Active        bool
}
