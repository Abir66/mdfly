package publish

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"time"
)

// Retry policy for init/commit per ADR-0013: bounded attempts with exponential
// backoff + jitter. Retries cover transient failures (5xx, connection timeout,
// DNS failure, EOF mid-response); 4xx are user errors and never retried. The
// caller reuses one idempotency_key across all attempts of a single publish, so
// retries are safe against the server's natural-terminal-state idempotency.
const (
	maxAttempts           = 3
	backoffBase           = 200 * time.Millisecond
	backoffMultiplier     = 3
	backoffJitterFraction = 0.5
)

// httpStatusError carries a non-2xx response so the retry layer can decide
// whether the status is transient.
type httpStatusError struct {
	code int
	msg  string
}

func (e *httpStatusError) Error() string { return e.msg }

// transientError marks a network-level failure (timeout, DNS, EOF mid-response)
// worth retrying.
type transientError struct{ err error }

func (e *transientError) Error() string { return e.err.Error() }
func (e *transientError) Unwrap() error { return e.err }

// isRetryable reports whether err is a transient condition worth another attempt.
func isRetryable(err error) bool {
	var se *httpStatusError
	if errors.As(err, &se) {
		return se.code >= 500
	}
	var te *transientError
	return errors.As(err, &te)
}

// backoffFor returns the delay before the given 1-based attempt's retry, applying
// exponential growth and additive jitter of up to backoffJitterFraction.
func backoffFor(attempt int) time.Duration {
	base := float64(backoffBase) * math.Pow(backoffMultiplier, float64(attempt-1))
	jitter := base * backoffJitterFraction * rand.Float64()
	return time.Duration(base + jitter)
}

// withRetry runs fn up to maxAttempts times, sleeping with backoff between
// attempts while the error is retryable. Non-retryable errors abort immediately.
func withRetry[T any](ctx context.Context, fn func() (T, error)) (T, error) {
	var result T
	var err error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		result, err = fn()
		if err == nil || !isRetryable(err) {
			return result, err
		}
		if attempt == maxAttempts {
			break
		}
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		case <-time.After(backoffFor(attempt)):
		}
	}
	return result, fmt.Errorf("after %d attempts: %w", maxAttempts, err)
}
