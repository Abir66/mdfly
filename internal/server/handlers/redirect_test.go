package handlers_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Abir66/mdfly/internal/server/handlers"
	"github.com/Abir66/mdfly/internal/server/service/raw"
	"github.com/Abir66/mdfly/internal/server/service/view"
)

// canonServer wires the read-path routes to services with a nil Db and Storage.
// The query redirect runs before any datastore access, so a request that gets
// canonicalized 302s without ever touching the nil backends (S54).
func canonServer(t *testing.T) *httptest.Server {
	t.Helper()
	rawSvc := &raw.Service{}
	viewSvc := &view.Service{}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /raw/{slug}", handlers.Raw(rawSvc))
	mux.HandleFunc("GET /raw/{slug}/{path...}", handlers.RawPath(rawSvc))
	mux.HandleFunc("GET /{slug}", handlers.View(viewSvc))
	mux.HandleFunc("GET /{slug}/{path...}", handlers.ViewPath(viewSvc))

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestQueryCanonicalization_bothReadPaths(t *testing.T) {
	srv := canonServer(t)
	client := noRedirectClient()

	cases := []struct {
		name    string
		path    string
		wantLoc string
	}{
		{"view junk stripped", "/doc?utm=x", "/doc"},
		{"view junk beside up keeps up", "/doc?utm=x&up=1", "/doc?up=1"},
		{"view nested junk stripped", "/doc/a.md?ref=y", "/doc/a.md"},
		{"raw junk stripped", "/raw/doc?utm=x", "/raw/doc"},
		{"raw junk beside up keeps up", "/raw/doc?up=2&utm=x", "/raw/doc?up=2"},
		{"raw nested junk stripped", "/raw/doc/a.md?ref=y", "/raw/doc/a.md"},
		{"view noncanonical up encoding", "/doc?%75p=1", "/doc?up=1"},
		{"raw noncanonical up encoding", "/raw/doc?%75p=1", "/raw/doc?up=1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := client.Get(srv.URL + tc.path)
			if err != nil {
				t.Fatalf("GET %s: %v", tc.path, err)
			}
			resp.Body.Close()

			if resp.StatusCode != http.StatusFound {
				t.Fatalf("status=%d, want 302", resp.StatusCode)
			}
			if loc := resp.Header.Get("Location"); loc != tc.wantLoc {
				t.Errorf("Location=%q, want %q", loc, tc.wantLoc)
			}
			if cc := resp.Header.Get("Cache-Control"); cc != "public, max-age=300, s-maxage=86400" {
				t.Errorf("Cache-Control=%q, want the slug edge policy", cc)
			}
		})
	}
}
