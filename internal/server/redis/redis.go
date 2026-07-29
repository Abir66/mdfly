// Package redis is the Redis-backed counter store for the write-path rate
// limiter (ADR-0013). It holds a persistent RESP connection pool, so a request
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

// incrementScript counts every key in one atomic server-side step. It validates
// all keys first and only then mutates, because Redis does not roll back a
// script's (or a transaction's) earlier writes when a later command fails: the
// limiter's all-or-nothing contract has to be won by checking before touching.
var incrementScript = goredis.NewScript(`
for i = 1, #KEYS do
  local ttl = tonumber(ARGV[i])
  if not ttl or ttl < 1 then
    return redis.error_reply('invalid ttl for key ' .. KEYS[i])
  end
  local current = redis.call('GET', KEYS[i])
  if current and not string.match(current, '^%-?%d+$') then
    return redis.error_reply('non-integer counter at key ' .. KEYS[i])
  end
end
local counts = {}
for i = 1, #KEYS do
  counts[i] = redis.call('INCR', KEYS[i])
  redis.call('EXPIRE', KEYS[i], ARGV[i])
end
return counts
`)

// IncrementWithTTL increments every op's key and arms its expiry in one atomic
// round trip, returning the incremented values in ops order. Atomicity is
// load-bearing: the limiter needs every window counted or none, so one window
// can never go unspent because a later one held a bad value. A rejected TTL or
// a non-counter key mutates nothing, and a failed execution or an unexpected
// reply is reported as an error — the counters are then indeterminate, never
// assumed spent. The EXPIRE only garbage-collects the key — callers own window
// resets by varying the key, so a lost EXPIRE leaks a key but never blocks a
// subject.
func (c *Client) IncrementWithTTL(ctx context.Context, ops []ratelimit.CounterOp) ([]int64, error) {
	if len(ops) == 0 {
		return nil, nil
	}

	keys := make([]string, len(ops))
	ttls := make([]any, len(ops))
	for i, op := range ops {
		if op.TTL < time.Second {
			return nil, fmt.Errorf("redis: ttl %s below one second for key %q", op.TTL, op.Key)
		}
		keys[i] = op.Key
		ttls[i] = int64(op.TTL.Seconds())
	}

	reply, err := incrementScript.Run(ctx, c.rdb, keys, ttls...).Result()
	if err != nil {
		return nil, fmt.Errorf("redis increment: %w", err)
	}
	return replyCounts(reply, ops)
}

// replyCounts reads the script's reply as one count per op. Anything else means
// the outcome is unknown — the counters may or may not have moved — so it is an
// error rather than a partial success.
func replyCounts(reply any, ops []ratelimit.CounterOp) ([]int64, error) {
	values, ok := reply.([]any)
	if !ok || len(values) != len(ops) {
		return nil, fmt.Errorf("redis increment: indeterminate reply %T for %d ops", reply, len(ops))
	}
	counts := make([]int64, len(ops))
	for i, value := range values {
		count, ok := value.(int64)
		if !ok {
			return nil, fmt.Errorf("redis increment: indeterminate count %T for key %q", value, ops[i].Key)
		}
		counts[i] = count
	}
	return counts, nil
}

// Close releases the connection pool.
func (c *Client) Close() error {
	return c.rdb.Close()
}
