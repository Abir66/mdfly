package manifest_test

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/Abir66/mdfly/internal/api"
	"github.com/Abir66/mdfly/internal/server/manifest"
)

func TestNestedKey(t *testing.T) {
	tests := []struct {
		rest    string
		up      string
		wantKey string
		wantOK  bool
	}{
		{"y.md", "", "y.md", true},
		{"sub2/a.md", "", "sub2/a.md", true},
		{"/y.md/", "", "y.md", true},
		{"a.md", "2", "../../a.md", true},
		{"assets/logo.png", "", "assets/logo.png", true},
		{"", "", "", false},
		{"y.md", "abc", "", false},
		{"y.md", "-1", "", false},
		{"y.md", strconv.Itoa(manifest.MaxUp), strings.Repeat("../", manifest.MaxUp) + "y.md", true},
		{"y.md", strconv.Itoa(manifest.MaxUp + 1), "", false},
	}
	for _, tt := range tests {
		key, ok := manifest.NestedKey(tt.rest, tt.up)
		if ok != tt.wantOK || key != tt.wantKey {
			t.Errorf("NestedKey(%q,%q)=(%q,%v), want (%q,%v)", tt.rest, tt.up, key, ok, tt.wantKey, tt.wantOK)
		}
	}
}

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

func TestFromDTO_carriesProjectRoot(t *testing.T) {
	m, err := manifest.FromDTO(api.BundleDTO{
		RootPath:    "index.md",
		ProjectRoot: "/home/user/proj",
		Files:       []api.BundleFileDTO{{Path: "index.md", Hash: "rh", Size: 1}},
	})
	if err != nil {
		t.Fatalf("FromDTO: %v", err)
	}
	if m.ProjectRoot != "/home/user/proj" {
		t.Errorf("ProjectRoot=%q, want /home/user/proj", m.ProjectRoot)
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

func TestFromDTO_emptyRootPathTolerated(t *testing.T) {
	m, err := manifest.FromDTO(api.BundleDTO{
		RootPath: "",
		Files:    []api.BundleFileDTO{{Path: "a.md", Hash: "ah", Size: 1}},
	})
	if err != nil {
		t.Fatalf("FromDTO empty root_path: %v", err)
	}
	if _, ok := m.RootFile(); ok {
		t.Error("RootFile: ok=true for empty root_path, want false")
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
