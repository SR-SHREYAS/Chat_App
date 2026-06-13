package util

import "fmt"

// AppError carries HTTP status codes, user-facing messages, and internal debugging context.
type AppError struct {
	StatusCode int
	Message    string // Public message returned to the client
	Internal   error  // Internal error for logging (e.g., the actual SQL error)
}

func (e *AppError) Error() string {
	if e.Internal != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Internal)
	}
	return e.Message
}

func NewAppError(code int, message string, internal error) *AppError {
	return &AppError{
		StatusCode: code,
		Message:    message,
		Internal:   internal,
	}
}
