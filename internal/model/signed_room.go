package model

import "time"

type SignedRoom struct {
	RoomName         string
	OwnerUserID      int64
	OwnerDisplayName string
	ExpiresAt        time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}
