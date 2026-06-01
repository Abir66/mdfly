package static_test

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/Abir66/mdfly/internal/server/static"
)

func TestAssets_hashedURLs(t *testing.T) {
	a := static.New()

	cssRe := regexp.MustCompile(`^/_static/app\.[0-9a-f]+\.css$`)
	if got := a.CSSURL(); !cssRe.MatchString(got) {
		t.Errorf("CSSURL() = %q, want /_static/app.<hash>.css", got)
	}
	jsRe := regexp.MustCompile(`^/_static/app\.[0-9a-f]+\.js$`)
	if got := a.JSURL(); !jsRe.MatchString(got) {
		t.Errorf("JSURL() = %q, want /_static/app.<hash>.js", got)
	}
}

func TestAssets_hashIsContentStable(t *testing.T) {
	first, second := static.New().CSSURL(), static.New().CSSURL()
	if first != second {
		t.Errorf("content hash must be deterministic: %q != %q", first, second)
	}
}

func TestHandler_servesCSSWithImmutableCache(t *testing.T) {
	a := static.New()
	srv := httptest.NewServer(a.Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + a.CSSURL())
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/css") {
		t.Errorf("Content-Type = %q, want text/css", ct)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != static.CacheControl {
		t.Errorf("Cache-Control = %q, want %q", cc, static.CacheControl)
	}
}

func TestHandler_servesJS(t *testing.T) {
	a := static.New()
	srv := httptest.NewServer(a.Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + a.JSURL())
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "javascript") {
		t.Errorf("Content-Type = %q, want javascript", ct)
	}
}

func TestHandler_unknownPath404(t *testing.T) {
	a := static.New()
	srv := httptest.NewServer(a.Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/_static/app.deadbeef.css")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown hash status = %d, want 404", resp.StatusCode)
	}
}
