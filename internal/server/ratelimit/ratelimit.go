// Package ratelimit enforces the application-level write-path rate limit
// (ADR-0028): dual fixed windows per subject, counted in a shared store so the
// limit survives restarts. Counting is one INCR+EXPIRE per window against a key
// that embeds the window's time bucket, so each window is a fresh self-resetting
// key and the TTL only garbage-collects dead ones. All of a request's windows are
// counted in a single store round trip.
package ratelimit

import (
	"context"
	"fmt"
	"time"
)

// Per-subject request allowances (ADR-0028).
const (
	PerMinute = 10
	PerHour   = 30
)

// CounterOp is one counter to bump: increment Key and arm TTL on it.
type CounterOp struct {
	Key string
	TTL time.Duration
}

// Counter is the shared counting store: increment every op's key, arm its TTL,
// and return the new values in ops order. Every op must land in one round trip,
// so a request either counts all its windows or none. Implemented by the redis
// client.
type Counter interface {
	IncrementWithTTL(ctx context.Context, ops []CounterOp) ([]int64, error)
}

// Window is one fixed-window allowance: at most Limit requests per Size.
type Window struct {
	Name  string
	Size  time.Duration
	Limit int
}

// DefaultWindows are the ADR-0028 allowances: 10/min and 30/hr per subject.
var DefaultWindows = []Window{
	{Name: "1m", Size: time.Minute, Limit: PerMinute},
	{Name: "1h", Size: time.Hour, Limit: PerHour},
}

// Decision is the outcome of one Allow call. Limit, Remaining, and RetryAfter
// describe the tightest window and feed the X-RateLimit-* / Retry-After headers.
type Decision struct {
	Allowed    bool
	Limit      int
	Remaining  int
	RetryAfter time.Duration
}

// Limiter counts a subject's requests across Windows. Build with New; tests may
// override Now to drive window rollover.
type Limiter struct {
	Counter Counter
	Windows []Window
	Now     func() time.Time
}

// New returns a Limiter counting in store with its own copy of DefaultWindows,
// so tuning one Limiter's Windows cannot disturb another's.
func New(store Counter) *Limiter {
	return &Limiter{
		Counter: store,
		Windows: append([]Window(nil), DefaultWindows...),
		Now:     time.Now,
	}
}

// Allow counts one request for subject in every window and reports whether it
// may proceed. All windows are counted in one store round trip, so a subject
// hammering past its per-minute allowance still spends its hourly one. A store
// failure fails open: the returned Decision allows the request and the error is
// reported for the caller to log.
func (l *Limiter) Allow(ctx context.Context, subject string) (Decision, error) {
	if len(l.Windows) == 0 {
		return Decision{Allowed: true}, nil
	}

	now := l.Now()
	counts, err := l.Counter.IncrementWithTTL(ctx, l.ops(subject, now))
	if err != nil {
		return Decision{Allowed: true}, err
	}
	if len(counts) != len(l.Windows) {
		return Decision{Allowed: true}, fmt.Errorf("ratelimit: got %d counts for %d windows", len(counts), len(l.Windows))
	}
	return l.decide(counts, now), nil
}

// ops names one counter per window, each in the bucket containing now.
func (l *Limiter) ops(subject string, now time.Time) []CounterOp {
	ops := make([]CounterOp, len(l.Windows))
	for i, w := range l.Windows {
		ops[i] = CounterOp{Key: bucketKey(subject, w, now), TTL: w.Size}
	}
	return ops
}

// decide turns per-window counts into a verdict: denied by the exceeded window
// the caller must wait longest on, else allowed carrying the tightest remaining
// allowance. Every window is inspected, so a request over both the minute and
// the hour limit is told to wait out the hour rather than retrying in a minute
// into another 429.
func (l *Limiter) decide(counts []int64, now time.Time) Decision {
	allowed := Decision{Allowed: true, Limit: l.Windows[0].Limit, Remaining: l.Windows[0].Limit}
	var denied Decision

	for i, w := range l.Windows {
		if counts[i] > int64(w.Limit) {
			if retry := untilNextWindow(w, now); retry > denied.RetryAfter {
				denied = Decision{Limit: w.Limit, RetryAfter: retry}
			}
			continue
		}
		if remaining := w.Limit - int(counts[i]); remaining < allowed.Remaining {
			allowed.Limit, allowed.Remaining = w.Limit, remaining
		}
	}
	if denied.RetryAfter > 0 {
		return denied
	}
	return allowed
}

// untilNextWindow is how long until w's current bucket rolls over.
func untilNextWindow(w Window, now time.Time) time.Duration {
	return w.Size - time.Duration(now.Unix()%int64(w.Size.Seconds()))*time.Second
}

// bucketKey names the counter for subject's window containing now. The
// floor(now/size) step makes each window a distinct, self-resetting key.
func bucketKey(subject string, w Window, now time.Time) string {
	step := now.Unix() / int64(w.Size.Seconds())
	return fmt.Sprintf("rl:%s:%s:%d", subject, w.Name, step)
}
