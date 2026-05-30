package db_test

import (
	"bytes"
	"testing"

	"github.com/Abir66/mdfly/internal/server/db"
	"github.com/Abir66/mdfly/internal/server/manifest"
)

func mkManifest(root, hash string) manifest.Manifest {
	return manifest.Manifest{
		RootPath:    root,
		FilesByPath: map[string]manifest.ManifestFile{root: {Hash: hash, Size: 1}},
	}
}

func TestManifestHash_deterministic(t *testing.T) {
	m := mkManifest("a.md", "h1")
	h1, err := db.ManifestHash(m)
	if err != nil {
		t.Fatalf("ManifestHash: %v", err)
	}
	h2, err := db.ManifestHash(m)
	if err != nil {
		t.Fatalf("ManifestHash: %v", err)
	}
	if !bytes.Equal(h1, h2) {
		t.Errorf("hash not deterministic: %x != %x", h1, h2)
	}
}

func TestManifestHash_sensitiveToContent(t *testing.T) {
	h1, _ := db.ManifestHash(mkManifest("a.md", "h1"))
	h2, _ := db.ManifestHash(mkManifest("a.md", "h2"))
	if bytes.Equal(h1, h2) {
		t.Error("different content produced same hash")
	}
}
