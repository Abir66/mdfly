package manifest

import (
	"errors"
	"strconv"
	"strings"

	"github.com/Abir66/mdfly/internal/api"
)

// ErrRootPathNotFound is returned when a BundleDTO's RootPath is not present in Files.
var ErrRootPathNotFound = errors.New("root path not found in bundle files")

// MaxUp caps ?up=N so a malicious request can't force a huge
// strings.Repeat("../", N) allocation. No real bundle nests this deep.
const MaxUp = 16

// NestedKey reconstructs the manifest key for a nested read path shared by the
// view and raw surfaces. rest is the project-root-relative path with its
// extension intact (ADR-0010 reverses the strip-.md convention); up is the
// optional ?up=N count of "../" prefixes for keys above the project root. ok is
// false for an empty path or an up value that is malformed, negative, or above
// MaxUp.
func NestedKey(rest, up string) (string, bool) {
	rest = strings.Trim(rest, "/")
	if rest == "" {
		return "", false
	}
	n := 0
	if up != "" {
		parsed, err := strconv.Atoi(up)
		if err != nil || parsed < 0 || parsed > MaxUp {
			return "", false
		}
		n = parsed
	}
	return strings.Repeat("../", n) + rest, true
}

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
