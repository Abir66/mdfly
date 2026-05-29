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

func TestForBundle_goldenMultiMd(t *testing.T) {
	root := filepath.Join("testdata", "bundles", "multi-md", "index.md")

	b, err := publish.ForBundle(root)
	if err != nil {
		t.Fatalf("ForBundle: %v", err)
	}
	if b.RootPath != "index.md" {
		t.Errorf("RootPath=%q, want index.md", b.RootPath)
	}

	wantKeys := []string{"index.md", "y.md", "sub/another.md", "sub/a.md", "sub2/a.md", "logo.png"}
	if len(b.FilesByPath) != len(wantKeys) {
		t.Fatalf("FilesByPath=%v, want exactly %v", b.FilesByPath, wantKeys)
	}
	for _, k := range wantKeys {
		if _, ok := b.FilesByPath[k]; !ok {
			t.Errorf("missing key %q; have %v", k, b.FilesByPath)
		}
	}

	indexBytes, err := os.ReadFile(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := b.FilesByPath["index.md"].Hash; got != hashOf(indexBytes) {
		t.Errorf("index.md not stored byte-identically: hash=%q want %q", got, hashOf(indexBytes))
	}
}

func TestForBundle_missingAssetWarnsAndContinues(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "hello.md")
	writeFile(t, root, []byte("![x](./does-not-exist.png)\n"))

	b, err := publish.ForBundle(root)
	if err != nil {
		t.Fatalf("ForBundle: %v", err)
	}
	if len(b.FilesByPath) != 1 {
		t.Errorf("missing asset must not be bundled; files=%v", b.FilesByPath)
	}
}

func TestForBundle_projectRootAndRootPath(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	root := filepath.Join(sub, "index.md")
	writeFile(t, root, []byte("# Hi\n"))

	b, err := publish.ForBundle(root)
	if err != nil {
		t.Fatalf("ForBundle: %v", err)
	}
	if b.RootPath != "index.md" {
		t.Errorf("RootPath=%q, want index.md", b.RootPath)
	}
	if b.ProjectRoot != sub {
		t.Errorf("ProjectRoot=%q, want %q", b.ProjectRoot, sub)
	}
}

func TestForBundle_transitiveMarkdown(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "index.md"), []byte("[y](./y.md)\n"))
	writeFile(t, filepath.Join(dir, "y.md"), []byte("[a](./sub2/a.md)\n"))
	writeFile(t, filepath.Join(dir, "sub2", "a.md"), []byte("# A\n"))

	b, err := publish.ForBundle(filepath.Join(dir, "index.md"))
	if err != nil {
		t.Fatalf("ForBundle: %v", err)
	}
	for _, key := range []string{"index.md", "y.md", "sub2/a.md"} {
		if _, ok := b.FilesByPath[key]; !ok {
			t.Errorf("missing key %q; have %v", key, b.FilesByPath)
		}
	}
	if len(b.FilesByPath) != 3 {
		t.Errorf("want 3 files, got %v", b.FilesByPath)
	}
}

func TestForBundle_cycle(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.md"), []byte("[b](./b.md)\n"))
	writeFile(t, filepath.Join(dir, "b.md"), []byte("[a](./a.md)\n"))

	b, err := publish.ForBundle(filepath.Join(dir, "a.md"))
	if err != nil {
		t.Fatalf("ForBundle: %v", err)
	}
	if len(b.FilesByPath) != 2 {
		t.Errorf("cycle must visit each file once; got %v", b.FilesByPath)
	}
}

func TestForBundle_baseDirAwareKey(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "index.md"), []byte("[s](./sub/another.md)\n"))
	writeFile(t, filepath.Join(dir, "sub", "another.md"), []byte("[a](./a.md)\n"))
	writeFile(t, filepath.Join(dir, "sub", "a.md"), []byte("# A\n"))

	b, err := publish.ForBundle(filepath.Join(dir, "index.md"))
	if err != nil {
		t.Fatalf("ForBundle: %v", err)
	}
	if _, ok := b.FilesByPath["sub/a.md"]; !ok {
		t.Errorf("./a.md from sub/another.md must key as sub/a.md; have %v", b.FilesByPath)
	}
}

func TestForBundle_inTreeDotDotFolds(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "index.md"), []byte("[z](./sub2/z.md)\n"))
	writeFile(t, filepath.Join(dir, "sub2", "z.md"), []byte("[a](../sub/a.md)\n"))
	writeFile(t, filepath.Join(dir, "sub", "a.md"), []byte("# A\n"))

	b, err := publish.ForBundle(filepath.Join(dir, "index.md"))
	if err != nil {
		t.Fatalf("ForBundle: %v", err)
	}
	if _, ok := b.FilesByPath["sub/a.md"]; !ok {
		t.Errorf("in-tree ../sub/a.md must fold to sub/a.md; have %v", b.FilesByPath)
	}
}

func TestForBundle_aboveRootSkipped(t *testing.T) {
	dir := t.TempDir()
	proj := filepath.Join(dir, "proj")
	writeFile(t, filepath.Join(proj, "index.md"), []byte("[x](../shared/x.md)\n"))
	writeFile(t, filepath.Join(dir, "shared", "x.md"), []byte("# X\n"))

	b, err := publish.ForBundle(filepath.Join(proj, "index.md"))
	if err != nil {
		t.Fatalf("ForBundle: %v", err)
	}
	if _, ok := b.FilesByPath["../shared/x.md"]; ok {
		t.Errorf("above-root ref must be skipped, not bundled; have %v", b.FilesByPath)
	}
	if len(b.FilesByPath) != 1 {
		t.Errorf("only the root file must be bundled; have %v", b.FilesByPath)
	}
}

func TestForBundle_bounceInUploaded(t *testing.T) {
	// index.md climbs above the project root then back into it; the target
	// ultimately resolves inside, so it must be uploaded under its in-root key.
	dir := t.TempDir()
	proj := filepath.Join(dir, "proj")
	writeFile(t, filepath.Join(proj, "sub", "x.md"), []byte("[z](../../proj/sub3/z.md)\n"))
	writeFile(t, filepath.Join(proj, "sub3", "z.md"), []byte("# Z\n"))
	writeFile(t, filepath.Join(proj, "index.md"), []byte("[x](./sub/x.md)\n"))

	b, err := publish.ForBundle(filepath.Join(proj, "index.md"))
	if err != nil {
		t.Fatalf("ForBundle: %v", err)
	}
	if _, ok := b.FilesByPath["sub3/z.md"]; !ok {
		t.Errorf("bounce-in ref must key as sub3/z.md; have %v", b.FilesByPath)
	}
}

func TestForBundle_diamondCountsOnce(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "index.md"), []byte("[a](./a.md)\n[b](./b.md)\n"))
	writeFile(t, filepath.Join(dir, "a.md"), []byte("[s](./shared.md)\n"))
	writeFile(t, filepath.Join(dir, "b.md"), []byte("[s](./shared.md)\n"))
	writeFile(t, filepath.Join(dir, "shared.md"), []byte("# S\n"))

	b, err := publish.ForBundle(filepath.Join(dir, "index.md"))
	if err != nil {
		t.Fatalf("ForBundle: %v", err)
	}
	if len(b.FilesByPath) != 4 {
		t.Errorf("diamond shared target must count once; got %v", b.FilesByPath)
	}
}

func TestForBundle_absolutePathRefKeyedByRel(t *testing.T) {
	dir := t.TempDir()
	proj := filepath.Join(dir, "proj")
	logo := filepath.Join(proj, "logo.png")
	writeFile(t, logo, []byte("img"))
	writeFile(t, filepath.Join(proj, "index.md"), []byte("![l]("+logo+")\n"))

	b, err := publish.ForBundle(filepath.Join(proj, "index.md"))
	if err != nil {
		t.Fatalf("ForBundle: %v", err)
	}
	if _, ok := b.FilesByPath["logo.png"]; !ok {
		t.Errorf("absolute in-root ref must key as logo.png; have %v", b.FilesByPath)
	}
}

func TestForBundle_byteIdenticalHash(t *testing.T) {
	dir := t.TempDir()
	body := []byte("# Hi\n\n![l](./logo.png)\n")
	writeFile(t, filepath.Join(dir, "index.md"), body)
	writeFile(t, filepath.Join(dir, "logo.png"), []byte("img"))

	b, err := publish.ForBundle(filepath.Join(dir, "index.md"))
	if err != nil {
		t.Fatalf("ForBundle: %v", err)
	}
	if got := b.FilesByPath["index.md"].Hash; got != hashOf(body) {
		t.Errorf("root stored non-identically: hash=%q want %q", got, hashOf(body))
	}
}

func TestForBundle_absoluteRefNotFollowed(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "index.md"), []byte("[x](https://example.com/x.md)\n"))

	b, err := publish.ForBundle(filepath.Join(dir, "index.md"))
	if err != nil {
		t.Fatalf("ForBundle: %v", err)
	}
	if len(b.FilesByPath) != 1 {
		t.Errorf("absolute URL must not be followed; got %v", b.FilesByPath)
	}
}
