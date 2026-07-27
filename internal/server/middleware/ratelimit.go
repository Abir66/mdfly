package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/Abir66/mdfly/internal/api"
	"github.com/Abir66/mdfly/internal/server/httpx"
	"github.com/Abir66/mdfly/internal/server/ratelimit"
)

const rateLimitedMessage = "rate limit exceeded; retry later"

// Limiter counts a subject's requests and decides whether one may proceed.
// Implemented by *ratelimit.Limiter.
type Limiter interface {
	Allow(ctx context.Context, subject string) (ratelimit.Decision, error)
}

// RateLimit throttles the wrapped handler per subject (ADR-0028). Wrap the write
// paths only — read paths are edge-cached and never limited. A limiter failure
// is logged and the request allowed through, so an Upstash outage degrades to
// unthrottled rather than blocked.
func RateLimit(limiter Limiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			subject := rateLimitSubject(r)
			decision, err := limiter.Allow(r.Context(), subject)
			if err != nil {
				slog.Warn("rate limiter unavailable, allowing request", "err", err, "path", r.URL.Path)
				next.ServeHTTP(w, r)
				return
			}
			if !decision.Allowed {
				writeRateLimited(w, decision)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func writeRateLimited(w http.ResponseWriter, d ratelimit.Decision) {
	w.Header().Set("X-RateLimit-Limit", strconv.Itoa(d.Limit))
	w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(d.Remaining))
	w.Header().Set("Retry-After", strconv.Itoa(int(d.RetryAfter.Seconds())))
	httpx.WriteError(w, &httpx.Error{
		Status: http.StatusTooManyRequests,
		Code:   api.CodeRateLimited,
		Msg:    rateLimitedMessage,
	})
}
