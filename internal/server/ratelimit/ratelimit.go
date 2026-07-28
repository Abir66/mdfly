// Package ratelimit enforces the application-level write-path rate limit
// (ADR-0028): dual fixed windows per subject, counted in a shared store so the
// limit survives restarts. Counting is one INCR+EXPIRE per window against a key
// that embeds the window's time bucket, so each window is a fresh self-resetting
// key and the TTL only garbage-collects dead ones.
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

// Counter is the shared counting store: increment key and (re)arm its TTL,
// returning the new value. Implemented by the upstash client.
type Counter interface {
	IncrementWithTTL(ctx context.Context, key string, ttl time.Duration) (int64, error)
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
// may proceed. Every window is counted even once one has tripped, so a subject
// hammering past its per-minute allowance still spends its hourly one. A store
// failure fails open: the returned Decision allows the request and the error is
// reported for the caller to log.
func (l *Limiter) Allow(ctx context.Context, subject string) (Decision, error) {
	now := l.Now()
	allowed := Decision{Allowed: true, Limit: l.Windows[0].Limit, Remaining: l.Windows[0].Limit}
	var denied *Decision

	for _, w := range l.Windows {
		count, err := l.Counter.IncrementWithTTL(ctx, bucketKey(subject, w, now), w.Size)
		if err != nil {
			return Decision{Allowed: true}, err
		}
		if count > int64(w.Limit) {
			if denied == nil {
				denied = &Decision{Limit: w.Limit, RetryAfter: untilNextWindow(w, now)}
			}
			continue
		}
		if remaining := w.Limit - int(count); remaining < allowed.Remaining {
			allowed.Limit, allowed.Remaining = w.Limit, remaining
		}
	}

	if denied != nil {
		return *denied, nil
	}
	return allowed, nil
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
