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
	RoomName         string
	OwnerUserID      int64
	OwnerDisplayName string
	EntryCode        string
	ExpiresAt        time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}
