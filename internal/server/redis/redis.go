// Package redis is the Redis-backed counter store for the write-path rate
// limiter (ADR-0028). It holds a persistent RESP connection pool, so a request
// spends one round trip on an already-open socket rather than a fresh HTTP
// exchange. The URL selects the deployment — Upstash's TLS endpoint or a local
// instance — so nothing here is vendor-specific.
package redis

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/Abir66/mdfly/internal/server/ratelimit"
)

// Connection pool and timeout bounds. The limiter fails open, so a slow or
// unreachable Redis must cost the write path a bounded delay, never a hung
// request. The pool is capped well under any provider's concurrent-connection
// ceiling, and idle sockets are kept warm so a request rarely pays a dial.
const (
	dialTimeout    = 2 * time.Second
	commandTimeout = 2 * time.Second
	poolSize       = 10
	minIdleConns   = 2
	pingTimeout    = 2 * time.Second
)

// Config holds the Redis connection URL: `rediss://` for a TLS endpoint,
// `redis://` for plaintext.
type Config struct {
	URL string
}

// Client counts against Redis over a pooled connection. Build with New, release
// with Close.
type Client struct {
	rdb *goredis.Client
}

// New returns a Client for cfg and probes the connection. A malformed URL is a
// configuration error and fails the boot; an unreachable server only warns,
// because the limiter fails open and must not block startup.
func New(ctx context.Context, cfg Config) (*Client, error) {
	opts, err := goredis.ParseURL(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}
	opts.DialTimeout = dialTimeout
	opts.ReadTimeout = commandTimeout
	opts.WriteTimeout = commandTimeout
	opts.PoolSize = poolSize
	opts.MinIdleConns = minIdleConns

	client := &Client{rdb: goredis.NewClient(opts)}
	pingCtx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()
	if err := client.rdb.Ping(pingCtx).Err(); err != nil {
		slog.Warn("redis unreachable at boot, rate limiter will fail open", "err", err)
	}
	return client, nil
}

// IncrementWithTTL increments every op's key and arms its expiry, all in one
// pipelined round trip, returning the incremented values in ops order. Batching
// is load-bearing: the limiter needs every window counted or none, so one
// window can never go unspent because a later one failed. The EXPIRE only
// garbage-collects the key — callers own window resets by varying the key, so a
// lost EXPIRE leaks a key but never blocks a subject.
func (c *Client) IncrementWithTTL(ctx context.Context, ops []ratelimit.CounterOp) ([]int64, error) {
	if len(ops) == 0 {
		return nil, nil
	}

	pipe := c.rdb.Pipeline()
	incrs := make([]*goredis.IntCmd, len(ops))
	for i, op := range ops {
		if op.TTL <= 0 {
			return nil, fmt.Errorf("redis: non-positive ttl %s for key %q", op.TTL, op.Key)
		}
		incrs[i] = pipe.Incr(ctx, op.Key)
		pipe.Expire(ctx, op.Key, op.TTL)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, fmt.Errorf("redis pipeline: %w", err)
	}

	counts := make([]int64, len(ops))
	for i, incr := range incrs {
		count, err := incr.Result()
		if err != nil {
			return nil, fmt.Errorf("redis incr %q: %w", ops[i].Key, err)
		}
		counts[i] = count
	}
	return counts, nil
}

// Close releases the connection pool.
func (c *Client) Close() error {
	return c.rdb.Close()
}
