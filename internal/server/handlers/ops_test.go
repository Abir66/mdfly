package handlers_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Abir66/mdfly/internal/server/handlers"
)

func TestRobots_servesBlanketDisallow(t *testing.T) {
	rec := httptest.NewRecorder()
	handlers.Robots()(rec, httptest.NewRequest(http.MethodGet, "/robots.txt", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/plain; charset=utf-8" {
		t.Errorf("Content-Type=%q, want text/plain; charset=utf-8", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "" {
		t.Errorf("Cache-Control=%q, want unset on robots.txt", cc)
	}
	body, _ := io.ReadAll(rec.Body)
	want := "User-agent: *\nDisallow: /\n"
	if string(body) != want {
		t.Errorf("body=%q, want %q", body, want)
	}
}

func TestHealthz_reportsLivenessWithoutDeps(t *testing.T) {
	rec := httptest.NewRecorder()
	handlers.Healthz()(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/plain; charset=utf-8" {
		t.Errorf("Content-Type=%q, want text/plain; charset=utf-8", ct)
	}
	body, _ := io.ReadAll(rec.Body)
	if string(body) != "ok" {
		t.Errorf("body=%q, want %q", body, "ok")
	}
}
