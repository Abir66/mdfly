package redis_test

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"github.com/Abir66/mdfly/internal/server/ratelimit"
	"github.com/Abir66/mdfly/internal/server/redis"
)

func newClient(t *testing.T, addr string) *redis.Client {
	t.Helper()
	client, err := redis.New(context.Background(), redis.Config{URL: "redis://" + addr})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { client.Close() }) //nolint:errcheck
	return client
}

// TestIncrementWithTTL_pipelinesEveryOp pins the wire contract: every op's key is
// incremented and given its TTL in one pipelined exchange, and the counts come
// back in ops order.
func TestIncrementWithTTL_pipelinesEveryOp(t *testing.T) {
	mr := miniredis.RunT(t)
	client := newClient(t, mr.Addr())

	ops := []ratelimit.CounterOp{
		{Key: "rl:sub:1m:42", TTL: time.Minute},
		{Key: "rl:sub:1h:7", TTL: time.Hour},
	}

	for i := range 3 {
		counts, err := client.IncrementWithTTL(context.Background(), ops)
		if err != nil {
			t.Fatalf("IncrementWithTTL: %v", err)
		}
		want := int64(i + 1)
		if len(counts) != len(ops) {
			t.Fatalf("counts = %v, want %d values", counts, len(ops))
		}
		for j, got := range counts {
			if got != want {
				t.Errorf("counts[%d] = %d, want %d", j, got, want)
			}
		}
	}

	for _, op := range ops {
		if got := mr.TTL(op.Key); got != op.TTL {
			t.Errorf("TTL(%q) = %s, want %s", op.Key, got, op.TTL)
		}
	}
}

// TestIncrementWithTTL_rejectsNonPositiveTTL pins that a bad TTL is caught before
// any command is issued, so no key is ever armed with Redis's "delete now" TTL.
func TestIncrementWithTTL_rejectsNonPositiveTTL(t *testing.T) {
	mr := miniredis.RunT(t)
	client := newClient(t, mr.Addr())

	for _, ttl := range []time.Duration{0, -time.Second} {
		_, err := client.IncrementWithTTL(context.Background(), []ratelimit.CounterOp{{Key: "k", TTL: ttl}})
		if err == nil {
			t.Errorf("IncrementWithTTL(ttl %s) returned no error", ttl)
		}
		if mr.Exists("k") {
			t.Fatalf("key was incremented despite ttl %s", ttl)
		}
	}
}

// TestIncrementWithTTL_errorsWhenUnreachable pins that an outage surfaces as an
// error so the limiter can fail open rather than deny.
func TestIncrementWithTTL_errorsWhenUnreachable(t *testing.T) {
	mr := miniredis.RunT(t)
	client := newClient(t, mr.Addr())
	mr.Close()

	_, err := client.IncrementWithTTL(context.Background(), []ratelimit.CounterOp{{Key: "k", TTL: time.Minute}})
	if err == nil {
		t.Fatal("IncrementWithTTL returned no error against a closed server")
	}
}

// TestNew_rejectsMalformedURL pins that a typo'd REDIS_URL fails the boot instead
// of silently degrading to unthrottled.
func TestNew_rejectsMalformedURL(t *testing.T) {
	if _, err := redis.New(context.Background(), redis.Config{URL: "not-a-url"}); err == nil {
		t.Fatal("New returned no error for a malformed URL")
	}
}
