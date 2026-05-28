package publish_test

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/Abir66/mdfly/internal/cli/publish"
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

func hashOf(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func TestForBundle_noAssets(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "hello.md")
	writeFile(t, root, []byte("# Hello\n\nNo images.\n"))

	b, err := publish.ForBundle(root)
	if err != nil {
		t.Fatalf("ForBundle: %v", err)
	}
	if b.RootPath != "hello.md" || len(b.FilesByPath) != 1 {
		t.Fatalf("got RootPath=%q files=%d, want hello.md / 1", b.RootPath, len(b.FilesByPath))
	}
}

func TestForBundle_withImage(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "hello.md")
	img := []byte("\x89PNG\r\n\x1a\nfake")
	writeFile(t, root, []byte("# Hi\n\n![logo](./logo.png)\n"))
	writeFile(t, filepath.Join(dir, "logo.png"), img)

	b, err := publish.ForBundle(root)
	if err != nil {
		t.Fatalf("ForBundle: %v", err)
	}
	if len(b.FilesByPath) != 2 {
		t.Fatalf("FilesByPath len=%d, want 2", len(b.FilesByPath))
	}
	f, ok := b.FilesByPath["logo.png"]
	if !ok {
		t.Fatalf("missing logical path logo.png; have %v", b.FilesByPath)
	}
	if f.Hash != hashOf(img) {
		t.Errorf("logo.png hash=%q, want %q", f.Hash, hashOf(img))
	}
	if f.Size != int64(len(img)) {
		t.Errorf("logo.png size=%d, want %d", f.Size, len(img))
	}
}

func TestForBundle_subdirImage(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "hello.md")
	img := []byte("img")
	writeFile(t, root, []byte("![x](./imgs/logo.png)\n"))
	writeFile(t, filepath.Join(dir, "imgs", "logo.png"), img)

	b, err := publish.ForBundle(root)
	if err != nil {
		t.Fatalf("ForBundle: %v", err)
	}
	if _, ok := b.FilesByPath["imgs/logo.png"]; !ok {
		t.Fatalf("missing logical path imgs/logo.png; have %v", b.FilesByPath)
	}
}

func TestForBundle_skipsExternal(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "hello.md")
	writeFile(t, root, []byte("![x](https://example.com/x.png)\n"))

	b, err := publish.ForBundle(root)
	if err != nil {
		t.Fatalf("ForBundle: %v", err)
	}
	if len(b.FilesByPath) != 1 {
		t.Errorf("external image must not be bundled; files=%v", b.FilesByPath)
	}
}

func TestForBundle_dedupsRepeatedImage(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "hello.md")
	writeFile(t, root, []byte("![a](./logo.png)\n\n![b](./logo.png)\n"))
	writeFile(t, filepath.Join(dir, "logo.png"), []byte("img"))

	b, err := publish.ForBundle(root)
	if err != nil {
		t.Fatalf("ForBundle: %v", err)
	}
	if len(b.FilesByPath) != 2 {
		t.Errorf("repeated image must appear once; files=%v", b.FilesByPath)
	}
}

func TestForBundle_goldenWithImage(t *testing.T) {
	root := filepath.Join("testdata", "bundles", "with-image", "hello.md")

	b, err := publish.ForBundle(root)
	if err != nil {
		t.Fatalf("ForBundle: %v", err)
	}

	wantPaths := map[string]bool{"hello.md": true, "logo.png": true}
	if len(b.FilesByPath) != len(wantPaths) {
		t.Fatalf("FilesByPath=%v, want exactly %v", b.FilesByPath, wantPaths)
	}
	for p := range wantPaths {
		if _, ok := b.FilesByPath[p]; !ok {
			t.Errorf("missing logical path %q; have %v", p, b.FilesByPath)
		}
	}

	imgBytes, err := os.ReadFile(filepath.Join("testdata", "bundles", "with-image", "logo.png"))
	if err != nil {
		t.Fatal(err)
	}
	if got := b.FilesByPath["logo.png"].Hash; got != hashOf(imgBytes) {
		t.Errorf("logo.png hash=%q, want %q", got, hashOf(imgBytes))
	}
}

func TestForBundle_missingAssetErrors(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "hello.md")
	writeFile(t, root, []byte("![x](./does-not-exist.png)\n"))

	if _, err := publish.ForBundle(root); err == nil {
		t.Error("want error for missing referenced asset, got nil")
	}
}
