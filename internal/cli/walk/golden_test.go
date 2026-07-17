package walk_test

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/Abir66/mdfly/internal/cli/walk"
)

func TestWalk_goldenRecursiveGating(t *testing.T) {
	root := filepath.Join("testdata", "bundles", "recursive", "index.md")

	noR, err := walk.Walk(root, walk.Options{})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	wantNoR := map[string]bool{"index.md": true, "top.png": true}
	if len(noR.Files) != len(wantNoR) {
		t.Fatalf("without -r want %v, have %v", wantNoR, keys(noR.Files))
	}
	for k := range wantNoR {
		if _, ok := noR.Files[k]; !ok {
			t.Errorf("without -r missing %q; have %v", k, keys(noR.Files))
		}
	}

	withR, err := walk.Walk(root, walk.Options{Recursive: true})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	wantR := []string{"index.md", "top.png", "chapter.md", "chapter.png"}
	if len(withR.Files) != len(wantR) {
		t.Fatalf("with -r want %v, have %v", wantR, keys(withR.Files))
	}
	for _, k := range wantR {
		if _, ok := withR.Files[k]; !ok {
			t.Errorf("with -r missing %q; have %v", k, keys(withR.Files))
		}
	}
}

func TestWalk_goldenCodeRoot(t *testing.T) {
	root := filepath.Join("testdata", "bundles", "code-root", "main.go")
	res, err := walk.Walk(root, walk.Options{Recursive: true})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if res.RootPath != "main.go" || len(res.Files) != 1 {
		t.Fatalf("code root must be single-file; have %v", keys(res.Files))
	}
}

func TestWalk_goldenBinaryRoot(t *testing.T) {
	root := filepath.Join("testdata", "bundles", "binary-root", "report.pdf")
	res, err := walk.Walk(root, walk.Options{Recursive: true})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if res.RootPath != "report.pdf" || len(res.Files) != 1 {
		t.Fatalf("binary root must be single-file; have %v", keys(res.Files))
	}
}

func TestWalk_goldenSymlinkEscape(t *testing.T) {
	root := filepath.Join("testdata", "bundles", "symlink-escape", "proj", "index.md")
	res, err := walk.Walk(root, walk.Options{Recursive: true})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if len(res.Files) != 1 {
		t.Fatalf("escaping symlink must be skipped; have %v", keys(res.Files))
	}
	secret := []byte("TOP-SECRET-DO-NOT-UPLOAD")
	for k, f := range res.Files {
		if bytes.Contains(f.Content, secret) {
			t.Fatalf("secret bytes leaked into %q", k)
		}
	}
}
