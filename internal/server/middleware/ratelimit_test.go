package middleware_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Abir66/mdfly/internal/api"
	"github.com/Abir66/mdfly/internal/server/middleware"
	"github.com/Abir66/mdfly/internal/server/ratelimit"
)

// fakeLimiter records the subject it was asked about and returns a canned
// decision.
type fakeLimiter struct {
	decision ratelimit.Decision
	err      error
	subject  string
}

func (f *fakeLimiter) Allow(_ context.Context, subject string) (ratelimit.Decision, error) {
	f.subject = subject
	return f.decision, f.err
}

func TestRateLimit_deny(t *testing.T) {
	limiter := &fakeLimiter{decision: ratelimit.Decision{
		Limit:      ratelimit.PerMinute,
		RetryAfter: 40 * time.Second,
	}}
	served := false
	handler := middleware.RateLimit(limiter)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		served = true
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/publish/init", nil))

	if served {
		t.Error("handler ran for a denied request")
	}
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusTooManyRequests)
	}
	wantHeaders := map[string]string{
		"X-Ratelimit-Limit":     "10",
		"X-Ratelimit-Remaining": "0",
		"Retry-After":           "40",
	}
	for name, want := range wantHeaders {
		if got := rec.Header().Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}

	var body api.ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Error.Code != api.CodeRateLimited {
		t.Errorf("error code = %q, want %q", body.Error.Code, api.CodeRateLimited)
	}
}

// TestRateLimit_retryAfterRoundsUp keeps Retry-After from advertising a retry the
// limiter would still deny: a fractional wait rounds up to the next second.
func TestRateLimit_retryAfterRoundsUp(t *testing.T) {
	limiter := &fakeLimiter{decision: ratelimit.Decision{
		Limit:      ratelimit.PerMinute,
		RetryAfter: 1500 * time.Millisecond,
	}}
	handler := middleware.RateLimit(limiter)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/publish/init", nil))

	if got := rec.Header().Get("Retry-After"); got != "2" {
		t.Errorf("Retry-After = %q, want %q", got, "2")
	}
}

// TestRateLimit_subject pins key resolution: an Edit Token identifies the editor
// across IPs; anonymous callers are keyed by CF-Connecting-IP, but only when the
// peer is a Cloudflare edge — a header from anywhere else is spoofable and the
// TCP peer is used instead. An IPv6 address arrives with its colons flattened.
func TestRateLimit_subject(t *testing.T) {
	const (
		cloudflareEdge = "173.245.48.5:40000"
		strangerPeer   = "203.0.113.9:40000"
		editToken      = "mftk_abc123"
		claimedIP      = "9.9.9.9"
	)
	tests := []struct {
		name         string
		remoteAddr   string
		authz        string
		connectingIP string
		want         string
	}{
		// The token subject is a digest prefix, not the credential itself.
		{"edit token wins", cloudflareEdge, "Bearer " + editToken, claimedIP, "token:c168a1093fe13850"},
		{"cloudflare peer is trusted", cloudflareEdge, "", claimedIP, "ip:" + claimedIP},
		{"spoofed header is ignored", strangerPeer, "", claimedIP, "ip:203.0.113.9"},
		{"no header falls back to peer", cloudflareEdge, "", "", "ip:173.245.48.5"},
		{"ipv6 peer colons are flattened", "[::1]:40000", "", "", "ip:..1"},
		{"ipv6 claimed address is flattened", cloudflareEdge, "", "2001:db8::5", "ip:2001.db8..5"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			limiter := &fakeLimiter{decision: ratelimit.Decision{Allowed: true}}
			handler := middleware.RateLimit(limiter)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

			req := httptest.NewRequest(http.MethodPost, "/v1/publish/init", nil)
			req.RemoteAddr = tt.remoteAddr
			if tt.authz != "" {
				req.Header.Set("Authorization", tt.authz)
			}
			if tt.connectingIP != "" {
				req.Header.Set("CF-Connecting-IP", tt.connectingIP)
			}
			handler.ServeHTTP(httptest.NewRecorder(), req)

			if limiter.subject != tt.want {
				t.Errorf("subject = %q, want %q", limiter.subject, tt.want)
			}
		})
	}
}

// TestRateLimit_failsOpen pins the fail-open path end to end: a limiter error
// must still reach the handler.
func TestRateLimit_failsOpen(t *testing.T) {
	limiter := &fakeLimiter{err: errors.New("redis unreachable")}
	served := false
	handler := middleware.RateLimit(limiter)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		served = true
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/publish/init", nil))

	if !served {
		t.Error("handler skipped when the limiter failed, want fail-open")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}
