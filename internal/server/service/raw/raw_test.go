package raw

import (
	"context"
	"net/http"
	"testing"

	"github.com/Abir66/mdfly/internal/server/httpx"
	"github.com/Abir66/mdfly/internal/server/manifest"
	"github.com/Abir66/mdfly/internal/server/storage"
)

// noFetchService builds a Service whose Storage points at a dead endpoint: any
// blob stream open errors, so an outcome that is not a 500 proves the resolution
// path returned before touching storage.
func noFetchService() *Service {
	return &Service{Storage: storage.New(storage.Config{PublicBaseURL: "https://storage.mdfly.dev"})}
}

func mfst() manifest.Manifest {
	return manifest.Manifest{
		RootPath: "index.md",
		FilesByPath: map[string]manifest.ManifestFile{
			"index.md":      {Hash: "ih", Size: 12},
			"docs/guide.md": {Hash: "gh", Size: 34},
		},
	}
}

func TestRawKey(t *testing.T) {
	tests := []struct {
		name    string
		root    string
		rawPath string
		up      string
		wantKey string
		wantOK  bool
	}{
		{"bare addresses root", "index.md", "", "", "index.md", true},
		{"bare with empty root 404s", "", "", "", "", false},
		{"nested key", "index.md", "docs/guide.md", "", "docs/guide.md", true},
		{"up reconstructs above root", "index.md", "shared.md", "2", "../../shared.md", true},
		{"malformed up 404s", "index.md", "x.md", "abc", "", false},
	}
	for _, tt := range tests {
		m := manifest.Manifest{RootPath: tt.root}
		key, ok := rawKey(m, tt.rawPath, tt.up)
		if ok != tt.wantOK || key != tt.wantKey {
			t.Errorf("%s: rawKey=(%q,%v), want (%q,%v)", tt.name, key, ok, tt.wantKey, tt.wantOK)
		}
	}
}

// TestOpen_directoryPrefix404 verifies a directory prefix resolves to a 404 with
// no blob fetch — the dead-endpoint storage would 500 if reached.
func TestOpen_directoryPrefix404(t *testing.T) {
	_, herr := noFetchService().open(context.Background(), "slug123", mfst(), "docs", "")
	assertStatus(t, herr, http.StatusNotFound)
}

// TestOpen_unknownKey404 verifies an unknown file key 404s with no fetch.
func TestOpen_unknownKey404(t *testing.T) {
	_, herr := noFetchService().open(context.Background(), "slug123", mfst(), "nope.md", "")
	assertStatus(t, herr, http.StatusNotFound)
}

// TestOpen_emptyRoot404 verifies a bare request against a folder-Document (empty
// root_path) 404s with no fetch.
func TestOpen_emptyRoot404(t *testing.T) {
	m := manifest.Manifest{FilesByPath: map[string]manifest.ManifestFile{"a.md": {Hash: "ah", Size: 1}}}
	_, herr := noFetchService().open(context.Background(), "slug123", m, "", "")
	assertStatus(t, herr, http.StatusNotFound)
}

// TestOpen_upToAbsentKey404 verifies the ?up=N reconstruction runs — an above-root
// key that is absent 404s with no fetch, proving the prefix changed the lookup.
func TestOpen_upToAbsentKey404(t *testing.T) {
	_, herr := noFetchService().open(context.Background(), "slug123", mfst(), "index.md", "1")
	assertStatus(t, herr, http.StatusNotFound)
}

func assertStatus(t *testing.T, herr *httpx.Error, want int) {
	t.Helper()
	if herr == nil {
		t.Fatalf("expected error status %d, got nil", want)
	}
	if herr.Status != want {
		t.Fatalf("status = %d, want %d", herr.Status, want)
	}
}
