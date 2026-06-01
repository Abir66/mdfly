package view

import (
	"context"
	"strings"
	"testing"

	"github.com/Abir66/mdfly/internal/server/manifest"
	"github.com/Abir66/mdfly/internal/server/static"
)

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

	html, herr := svc.render(context.Background(), "slug123", mfst, "")
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
