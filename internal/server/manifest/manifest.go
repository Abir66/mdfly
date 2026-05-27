package manifest

import (
	"errors"

	"github.com/Abir66/mdfly/internal/api"
)

// ErrRootHashNotFound is returned when a BundleDTO's RootHash matches no file.
var ErrRootHashNotFound = errors.New("root hash not found in bundle files")

// ManifestFile is one file in a stored Manifest, keyed by its logical path.
type ManifestFile struct {
	Hash string `json:"hash"`
	Size int64  `json:"size"`
}

// Manifest is the server-side, persisted form of a bundle. It is keyed by
// logical path so SSR can rewrite asset references to R2 blob keys in O(1).
type Manifest struct {
	RootPath    string                  `json:"root_path"`
	FilesByPath map[string]ManifestFile `json:"files_by_path"`
}

// FromDTO converts the wire BundleDTO into a path-keyed Manifest, resolving
// RootHash to the matching file's path. Returns ErrRootHashNotFound if no
// file carries the root hash.
func FromDTO(dto api.BundleDTO) (Manifest, error) {
	files := make(map[string]ManifestFile, len(dto.Files))
	var rootPath string
	found := false
	for _, f := range dto.Files {
		files[f.Path] = ManifestFile{Hash: f.Hash, Size: f.Size}
		if !found && f.Hash == dto.RootHash {
			rootPath = f.Path
			found = true
		}
	}
	if !found {
		return Manifest{}, ErrRootHashNotFound
	}
	return Manifest{RootPath: rootPath, FilesByPath: files}, nil
}

// RootFile returns the root file entry and whether it exists in FilesByPath.
func (m Manifest) RootFile() (ManifestFile, bool) {
	f, ok := m.FilesByPath[m.RootPath]
	return f, ok
}
