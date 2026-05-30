package publish

import (
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestBackoffFor_growsAndJitters(t *testing.T) {
	// Base sequence is base*multiplier^(attempt-1): 200ms, 600ms, 1800ms.
	// Jitter must keep each delay within [base, base+base*jitterFraction).
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		base := time.Duration(float64(backoffBase) * pow(backoffMultiplier, attempt-1))
		max := base + time.Duration(float64(base)*backoffJitterFraction)
		seen := map[time.Duration]bool{}
		for i := 0; i < 16; i++ {
			d := backoffFor(attempt)
			if d < base || d >= max {
				t.Fatalf("attempt %d: backoff %v out of [%v,%v)", attempt, d, base, max)
			}
			seen[d] = true
		}
		if len(seen) == 1 {
			t.Errorf("attempt %d: backoff never jittered (always %v)", attempt, base)
		}
	}
}

func pow(base float64, exp int) float64 {
	out := 1.0
	for i := 0; i < exp; i++ {
		out *= base
	}
	return out
}

func TestIsRetryable(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"5xx", &httpStatusError{code: http.StatusServiceUnavailable}, true},
		{"500", &httpStatusError{code: http.StatusInternalServerError}, true},
		{"4xx not retryable", &httpStatusError{code: http.StatusUnprocessableEntity}, false},
		{"404 not retryable", &httpStatusError{code: http.StatusNotFound}, false},
		{"transient network", &transientError{err: errors.New("EOF")}, true},
		{"plain error not retryable", errors.New("boom"), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isRetryable(c.err); got != c.want {
				t.Errorf("isRetryable(%v)=%v, want %v", c.err, got, c.want)
			}
		})
	}
}
