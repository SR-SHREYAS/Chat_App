// Package postgres owns the PostgreSQL connection lifecycle.
package postgres

import (
	"context"
	"database/sql"
	"time"
)

const (
	maxOpenConnections = 25
	maxIdleConnections = 25
	connectionMaxLife  = 5 * time.Minute
)

// Client is the minimal database capability repositories need to run queries.
// It deliberately exposes no connection construction or lifecycle operations.
type Client interface {
	BeginTx(context.Context, *sql.TxOptions) (*sql.Tx, error)
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	PingContext(context.Context) error
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type Storage struct {
	client *sql.DB
}

func Open(databaseURL string) (*Storage, error) {
	client, err := sql.Open("postgres", databaseURL)
	if err != nil {
		return nil, err
	}

	client.SetMaxOpenConns(maxOpenConnections)
	client.SetMaxIdleConns(maxIdleConnections)
	client.SetConnMaxLifetime(connectionMaxLife)

	return &Storage{client: client}, nil
}

// New wraps an existing database connection. It is intended for composition
// roots that provide their own configured connection, such as integration tests.
func New(client *sql.DB) *Storage {
	return &Storage{client: client}
}

func (s *Storage) Client() Client {
	return s.client
}

func (s *Storage) Close() error {
	return s.client.Close()
}
