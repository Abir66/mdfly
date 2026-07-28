package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Abir66/mdfly/internal/server/ratelimit"
	"github.com/Abir66/mdfly/internal/server/static"
)

// denyAll is a limiter that refuses every request.
type denyAll struct{}

func (denyAll) Allow(context.Context, string) (ratelimit.Decision, error) {
	return ratelimit.Decision{Limit: ratelimit.PerMinute}, nil
}

// TestRoutes_rateLimitCoversWritePathsOnly pins where the limiter is mounted:
// the three write endpoints, and nothing that serves reads.
func TestRoutes_rateLimitCoversWritePathsOnly(t *testing.T) {
	app := &App{static: static.New(), limiter: denyAll{}}
	handler := app.routes()

	limited := []struct{ method, path string }{
		{http.MethodPost, "/v1/publish/init"},
		{http.MethodPost, "/v1/update/init"},
		{http.MethodDelete, "/v1/documents/abc123"},
	}
	for _, route := range limited {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(route.method, route.path, nil))
		if rec.Code != http.StatusTooManyRequests {
			t.Errorf("%s %s: status = %d, want %d", route.method, route.path, rec.Code, http.StatusTooManyRequests)
		}
	}

	unlimited := []string{"/healthz", "/robots.txt"}
	for _, path := range unlimited {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s: status = %d, want %d", path, rec.Code, http.StatusOK)
		}
	}
}
