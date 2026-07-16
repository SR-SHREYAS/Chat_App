package model

import "time"

type SignedRoom struct {
	ID            string
	RoomName      string
	OwnerUserID   string
	OwnerUsername string
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
	OwnerUsername string
	EntryCode     string
	ExpiresAt     time.Time
	JoinedAt      time.Time
	LastVisitedAt time.Time
	Active        bool
}
