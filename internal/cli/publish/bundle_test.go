package publish_test

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Abir66/mdfly/internal/cli/publish"
)

func TestForSingleFile(t *testing.T) {
	dir := t.TempDir()
	content := []byte("# Hello\n\nTest content.\n")
	path := filepath.Join(dir, "test.md")
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}

	b, err := publish.ForSingleFile(path)
	if err != nil {
		t.Fatalf("ForSingleFile: %v", err)
	}

	sum := sha256.Sum256(content)
	wantHash := hex.EncodeToString(sum[:])

	if b.RootPath != "test.md" {
		t.Errorf("RootPath=%q, want test.md", b.RootPath)
	}
	if len(b.FilesByPath) != 1 {
		t.Fatalf("FilesByPath len=%d, want 1", len(b.FilesByPath))
	}
	f, ok := b.FilesByPath["test.md"]
	if !ok {
		t.Fatalf("FilesByPath has no entry for path %q", "test.md")
	}
	if f.Path != "test.md" {
		t.Errorf("Path=%q, want test.md", f.Path)
	}
	if f.Hash != wantHash {
		t.Errorf("Hash=%q, want %q", f.Hash, wantHash)
	}
	if f.Size != int64(len(content)) {
		t.Errorf("Size=%d, want %d", f.Size, len(content))
	}
	if string(f.Content) != string(content) {
		t.Errorf("Content=%q, want the bytes read from disk", f.Content)
	}
}

func TestForSingleFile_notFound(t *testing.T) {
	_, err := publish.ForSingleFile("/nonexistent/path/file.md")
	if err == nil {
		t.Error("want error for nonexistent file, got nil")
	}
}

// A file edited on disk after the bundle is built must not change what upload
// PUTs: Content is the snapshot Hash was computed over, and nothing re-reads
// DiskPath. The file is deliberately large, since the old size-based inlining
// dropped Content above 256 KB and re-read it at upload.
func TestForBundle_contentSnapshotSurvivesDiskEdit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.md")
	original := []byte("# Big\n\n" + strings.Repeat("original ", 64*1024))
	if err := os.WriteFile(path, original, 0644); err != nil {
		t.Fatal(err)
	}

	b, err := publish.ForBundle(path)
	if err != nil {
		t.Fatalf("ForBundle: %v", err)
	}
	f := b.FilesByPath["big.md"]

	edited := []byte("# Big\n\n" + strings.Repeat("modified ", 64*1024))
	if len(edited) != len(original) {
		t.Fatalf("test setup: edit must keep size (%d vs %d)", len(edited), len(original))
	}
	if err := os.WriteFile(path, edited, 0644); err != nil {
		t.Fatal(err)
	}

	if string(f.Content) != string(original) {
		t.Error("Content changed after disk edit; upload would send bytes the server was not told about")
	}
	sum := sha256.Sum256(f.Content)
	if hex.EncodeToString(sum[:]) != f.Hash {
		t.Errorf("Content does not hash to Hash=%q", f.Hash)
	}
}

func TestBundle_ToDTO(t *testing.T) {
	dir := t.TempDir()
	content := []byte("body")
	path := filepath.Join(dir, "doc.md")
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}

	b, err := publish.ForSingleFile(path)
	if err != nil {
		t.Fatalf("ForSingleFile: %v", err)
	}

	dto := b.ToDTO()
	if dto.RootPath != b.RootPath {
		t.Errorf("dto.RootPath=%q, want %q", dto.RootPath, b.RootPath)
	}
	if len(dto.Files) != 1 {
		t.Fatalf("dto.Files len=%d, want 1", len(dto.Files))
	}
	wantHash := b.FilesByPath[b.RootPath].Hash
	if dto.Files[0].Hash != wantHash || dto.Files[0].Path != "doc.md" || dto.Files[0].Size != int64(len(content)) {
		t.Errorf("dto.Files[0]=%+v unexpected", dto.Files[0])
	}
}
