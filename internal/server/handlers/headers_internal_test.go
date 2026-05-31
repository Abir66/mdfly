package handlers

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWriteNotFound_minimalHTMLAndHeaders(t *testing.T) {
	rec := httptest.NewRecorder()
	writeNotFound(rec)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d, want 404", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("Content-Type=%q, want text/html; charset=utf-8", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "public, max-age=60" {
		t.Errorf("Cache-Control=%q, want public, max-age=60", cc)
	}
	if rt := rec.Header().Get("X-Robots-Tag"); rt != "noindex" {
		t.Errorf("X-Robots-Tag=%q, want noindex", rt)
	}
	body, _ := io.ReadAll(rec.Body)
	if string(body) != notFoundHTML {
		t.Errorf("body=%q, want %q", body, notFoundHTML)
	}
}

func TestCacheControlValues(t *testing.T) {
	if got := slugCacheControl(); got != "public, max-age=300, s-maxage=86400" {
		t.Errorf("slugCacheControl()=%q", got)
	}
	if got := notFoundCacheControl(); got != "public, max-age=60" {
		t.Errorf("notFoundCacheControl()=%q", got)
	}
}
