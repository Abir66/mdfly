package view

import (
	"testing"

	"github.com/Abir66/mdfly/internal/server/manifest"
	"github.com/Abir66/mdfly/internal/server/storage"
)

func TestResolveKey(t *testing.T) {
	const root = "/p/root"
	tests := []struct {
		name        string
		referrerDir string
		projectRoot string
		ref         string
		wantKey     string
		wantOK      bool
	}{
		{"relative inside from root", ".", root, "img/logo.png", "img/logo.png", true},
		{"dot-slash stripped", ".", root, "./logo.png", "logo.png", true},
		{"relative from nested referrer", "sub", root, "a.png", "sub/a.png", true},
		{"climb then stay inside", "sub", root, "../sub2/y.png", "sub2/y.png", true},
		{"bounce above then back inside", "sub", root, "../../root/sub3/z.png", "sub3/z.png", true},
		{"genuinely above root", ".", root, "../shared/x.png", "../shared/x.png", true},
		{"absolute inside root", ".", root, "/p/root/sub/a.png", "sub/a.png", true},
		{"absolute above root", ".", root, "/p/shared/x.png", "../shared/x.png", true},
		{"dotdot in the middle", ".", root, "a/../b/c.png", "b/c.png", true},
		{"dotdot mid-path bounces back inside", "sub", root, "deep/../../shared/x.png", "shared/x.png", true},
		{"no project root, absolute unresolvable", ".", "", "/x/y.png", "", false},
		{"no project root, relative logical", ".", "", "./y.png", "y.png", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key, ok := resolveKey(tt.referrerDir, tt.projectRoot, tt.ref)
			if ok != tt.wantOK {
				t.Fatalf("ok=%v, want %v", ok, tt.wantOK)
			}
			if key != tt.wantKey {
				t.Errorf("key=%q, want %q", key, tt.wantKey)
			}
		})
	}
}

func TestSlugPageURL(t *testing.T) {
	tests := []struct {
		key  string
		want string
	}{
		{"y.md", "/s/y.md"},
		{"sub2/a.md", "/s/sub2/a.md"},
		{"../shared/x.md", "/s/shared/x.md?up=1"},
		{"../../a.md", "/s/a.md?up=2"},
	}
	for _, tt := range tests {
		if got := slugPageURL("s", tt.key); got != tt.want {
			t.Errorf("slugPageURL(s,%q)=%q, want %q", tt.key, got, tt.want)
		}
	}
}

func TestSplitRefSuffix(t *testing.T) {
	tests := []struct {
		ref        string
		wantBase   string
		wantSuffix string
	}{
		{"./y.md", "./y.md", ""},
		{"./y.md#intro", "./y.md", "#intro"},
		{"./img.png?v=1", "./img.png", "?v=1"},
		{"./y.md?a=1#frag", "./y.md", "?a=1#frag"},
		{"./y.md#a?b", "./y.md", "#a?b"},
	}
	for _, tt := range tests {
		base, suffix := splitRefSuffix(tt.ref)
		if base != tt.wantBase || suffix != tt.wantSuffix {
			t.Errorf("splitRefSuffix(%q)=(%q,%q), want (%q,%q)", tt.ref, base, suffix, tt.wantBase, tt.wantSuffix)
		}
	}
}

// TestPageResolver_directoryRef checks a link to an in-bundle directory rewrites
// to that directory's listing URL rather than dead-ending.
func TestPageResolver_directoryRef(t *testing.T) {
	const slug = "s"
	mfst := manifest.Manifest{
		RootPath:    "index.md",
		ProjectRoot: "/proj",
		FilesByPath: map[string]manifest.ManifestFile{
			"index.md":      {Hash: "rh", Size: 1},
			"docs/guide.md": {Hash: "gh", Size: 1},
		},
	}
	r2 := storage.New(storage.Config{PublicBaseURL: "https://cdn.test"})
	resolve := pageResolver(r2, slug, mfst, "")

	tests := []struct {
		ref  string
		want string
	}{
		{"docs", "/s/docs"},
		{"./docs", "/s/docs"},
		{"docs/", "/s/docs"},
	}
	for _, tt := range tests {
		got, ok := resolve(tt.ref)
		if !ok || got != tt.want {
			t.Errorf("resolve(%q)=(%q,%v), want (%q,true)", tt.ref, got, ok, tt.want)
		}
	}
}

// TestPageResolver_preservesSuffix checks that a ref's query/fragment survives
// rewriting for both markdown-page and asset targets.
func TestPageResolver_preservesSuffix(t *testing.T) {
	const slug = "s"
	mfst := manifest.Manifest{
		RootPath:    "index.md",
		ProjectRoot: "/proj",
		FilesByPath: map[string]manifest.ManifestFile{
			"index.md":       {Hash: "rh", Size: 1},
			"y.md":           {Hash: "yh", Size: 1},
			"logo.png":       {Hash: "lh", Size: 1},
			"../shared/x.md": {Hash: "xh", Size: 1},
		},
	}
	r2 := storage.New(storage.Config{PublicBaseURL: "https://cdn.test"})
	resolve := pageResolver(r2, slug, mfst, "")

	tests := []struct {
		ref  string
		want string
	}{
		{"./y.md#intro", "/s/y.md#intro"},
		{"./y.md?a=1", "/s/y.md?a=1"},
		{"../shared/x.md?a=1", "/s/shared/x.md?up=1&a=1"},
		{"../shared/x.md#frag", "/s/shared/x.md?up=1#frag"},
		{"./logo.png?v=1", "https://cdn.test/" + storage.BlobKey(slug, "lh", ".png") + "?v=1"},
		{"./logo.png#frag", "https://cdn.test/" + storage.BlobKey(slug, "lh", ".png") + "#frag"},
	}
	for _, tt := range tests {
		got, ok := resolve(tt.ref)
		if !ok || got != tt.want {
			t.Errorf("resolve(%q)=(%q,%v), want (%q,true)", tt.ref, got, ok, tt.want)
		}
	}
}
