package publish

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"

	"github.com/Abir66/mdfly/internal/api"
)

// BundleFile is one file in a local Bundle. Content holds the exact bytes Hash
// was computed over and is what the upload phase PUTs, so a file edited between
// hashing and upload cannot desync the bundle from what the server was told.
type BundleFile struct {
	Path    string // logical path within the bundle (sent on the wire)
	Hash    string // hex-encoded SHA256 of Content
	Size    int64
	Content []byte
}

// Bundle is the CLI-local view of a publish bundle, keyed by project-root-relative
// logical path. Path keying preserves every file even when two files share content
// (same hash) or share both content and extension at different paths.
type Bundle struct {
	RootPath    string // project-root-relative path of the root markdown file
	ProjectRoot string // absolute path of the project root (root file's dir)
	FilesByPath map[string]BundleFile
}

// ForSingleFile reads filePath, computes its SHA256, and returns a one-file Bundle.
func ForSingleFile(filePath string) (Bundle, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return Bundle{}, err
	}
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return Bundle{}, err
	}
	logicalPath := filepath.Base(absPath)
	f := newBundleFile(logicalPath, content)
	return Bundle{
		RootPath:    logicalPath,
		ProjectRoot: filepath.Dir(absPath),
		FilesByPath: map[string]BundleFile{logicalPath: f},
	}, nil
}

// newBundleFile builds a BundleFile from content already read into memory,
// retaining the bytes so upload never re-reads from disk.
func newBundleFile(logicalPath string, content []byte) BundleFile {
	sum := sha256.Sum256(content)
	return BundleFile{
		Path:    logicalPath,
		Hash:    hex.EncodeToString(sum[:]),
		Size:    int64(len(content)),
		Content: content,
	}
}

// TotalBytes is the summed size of every file in the bundle.
func (b Bundle) TotalBytes() int64 {
	var total int64
	for _, f := range b.FilesByPath {
		total += f.Size
	}
	return total
}

// ToDTO converts the Bundle into the wire BundleDTO, dropping local-only fields.
// One DTO entry is emitted per path, so paths sharing content are preserved.
func (b Bundle) ToDTO() api.BundleDTO {
	files := make([]api.BundleFileDTO, 0, len(b.FilesByPath))
	for _, f := range b.FilesByPath {
		files = append(files, api.BundleFileDTO{Path: f.Path, Hash: f.Hash, Size: f.Size})
	}
	return api.BundleDTO{RootPath: b.RootPath, ProjectRoot: b.ProjectRoot, Files: files}
}
