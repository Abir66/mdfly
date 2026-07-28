package ratelimit_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Abir66/mdfly/internal/server/ratelimit"
)

// fakeCounter is an in-memory stand-in for the Redis store: it counts per key,
// records the TTL each key was given, and tallies round trips.
type fakeCounter struct {
	counts    map[string]int64
	ttls      map[string]time.Duration
	roundTrip int
	err       error
}

func newFakeCounter() *fakeCounter {
	return &fakeCounter{counts: map[string]int64{}, ttls: map[string]time.Duration{}}
}

func (f *fakeCounter) IncrementWithTTL(_ context.Context, ops []ratelimit.CounterOp) ([]int64, error) {
	f.roundTrip++
	if f.err != nil {
		return nil, f.err
	}
	counts := make([]int64, len(ops))
	for i, op := range ops {
		f.counts[op.Key]++
		f.ttls[op.Key] = op.TTL
		counts[i] = f.counts[op.Key]
	}
	return counts, nil
}

func TestAllow_underLimit(t *testing.T) {
	limiter := ratelimit.New(newFakeCounter())

	got, err := limiter.Allow(context.Background(), "1.2.3.4")
	if err != nil {
		t.Fatalf("Allow: %v", err)
	}
	if !got.Allowed {
		t.Fatal("first request denied, want allowed")
	}
	if got.Limit != ratelimit.PerMinute || got.Remaining != ratelimit.PerMinute-1 {
		t.Errorf("decision = %+v, want limit %d remaining %d", got, ratelimit.PerMinute, ratelimit.PerMinute-1)
	}
}

// TestAllow_oneRoundTripPerRequest pins the batching contract: however many
// windows are configured, one Allow costs the store exactly one round trip.
func TestAllow_oneRoundTripPerRequest(t *testing.T) {
	counter := newFakeCounter()
	limiter := ratelimit.New(counter)

	for range 3 {
		if _, err := limiter.Allow(context.Background(), "subject"); err != nil {
			t.Fatalf("Allow: %v", err)
		}
	}

	if counter.roundTrip != 3 {
		t.Errorf("round trips = %d for 3 requests, want 3", counter.roundTrip)
	}
}

// TestAllow_deniedRequestStillSpendsEveryWindow pins ADR-0028's rule that all
// windows are counted on every request: a request already denied by the
// per-minute window still spends the hourly allowance, so short-circuiting the
// count on the first tripped window would be caught here.
func TestAllow_deniedRequestStillSpendsEveryWindow(t *testing.T) {
	const overspend = 5

	counter := newFakeCounter()
	limiter := ratelimit.New(counter)
	at := time.Date(2026, 7, 27, 10, 30, 0, 0, time.UTC)
	limiter.Now = func() time.Time { return at }

	for range ratelimit.PerMinute + overspend {
		limiter.Allow(context.Background(), "subject") //nolint:errcheck
	}

	want := int64(ratelimit.PerMinute + overspend)
	for key, count := range counter.counts {
		if strings.Contains(key, ":1h:") && count != want {
			t.Errorf("hourly counter %q = %d, want %d", key, count, want)
		}
	}
}

// TestAllow_minuteWindowTrips pins the per-minute allowance: the 11th request
// inside one minute is denied and told how long to wait for the next window.
func TestAllow_minuteWindowTrips(t *testing.T) {
	limiter := ratelimit.New(newFakeCounter())
	at := time.Date(2026, 7, 27, 10, 30, 20, 0, time.UTC)
	limiter.Now = func() time.Time { return at }

	for i := range ratelimit.PerMinute {
		got, _ := limiter.Allow(context.Background(), "subject")
		if !got.Allowed {
			t.Fatalf("request %d denied, want allowed", i+1)
		}
	}

	got, err := limiter.Allow(context.Background(), "subject")
	if err != nil {
		t.Fatalf("Allow: %v", err)
	}
	if got.Allowed {
		t.Fatal("request past the per-minute allowance was allowed")
	}
	if got.Limit != ratelimit.PerMinute || got.Remaining != 0 {
		t.Errorf("decision = %+v, want limit %d remaining 0", got, ratelimit.PerMinute)
	}
	if want := 40 * time.Second; got.RetryAfter != want {
		t.Errorf("RetryAfter = %s, want %s (to the next minute bucket)", got.RetryAfter, want)
	}
}

// TestAllow_hourWindowTripsIndependently spends the hourly allowance a few
// requests per minute, so the per-minute window never trips: the 31st request
// is still denied, by the hour window, and told to wait it out.
func TestAllow_hourWindowTripsIndependently(t *testing.T) {
	limiter := ratelimit.New(newFakeCounter())
	at := time.Date(2026, 7, 27, 10, 0, 0, 0, time.UTC)
	limiter.Now = func() time.Time { return at }

	for i := range ratelimit.PerHour {
		got, _ := limiter.Allow(context.Background(), "subject")
		if !got.Allowed {
			t.Fatalf("request %d denied, want allowed", i+1)
		}
		at = at.Add(time.Minute)
	}

	got, _ := limiter.Allow(context.Background(), "subject")
	if got.Allowed {
		t.Fatal("request past the hourly allowance was allowed")
	}
	if got.Limit != ratelimit.PerHour || got.Remaining != 0 {
		t.Errorf("decision = %+v, want limit %d remaining 0", got, ratelimit.PerHour)
	}
	if want := 30 * time.Minute; got.RetryAfter != want {
		t.Errorf("RetryAfter = %s, want %s (to the next hour bucket)", got.RetryAfter, want)
	}
}

// TestAllow_windowRollover walks the clock past the minute bucket boundary: the
// next window is a different key, so the subject starts from a full allowance.
func TestAllow_windowRollover(t *testing.T) {
	counter := newFakeCounter()
	limiter := ratelimit.New(counter)
	at := time.Date(2026, 7, 27, 10, 30, 50, 0, time.UTC)
	limiter.Now = func() time.Time { return at }

	for range ratelimit.PerMinute + 1 {
		limiter.Allow(context.Background(), "subject") //nolint:errcheck
	}

	at = at.Add(time.Minute)
	got, err := limiter.Allow(context.Background(), "subject")
	if err != nil {
		t.Fatalf("Allow: %v", err)
	}
	if !got.Allowed {
		t.Fatal("first request of a new minute window denied, want allowed")
	}
	if ttl := counter.ttls; len(ttl) == 0 {
		t.Fatal("no TTL armed on any counter key")
	}
	for key, ttl := range counter.ttls {
		if ttl <= 0 {
			t.Errorf("key %q armed with TTL %s, want positive", key, ttl)
		}
	}
}

// TestAllow_failsOpen pins ADR-0028's fail-open rule: a store outage must
// degrade to unthrottled, never to blocked, and surface the error for logging.
func TestAllow_failsOpen(t *testing.T) {
	counter := newFakeCounter()
	counter.err = errors.New("redis unreachable")
	limiter := ratelimit.New(counter)

	got, err := limiter.Allow(context.Background(), "subject")
	if err == nil {
		t.Fatal("Allow returned no error on a store failure")
	}
	if !got.Allowed {
		t.Error("request denied on a store failure, want fail-open")
	}
}
