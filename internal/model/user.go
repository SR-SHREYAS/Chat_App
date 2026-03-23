package model

import "time"

type User struct {
	ID          int64
	Email       string
	DisplayName string
	CreatedAt   time.Time
}
