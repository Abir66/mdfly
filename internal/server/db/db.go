package db

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// queryer runs one statement. Satisfied by both *pgxpool.Pool and pgx.Tx, so
// every query in this package works either on its own or inside a transaction.
type queryer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// ops carries every statement in this package, bound either to the pool
// (*Client) or to an open transaction (*Tx).
type ops struct {
	q queryer
}

// Client wraps a *pgxpool.Pool for documents-table operations.
type Client struct {
	ops
	pool *pgxpool.Pool
}

// New returns a Client backed by pool. It panics if pool is nil.
func New(pool *pgxpool.Pool) *Client {
	if pool == nil {
		panic("db.New: pool must not be nil")
	}
	return &Client{ops: ops{q: pool}, pool: pool}
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

// Close closes the underlying pool. It is safe to call on a nil Client.
func (c *Client) Close() {
	if c == nil || c.pool == nil {
		return
	}
	c.pool.Close()
}

// Tx is a Client's statement set bound to an open transaction. It exists so a
// row flip and its purge enqueue commit or roll back together (ADR-0031).
type Tx struct {
	ops
	tx pgx.Tx
}

// Begin opens a transaction. The caller must call Commit or Rollback; deferring
// Rollback is safe because it is a no-op once the transaction has committed.
func (c *Client) Begin(ctx context.Context) (*Tx, error) {
	tx, err := c.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	return &Tx{ops: ops{q: tx}, tx: tx}, nil
}

// Commit applies the transaction.
func (t *Tx) Commit(ctx context.Context) error {
	if err := t.tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

// Rollback discards the transaction. Safe to call after Commit — an
// already-closed transaction is not an error — so callers can defer it
// unconditionally.
func (t *Tx) Rollback(ctx context.Context) {
	if err := t.tx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		slog.Warn("rollback transaction", "err", err)
	}
}
