package filetree

import (
	"reflect"
	"testing"
)

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

// childNames returns the names of n's children in order, with dirs suffixed "/".
func childNames(n *TreeNode) []string {
	out := make([]string, 0, len(n.Children))
	for _, c := range n.Children {
		if c.IsDir {
			out = append(out, c.Name+"/")
		} else {
			out = append(out, c.Name)
		}
	}
	return out
}

// find returns the descendant of root whose Path == path, or nil.
func find(root *TreeNode, path string) *TreeNode {
	if root.Path == path && (root.Name != "" || path == "") {
		return root
	}
	for _, c := range root.Children {
		if c.Path == path {
			return c
		}
		if got := find(c, path); got != nil {
			return got
		}
	}
	return nil
}

func TestBuildTree_nestedStructure(t *testing.T) {
	keys := []string{"index.md", "docs/api/auth.md", "docs/guide.md", "assets/logo.png"}
	root := BuildTree(keys, "docs/api/auth.md")

	if !root.IsDir || root.Name != "" || root.Path != "" {
		t.Fatalf("root = %+v, want synthetic dir with empty Name/Path", root)
	}
	// folders first (alpha), then files (alpha).
	if got, want := childNames(root), []string{"assets/", "docs/", "index.md"}; !reflect.DeepEqual(got, want) {
		t.Errorf("root children = %v, want %v", got, want)
	}

	docs := find(root, "docs")
	api := find(root, "docs/api")
	auth := find(root, "docs/api/auth.md")
	if docs == nil || api == nil || auth == nil {
		t.Fatalf("missing nodes: docs=%v api=%v auth=%v", docs, api, auth)
	}
	if !docs.Open || !api.Open {
		t.Errorf("current file's ancestor dirs must be open: docs.Open=%v api.Open=%v", docs.Open, api.Open)
	}
	if !auth.Current {
		t.Errorf("current file must be marked Current: %+v", auth)
	}
}

func TestBuildTree_ancestorsOnlyOpen(t *testing.T) {
	keys := []string{"index.md", "docs/api/auth.md", "assets/logo.png"}
	root := BuildTree(keys, "docs/api/auth.md")

	assets := find(root, "assets")
	if assets.Open {
		t.Errorf("non-ancestor dir 'assets' must not be open")
	}
	if c := find(root, "index.md"); c.Current {
		t.Errorf("non-current file 'index.md' must not be Current")
	}
}

func TestBuildTree_deeplyNested(t *testing.T) {
	keys := []string{"a/b/c/d/e.md"}
	root := BuildTree(keys, "a/b/c/d/e.md")
	for _, p := range []string{"a", "a/b", "a/b/c", "a/b/c/d"} {
		if n := find(root, p); n == nil || !n.Open {
			t.Errorf("ancestor %q must exist and be open: %+v", p, n)
		}
	}
	if leaf := find(root, "a/b/c/d/e.md"); leaf == nil || !leaf.Current {
		t.Errorf("deep leaf must be Current: %+v", leaf)
	}
}

func TestBuildTree_directoryCurrentKey(t *testing.T) {
	keys := []string{"index.md", "docs/api/auth.md", "docs/guide.md", "assets/logo.png"}
	root := BuildTree(keys, "docs/api")

	api := find(root, "docs/api")
	if api == nil || !api.IsDir {
		t.Fatalf("docs/api dir node must exist: %+v", api)
	}
	if !api.Current || !api.Open {
		t.Errorf("current dir must be Current and Open: Current=%v Open=%v", api.Current, api.Open)
	}
	if docs := find(root, "docs"); !docs.Open {
		t.Errorf("ancestor dir 'docs' must be open")
	}
	if assets := find(root, "assets"); assets.Open {
		t.Errorf("non-ancestor dir 'assets' must not be open")
	}
	if auth := find(root, "docs/api/auth.md"); auth.Current {
		t.Errorf("file under current dir must not be marked Current")
	}
}

func TestBuildTree_singleFileBundle(t *testing.T) {
	root := BuildTree([]string{"only.md"}, "only.md")
	if got, want := childNames(root), []string{"only.md"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("children = %v, want %v", got, want)
	}
	if !root.Children[0].Current {
		t.Errorf("the only file must be Current")
	}
}

func TestListDir(t *testing.T) {
	files := map[string]int64{
		"index.md":         10,
		"readme.md":        20,
		"docs/api/auth.md": 30,
		"docs/guide.md":    40,
		"assets/logo.png":  99,
	}
	tests := []struct {
		name   string
		prefix string
		want   []Entry
	}{
		{
			name:   "root folders-first then files alpha",
			prefix: "",
			want: []Entry{
				{Name: "assets", IsDir: true, Path: "assets"},
				{Name: "docs", IsDir: true, Path: "docs"},
				{Name: "index.md", Size: 10, Path: "index.md"},
				{Name: "readme.md", Size: 20, Path: "readme.md"},
			},
		},
		{
			name:   "immediate children only",
			prefix: "docs",
			want: []Entry{
				{Name: "api", IsDir: true, Path: "docs/api"},
				{Name: "guide.md", Size: 40, Path: "docs/guide.md"},
			},
		},
		{
			name:   "leaf dir lists its file",
			prefix: "docs/api",
			want:   []Entry{{Name: "auth.md", Size: 30, Path: "docs/api/auth.md"}},
		},
		{
			name:   "trailing slash tolerated",
			prefix: "docs/",
			want: []Entry{
				{Name: "api", IsDir: true, Path: "docs/api"},
				{Name: "guide.md", Size: 40, Path: "docs/guide.md"},
			},
		},
		{name: "miss yields no entries", prefix: "nope", want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ListDir(files, tt.prefix)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ListDir(%q) = %+v, want %+v", tt.prefix, got, tt.want)
			}
		})
	}
}

func TestIndexFile(t *testing.T) {
	tests := []struct {
		name    string
		keys    []string
		prefix  string
		wantKey string
		wantOK  bool
	}{
		{"readme precedence over index", []string{"docs/README.md", "docs/index.md"}, "docs", "docs/README.md", true},
		{"case-insensitive readme", []string{"docs/ReadMe.md"}, "docs", "docs/ReadMe.md", true},
		{"index when no readme", []string{"docs/INDEX.md", "docs/x.md"}, "docs", "docs/INDEX.md", true},
		{"root prefix", []string{"README.md", "a.md"}, "", "README.md", true},
		{"immediate children only", []string{"docs/sub/README.md"}, "docs", "", false},
		{"no index miss", []string{"docs/a.md", "docs/b.md"}, "docs", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key, ok := IndexFile(tt.keys, tt.prefix)
			if ok != tt.wantOK || key != tt.wantKey {
				t.Errorf("IndexFile(%q)=(%q,%v), want (%q,%v)", tt.prefix, key, ok, tt.wantKey, tt.wantOK)
			}
		})
	}
}

func TestBreadcrumb_nested(t *testing.T) {
	got := Breadcrumb("docs/api/auth.md")
	want := []Crumb{
		{Name: "", Path: "", IsCurrent: false},
		{Name: "docs", Path: "docs", IsCurrent: false},
		{Name: "api", Path: "docs/api", IsCurrent: false},
		{Name: "auth.md", Path: "docs/api/auth.md", IsCurrent: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Breadcrumb = %+v, want %+v", got, want)
	}
}

func TestBreadcrumb_root(t *testing.T) {
	got := Breadcrumb("")
	want := []Crumb{{Name: "", Path: "", IsCurrent: true}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Breadcrumb(\"\") = %+v, want %+v", got, want)
	}
}

func TestBreadcrumb_aboveRoot(t *testing.T) {
	got := Breadcrumb("../shared/x.md")
	want := []Crumb{
		{Name: "", Path: "", IsCurrent: false},
		{Name: "..", Path: "..", IsCurrent: false},
		{Name: "shared", Path: "../shared", IsCurrent: false},
		{Name: "x.md", Path: "../shared/x.md", IsCurrent: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Breadcrumb above-root = %+v, want %+v", got, want)
	}
}
