package model

import "time"

const SignedRoomEntryCodeLength = 4

type SignedRoom struct {
	RoomName         string
	OwnerUserID      int64
	OwnerDisplayName string
	EntryCode        string
	ExpiresAt        time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}
