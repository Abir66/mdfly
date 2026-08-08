package view

import (
	"context"
	"strings"
	"testing"

	"github.com/Abir66/mdfly/internal/server/manifest"
	"github.com/Abir66/mdfly/internal/server/static"
	"github.com/Abir66/mdfly/internal/server/storage"
)

// noFetchService builds a Service whose Storage can mint CDN URLs but points at a
// dead endpoint: any blob fetch errors, so a render that succeeds proves it never
// touched storage.
func noFetchService() *Service {
	return &Service{
		Static:  static.New(),
		Storage: storage.New(storage.Config{PublicBaseURL: "https://storage.mdfly.dev"}),
	}
}

// TestRender_emptyRootListsDirectory verifies a folder-Document (empty root_path)
// renders a root Directory Listing. With no index file the render path never
// touches storage, so the service needs only the static assets.
func TestRender_emptyRootListsDirectory(t *testing.T) {
	svc := &Service{Static: static.New()}
	mfst := manifest.Manifest{
		RootPath: "",
		FilesByPath: map[string]manifest.ManifestFile{
			"intro.md":      {Hash: "ih", Size: 12},
			"docs/guide.md": {Hash: "gh", Size: 34},
		},
	}

	html, herr := svc.render(context.Background(), "slug123", bundle{Manifest: mfst}, "")
	if herr != nil {
		t.Fatalf("render: %v", herr)
	}
	if !strings.Contains(html, `class="listing"`) {
		t.Errorf("empty-root render missing Directory Listing:\n%s", html)
	}
	if !strings.Contains(html, `href="/slug123/docs"`) {
		t.Errorf("root listing missing folder link:\n%s", html)
	}
	if !strings.Contains(html, `href="/slug123/intro.md"`) {
		t.Errorf("root listing missing file link:\n%s", html)
	}
}

// TestRender_imageInline verifies an image key renders an inline <img> pointing at
// the CDN blob, with no backend fetch (the dead-endpoint storage would error).
func TestRender_imageInline(t *testing.T) {
	svc := noFetchService()
	mfst := manifest.Manifest{
		RootPath: "readme.md",
		FilesByPath: map[string]manifest.ManifestFile{
			"readme.md":    {Hash: "rh", Size: 10},
			"img/logo.png": {Hash: "ph", Size: 2048},
		},
	}

	html, herr := svc.render(context.Background(), "slug123", bundle{Manifest: mfst}, "img/logo.png")
	if herr != nil {
		t.Fatalf("render: %v", herr)
	}
	want := `<img class="asset-image" src="https://storage.mdfly.dev/documents/slug123/ph.png"`
	if !strings.Contains(html, want) {
		t.Errorf("image render missing inline img:\n%s", html)
	}
}

// TestRender_oversizeTextDownloadCard verifies a text file larger than
// PreviewMaxBytes yields a download card sized from the manifest, with no fetch.
func TestRender_oversizeTextDownloadCard(t *testing.T) {
	svc := noFetchService()
	const big = PreviewMaxBytes + 1
	mfst := manifest.Manifest{
		RootPath: "readme.md",
		FilesByPath: map[string]manifest.ManifestFile{
			"readme.md": {Hash: "rh", Size: 10},
			"huge.txt":  {Hash: "th", Size: big},
		},
	}

	html, herr := svc.render(context.Background(), "slug123", bundle{Manifest: mfst}, "huge.txt")
	if herr != nil {
		t.Fatalf("render: %v", herr)
	}
	if !strings.Contains(html, `class="download-card"`) {
		t.Errorf("oversize text render missing download card:\n%s", html)
	}
	if !strings.Contains(html, `href="https://storage.mdfly.dev/documents/slug123/th.txt"`) {
		t.Errorf("download card missing CDN link:\n%s", html)
	}
}
