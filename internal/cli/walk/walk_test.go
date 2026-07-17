package walk_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/Abir66/mdfly/internal/cli/walk"
)

func writeFile(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}
}

func keys(files map[string]walk.File) []string {
	ks := make([]string, 0, len(files))
	for k := range files {
		ks = append(ks, k)
	}
	return ks
}

func TestWalk_noAssets(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "hello.md")
	writeFile(t, root, []byte("# Hello\n\nNo images.\n"))

	res, err := walk.Walk(root, walk.Options{})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if res.RootPath != "hello.md" || len(res.Files) != 1 {
		t.Fatalf("got RootPath=%q files=%v, want hello.md / 1", res.RootPath, keys(res.Files))
	}
}

func TestWalk_imageAlwaysPulledIn(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "hello.md")
	img := []byte("\x89PNG\r\n\x1a\nfake")
	writeFile(t, root, []byte("# Hi\n\n![logo](./logo.png)\n"))
	writeFile(t, filepath.Join(dir, "logo.png"), img)

	res, err := walk.Walk(root, walk.Options{})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	f, ok := res.Files["logo.png"]
	if !ok {
		t.Fatalf("image asset must be pulled in without -r; have %v", keys(res.Files))
	}
	if !bytes.Equal(f.Content, img) {
		t.Errorf("logo.png content mismatch")
	}
}

func TestWalk_linkedMarkdownGatedByRecursive(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "index.md")
	writeFile(t, root, []byte("[y](./y.md)\n"))
	writeFile(t, filepath.Join(dir, "y.md"), []byte("# Y\n"))

	noR, err := walk.Walk(root, walk.Options{Recursive: false})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if _, ok := noR.Files["y.md"]; ok {
		t.Errorf("linked .md must NOT be followed without -r; have %v", keys(noR.Files))
	}
	if len(noR.Files) != 1 {
		t.Errorf("only root without -r; have %v", keys(noR.Files))
	}

	withR, err := walk.Walk(root, walk.Options{Recursive: true})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if _, ok := withR.Files["y.md"]; !ok {
		t.Errorf("linked .md must be followed under -r; have %v", keys(withR.Files))
	}
}

func TestWalk_transitiveUnderRecursive(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "index.md"), []byte("[y](./y.md)\n"))
	writeFile(t, filepath.Join(dir, "y.md"), []byte("[a](./sub/a.md)\n"))
	writeFile(t, filepath.Join(dir, "sub", "a.md"), []byte("![l](./logo.png)\n"))
	writeFile(t, filepath.Join(dir, "sub", "logo.png"), []byte("img"))

	res, err := walk.Walk(filepath.Join(dir, "index.md"), walk.Options{Recursive: true})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	for _, k := range []string{"index.md", "y.md", "sub/a.md", "sub/logo.png"} {
		if _, ok := res.Files[k]; !ok {
			t.Errorf("missing key %q; have %v", k, keys(res.Files))
		}
	}
}

func TestWalk_nonMarkdownRootNoWalk(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "main.go")
	writeFile(t, root, []byte("package main // ![x](./logo.png) [y](./y.md)\n"))
	writeFile(t, filepath.Join(dir, "logo.png"), []byte("img"))
	writeFile(t, filepath.Join(dir, "y.md"), []byte("# Y\n"))

	res, err := walk.Walk(root, walk.Options{Recursive: true})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if res.RootPath != "main.go" || len(res.Files) != 1 {
		t.Fatalf("non-markdown root must be a single-file bundle; have %v", keys(res.Files))
	}
}

func TestWalk_binaryRootNoWalk(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "report.pdf")
	writeFile(t, root, []byte("%PDF-1.4\x00\x01binary"))

	res, err := walk.Walk(root, walk.Options{Recursive: true})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if res.RootPath != "report.pdf" || len(res.Files) != 1 {
		t.Fatalf("binary root must be a single-file bundle; have %v", keys(res.Files))
	}
}

func TestWalk_symlinkEscapeSkippedBytesNeverRead(t *testing.T) {
	dir := t.TempDir()
	proj := filepath.Join(dir, "proj")
	secret := filepath.Join(dir, "secret.txt")
	secretBytes := []byte("TOP-SECRET-DO-NOT-UPLOAD")
	writeFile(t, secret, secretBytes)
	writeFile(t, filepath.Join(proj, "index.md"), []byte("[l](./leak.txt)\n"))
	if err := os.Symlink(secret, filepath.Join(proj, "leak.txt")); err != nil {
		t.Fatal(err)
	}

	res, err := walk.Walk(filepath.Join(proj, "index.md"), walk.Options{Recursive: true})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if _, ok := res.Files["leak.txt"]; ok {
		t.Errorf("escaping symlink must be skipped; have %v", keys(res.Files))
	}
	for k, f := range res.Files {
		if bytes.Contains(f.Content, secretBytes) {
			t.Fatalf("secret bytes leaked into %q", k)
		}
	}
}

func TestWalk_symlinkInsideRootFollowed(t *testing.T) {
	dir := t.TempDir()
	proj := filepath.Join(dir, "proj")
	writeFile(t, filepath.Join(proj, "real.png"), []byte("img"))
	writeFile(t, filepath.Join(proj, "index.md"), []byte("![l](./link.png)\n"))
	if err := os.Symlink(filepath.Join(proj, "real.png"), filepath.Join(proj, "link.png")); err != nil {
		t.Fatal(err)
	}

	res, err := walk.Walk(filepath.Join(proj, "index.md"), walk.Options{})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if _, ok := res.Files["link.png"]; !ok {
		t.Errorf("in-root symlink must be followed under its in-root key; have %v", keys(res.Files))
	}
}

func TestWalk_dotDotBeyondRootSkipped(t *testing.T) {
	dir := t.TempDir()
	proj := filepath.Join(dir, "proj")
	writeFile(t, filepath.Join(proj, "index.md"), []byte("[x](../shared/x.md)\n"))
	writeFile(t, filepath.Join(dir, "shared", "x.md"), []byte("# X\n"))

	res, err := walk.Walk(filepath.Join(proj, "index.md"), walk.Options{Recursive: true})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if len(res.Files) != 1 {
		t.Errorf("above-root ref must be skipped; have %v", keys(res.Files))
	}
}

func TestWalk_absoluteAndFileURLSkipped(t *testing.T) {
	dir := t.TempDir()
	proj := filepath.Join(dir, "proj")
	outside := filepath.Join(dir, "outside.png")
	writeFile(t, outside, []byte("img"))
	writeFile(t, filepath.Join(proj, "index.md"),
		[]byte("![a]("+outside+")\n![b](file://"+outside+")\n"))

	res, err := walk.Walk(filepath.Join(proj, "index.md"), walk.Options{})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if len(res.Files) != 1 {
		t.Errorf("absolute/file:// refs outside root must be skipped; have %v", keys(res.Files))
	}
}

func TestWalk_httpAndDataLeftUntouched(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "index.md")
	writeFile(t, root, []byte("![a](https://x/y.png)\n![b](data:image/png;base64,AAAA)\n[c](http://x/z.md)\n"))

	res, err := walk.Walk(root, walk.Options{Recursive: true})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if len(res.Files) != 1 {
		t.Errorf("http/https/data refs must not be fetched; have %v", keys(res.Files))
	}
}

func TestWalk_missingAssetWarnsAndContinues(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "index.md")
	writeFile(t, root, []byte("![x](./missing.png)\n"))

	res, err := walk.Walk(root, walk.Options{})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if len(res.Files) != 1 {
		t.Errorf("missing asset must not be bundled; have %v", keys(res.Files))
	}
}

func TestWalk_rootNotFound(t *testing.T) {
	dir := t.TempDir()
	if _, err := walk.Walk(filepath.Join(dir, "nope.md"), walk.Options{}); err == nil {
		t.Fatal("missing root must error")
	}
}

func TestWalkText_cwdAnchoredSynthRoot(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "logo.png"), []byte("img"))

	res, err := walk.WalkText([]byte("# Notes\n\n![l](./logo.png)\n"), dir, walk.Options{})
	if err != nil {
		t.Fatalf("WalkText: %v", err)
	}
	if res.RootPath != "index.md" {
		t.Errorf("synthetic root must be index.md; got %q", res.RootPath)
	}
	rf := res.Files["index.md"]
	if rf.DiskPath != "" {
		t.Errorf("synthetic root must have no disk path; got %q", rf.DiskPath)
	}
	if _, ok := res.Files["logo.png"]; !ok {
		t.Errorf("text-mode asset must anchor at cwd; have %v", keys(res.Files))
	}
}

func TestWalkText_indexCollisionAutoRenames(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "index.md"), []byte("# Real index\n"))

	res, err := walk.WalkText([]byte("[i](./index.md)\n"), dir, walk.Options{Recursive: true})
	if err != nil {
		t.Fatalf("WalkText: %v", err)
	}
	if res.RootPath == "index.md" {
		t.Fatalf("synthetic root must be renamed off a real index.md; got %q", res.RootPath)
	}
	if _, ok := res.Files["index.md"]; !ok {
		t.Errorf("real linked index.md must keep its key; have %v", keys(res.Files))
	}
	if _, ok := res.Files[res.RootPath]; !ok {
		t.Errorf("renamed synthetic root %q must be present", res.RootPath)
	}
}
