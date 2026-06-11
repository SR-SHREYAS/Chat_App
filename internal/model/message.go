package model

import "time"

type Message struct {
	ID           string
	RoomID       string
	SenderUserID string
	UserName     string
	Message      string
	CreatedAt    time.Time
}
