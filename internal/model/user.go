package model

import "time"

type User struct {
	ID        string
	Email     string
	UserName  string
	CreatedAt time.Time
	UpdatedAt time.Time
}
