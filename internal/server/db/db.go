package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Client wraps a *pgxpool.Pool for documents-table operations.
type Client struct {
	pool *pgxpool.Pool
}

// New returns a Client backed by pool.
func New(pool *pgxpool.Pool) *Client {
	return &Client{pool: pool}
}

// NewFromDSN opens a pgxpool from dsn and returns a Client.
func NewFromDSN(ctx context.Context, dsn string) (*Client, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return New(pool), nil
}

// Close closes the underlying pool.
func (c *Client) Close() { c.pool.Close() }
