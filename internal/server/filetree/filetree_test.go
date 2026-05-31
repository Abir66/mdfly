package filetree

import "testing"

func TestClassify(t *testing.T) {
	keys := []string{
		"index.md",
		"docs/api/auth.md",
		"docs/guide.md",
		"assets/logo.png",
		"foo",
		"foo/bar.md",
	}
	tests := []struct {
		name     string
		reqPath  string
		wantKind Kind
		wantKey  string
	}{
		{"exact file key", "docs/api/auth.md", File, "docs/api/auth.md"},
		{"exact asset key", "assets/logo.png", File, "assets/logo.png"},
		{"directory prefix", "docs", Dir, "docs"},
		{"nested directory prefix", "docs/api", Dir, "docs/api"},
		{"miss", "does/not/exist.md", NotFound, ""},
		{"miss bare", "nope", NotFound, ""},
		{"file wins over same-stem folder", "foo", File, "foo"},
		{"trailing slash tolerated on file", "docs/api/auth.md/", File, "docs/api/auth.md"},
		{"trailing slash tolerated on dir", "docs/", Dir, "docs"},
		{"leading slash tolerated", "/docs/guide.md", File, "docs/guide.md"},
		{"empty path is root dir", "", Dir, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Classify(keys, tt.reqPath)
			if got.Kind != tt.wantKind {
				t.Fatalf("Classify(%q).Kind = %v, want %v", tt.reqPath, got.Kind, tt.wantKind)
			}
			if got.Key != tt.wantKey {
				t.Errorf("Classify(%q).Key = %q, want %q", tt.reqPath, got.Key, tt.wantKey)
			}
		})
	}
}

func TestClassify_aboveRootKey(t *testing.T) {
	keys := []string{"index.md", "../shared/x.md"}
	got := Classify(keys, "../shared/x.md")
	if got.Kind != File || got.Key != "../shared/x.md" {
		t.Errorf("Classify above-root key = %+v, want File ../shared/x.md", got)
	}
}

func TestClassify_singleFileBundle(t *testing.T) {
	keys := []string{"only.md"}
	if got := Classify(keys, "only.md"); got.Kind != File {
		t.Errorf("single-file bundle exact = %+v, want File", got)
	}
	if got := Classify(keys, ""); got.Kind != Dir {
		t.Errorf("single-file bundle root = %+v, want Dir", got)
	}
}
