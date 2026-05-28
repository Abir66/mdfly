package publish

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"

	"github.com/Abir66/mdfly/internal/api"
)

// maxInlineContentBytes is the per-file size below which ForSingleFile keeps the
// file content in memory to avoid a second read at upload time.
const maxInlineContentBytes = 256 * 1024

// BundleFile is one file in a local Bundle. Content is preloaded for small
// files (<= maxInlineContentBytes) and nil otherwise, in which case the file is
// re-read from DiskPath at upload.
type BundleFile struct {
	Path     string // logical path within the bundle (sent on the wire)
	DiskPath string // path on disk for reading the bytes
	Hash     string // hex-encoded SHA256 of the file content
	Size     int64
	Content  []byte
}

// Bundle is the CLI-local view of a publish bundle, keyed by logical path.
// Path keying preserves every file even when two files share content (same hash)
// or share both content and extension at different paths.
type Bundle struct {
	RootHash    string
	FilesByPath map[string]BundleFile
}

// ForSingleFile reads filePath, computes its SHA256, and returns a one-file Bundle.
func ForSingleFile(filePath string) (Bundle, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return Bundle{}, err
	}
	sum := sha256.Sum256(content)
	hash := hex.EncodeToString(sum[:])
	size := int64(len(content))

	logicalPath := filepath.Base(filePath)
	f := BundleFile{
		Path:     logicalPath,
		DiskPath: filePath,
		Hash:     hash,
		Size:     size,
	}
	if size <= maxInlineContentBytes {
		f.Content = content
	}

	return Bundle{
		RootHash:    hash,
		FilesByPath: map[string]BundleFile{logicalPath: f},
	}, nil
}

// ToDTO converts the Bundle into the wire BundleDTO, dropping local-only fields.
// One DTO entry is emitted per path, so paths sharing content are preserved.
func (b Bundle) ToDTO() api.BundleDTO {
	files := make([]api.BundleFileDTO, 0, len(b.FilesByPath))
	for _, f := range b.FilesByPath {
		files = append(files, api.BundleFileDTO{Path: f.Path, Hash: f.Hash, Size: f.Size})
	}
	return api.BundleDTO{RootHash: b.RootHash, Files: files}
}
