package manifest

import (
	"errors"

	"github.com/Abir66/mdfly/internal/api"
)

// ErrRootPathNotFound is returned when a BundleDTO's RootPath is not present in Files.
var ErrRootPathNotFound = errors.New("root path not found in bundle files")

// ManifestFile is one file in a stored Manifest, keyed by its logical path.
type ManifestFile struct {
	Hash string `json:"hash"`
	Size int64  `json:"size"`
}

// Manifest is the server-side, persisted form of a bundle. It is keyed by
// project-root-relative logical path so SSR can rewrite references to R2 blob
// keys in O(1). ProjectRoot is the publisher's absolute project-root path, used
// to resolve absolute references found in markdown content.
type Manifest struct {
	RootPath    string                  `json:"root_path"`
	ProjectRoot string                  `json:"project_root"`
	FilesByPath map[string]ManifestFile `json:"files_by_path"`
}

// FromDTO converts the wire BundleDTO into a path-keyed Manifest. An empty
// dto.RootPath is tolerated (forward-compat for a folder-Document, whose root is
// a Directory Listing). Returns ErrRootPathNotFound only when a non-empty
// dto.RootPath is absent from dto.Files.
func FromDTO(dto api.BundleDTO) (Manifest, error) {
	files := make(map[string]ManifestFile, len(dto.Files))
	for _, f := range dto.Files {
		files[f.Path] = ManifestFile{Hash: f.Hash, Size: f.Size}
	}
	if dto.RootPath != "" {
		if _, ok := files[dto.RootPath]; !ok {
			return Manifest{}, ErrRootPathNotFound
		}
	}
	return Manifest{RootPath: dto.RootPath, ProjectRoot: dto.ProjectRoot, FilesByPath: files}, nil
}

// RootFile returns the root file entry and whether it exists in FilesByPath. An
// empty RootPath (folder-Document) returns ok=false: the root is a listing.
func (m Manifest) RootFile() (ManifestFile, bool) {
	if m.RootPath == "" {
		return ManifestFile{}, false
	}
	f, ok := m.FilesByPath[m.RootPath]
	return f, ok
}
