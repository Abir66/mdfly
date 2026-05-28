package publish_test

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
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

	if b.RootHash != wantHash {
		t.Errorf("RootHash=%q, want %q", b.RootHash, wantHash)
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
	if f.DiskPath != path {
		t.Errorf("DiskPath=%q, want %q", f.DiskPath, path)
	}
	if f.Size != int64(len(content)) {
		t.Errorf("Size=%d, want %d", f.Size, len(content))
	}
	if string(f.Content) != string(content) {
		t.Errorf("Content=%q, want preloaded small file", f.Content)
	}
}

func TestForSingleFile_notFound(t *testing.T) {
	_, err := publish.ForSingleFile("/nonexistent/path/file.md")
	if err == nil {
		t.Error("want error for nonexistent file, got nil")
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
	if dto.RootHash != b.RootHash {
		t.Errorf("dto.RootHash=%q, want %q", dto.RootHash, b.RootHash)
	}
	if len(dto.Files) != 1 {
		t.Fatalf("dto.Files len=%d, want 1", len(dto.Files))
	}
	if dto.Files[0].Hash != b.RootHash || dto.Files[0].Path != "doc.md" || dto.Files[0].Size != int64(len(content)) {
		t.Errorf("dto.Files[0]=%+v unexpected", dto.Files[0])
	}
}
