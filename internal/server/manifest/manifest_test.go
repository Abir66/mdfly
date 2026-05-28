package manifest_test

import (
	"errors"
	"testing"

	"github.com/Abir66/mdfly/internal/api"
	"github.com/Abir66/mdfly/internal/server/manifest"
)

func TestFromDTO_indexesByPath(t *testing.T) {
	dto := api.BundleDTO{
		RootPath: "index.md",
		Files: []api.BundleFileDTO{
			{Path: "index.md", Hash: "roothash", Size: 10},
			{Path: "img/logo.png", Hash: "imghash", Size: 99},
		},
	}

	m, err := manifest.FromDTO(dto)
	if err != nil {
		t.Fatalf("FromDTO: %v", err)
	}
	if m.RootPath != "index.md" {
		t.Errorf("RootPath=%q, want index.md", m.RootPath)
	}
	if len(m.FilesByPath) != 2 {
		t.Fatalf("FilesByPath len=%d, want 2", len(m.FilesByPath))
	}
	if got := m.FilesByPath["img/logo.png"]; got.Hash != "imghash" || got.Size != 99 {
		t.Errorf("FilesByPath[img]=%+v, want {imghash 99}", got)
	}
}

func TestFromDTO_rootPathNotInFiles(t *testing.T) {
	dto := api.BundleDTO{
		RootPath: "missing.md",
		Files:    []api.BundleFileDTO{{Path: "a.md", Hash: "other", Size: 1}},
	}

	_, err := manifest.FromDTO(dto)
	if !errors.Is(err, manifest.ErrRootPathNotFound) {
		t.Errorf("err=%v, want ErrRootPathNotFound", err)
	}
}

func TestRootFile(t *testing.T) {
	m, err := manifest.FromDTO(api.BundleDTO{
		RootPath: "doc.md",
		Files:    []api.BundleFileDTO{{Path: "doc.md", Hash: "rh", Size: 42}},
	})
	if err != nil {
		t.Fatalf("FromDTO: %v", err)
	}

	f, ok := m.RootFile()
	if !ok {
		t.Fatal("RootFile: ok=false, want true")
	}
	if f.Hash != "rh" || f.Size != 42 {
		t.Errorf("RootFile=%+v, want {rh 42}", f)
	}
}
