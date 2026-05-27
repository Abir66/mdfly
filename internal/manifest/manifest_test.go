package manifest_test

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/Abir66/mdfly/internal/manifest"
)

func TestForSingleFile(t *testing.T) {
	dir := t.TempDir()
	content := []byte("# Hello\n\nTest content.\n")
	path := filepath.Join(dir, "test.md")
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}

	mfst, got, err := manifest.ForSingleFile(path)
	if err != nil {
		t.Fatalf("ForSingleFile: %v", err)
	}

	if string(got) != string(content) {
		t.Errorf("content mismatch: got %q, want %q", got, content)
	}
	if mfst.Root != "test.md" {
		t.Errorf("root=%q, want %q", mfst.Root, "test.md")
	}
	if len(mfst.Files) != 1 {
		t.Fatalf("files len=%d, want 1", len(mfst.Files))
	}
	f := mfst.Files[0]
	if f.Path != "test.md" {
		t.Errorf("file path=%q, want %q", f.Path, "test.md")
	}
	if f.Size != int64(len(content)) {
		t.Errorf("file size=%d, want %d", f.Size, len(content))
	}
	sum := sha256.Sum256(content)
	wantHash := hex.EncodeToString(sum[:])
	if f.Hash != wantHash {
		t.Errorf("file hash=%q, want %q", f.Hash, wantHash)
	}
}

func TestForSingleFile_notFound(t *testing.T) {
	_, _, err := manifest.ForSingleFile("/nonexistent/path/file.md")
	if err == nil {
		t.Error("want error for nonexistent file, got nil")
	}
}
