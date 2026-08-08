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

// pngHeader is a real PNG signature: its first byte is a NUL-adjacent magic and
// the block sniffs binary, so it proves the whitelist wins over the sniff.
var pngHeader = []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00}

func TestClassify(t *testing.T) {
	tests := []struct {
		name       string
		key        string
		peek       []byte
		wantType   string
		wantAttach bool
	}{
		{"png inline", "img.png", pngHeader, "image/png", false},
		{"jpg inline", "a.jpg", pngHeader, "image/jpeg", false},
		{"jpeg inline", "a.jpeg", pngHeader, "image/jpeg", false},
		{"gif inline", "a.gif", pngHeader, "image/gif", false},
		{"webp inline", "a.webp", pngHeader, "image/webp", false},
		{"mp4 inline", "a.mp4", pngHeader, "video/mp4", false},
		{"webm inline", "a.webm", pngHeader, "video/webm", false},
		{"mp3 inline", "a.mp3", pngHeader, "audio/mpeg", false},
		{"wav inline", "a.wav", pngHeader, "audio/wav", false},

		{"pdf is attachment", "doc.pdf", []byte("%PDF-1.7\n"), "application/pdf", true},

		{"binary under unknown ext downloads", "data.bin", []byte{0x00, 0x01, 0xff}, "application/octet-stream", true},
		{"binary under text ext downloads", "notes.md", []byte{0x00, 0x01, 0xff}, "application/octet-stream", true},

		{"text under unknown ext is text/plain", "data.xyz", []byte("hello world"), "text/plain; charset=utf-8", false},
		{"extensionless text is text/plain", "LICENSE", []byte("MIT License"), "text/plain; charset=utf-8", false},
		{"extensionless binary downloads", "blob", []byte{0x00, 0xff}, "application/octet-stream", true},
		{"html is text/plain, never executed", "page.html", []byte("<html></html>"), "text/plain; charset=utf-8", false},

		// .svg is deliberately text/plain, NOT image/svg+xml: served at a top-level
		// URL it would execute embedded script. It is absent from the whitelist on
		// purpose. Do not "fix" this by adding it — the view resolver's imageExts
		// lists .svg because <img> never runs script; the two maps must not merge.
		{"svg is text/plain, not image/svg+xml", "logo.svg", []byte("<svg></svg>"), "text/plain; charset=utf-8", false},

		{"uppercase extension is case-insensitive", "IMG.PNG", pngHeader, "image/png", false},
		{"uppercase pdf is case-insensitive", "DOC.PDF", []byte("%PDF-1.7\n"), "application/pdf", true},
	}
	for _, tt := range tests {
		gotType, gotAttach := classify(tt.key, tt.peek)
		if gotType != tt.wantType || gotAttach != tt.wantAttach {
			t.Errorf("%s: classify=(%q,%v), want (%q,%v)", tt.name, gotType, gotAttach, tt.wantType, tt.wantAttach)
		}
	}
}

// TestAttachmentDisposition checks the download filename is RFC 6266-encoded, so
// a basename with a quote, space, or non-ASCII byte cannot malform the header.
func TestAttachmentDisposition(t *testing.T) {
	tests := []struct {
		key  string
		want string
	}{
		{"data.bin", "attachment; filename=data.bin"},
		{"docs/data.bin", "attachment; filename=data.bin"},
		{`ev"il.bin`, `attachment; filename="ev\"il.bin"`},
		{"a b.png", `attachment; filename="a b.png"`},
		{"unïcode.bin", "attachment; filename*=utf-8''un%C3%AFcode.bin"},
		{"crlf\r\nx.bin", "attachment; filename*=utf-8''crlf%0D%0Ax.bin"},
	}
	for _, tt := range tests {
		if got := attachmentDisposition(tt.key); got != tt.want {
			t.Errorf("attachmentDisposition(%q)=%q, want %q", tt.key, got, tt.want)
		}
	}
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
