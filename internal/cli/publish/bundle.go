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
	Size     int64
	Content  []byte
}

// Bundle is the CLI-local view of a publish bundle, keyed by content hash so the
// presigned-URL response (also keyed by hash) resolves directly to a file.
type Bundle struct {
	RootHash    string
	FilesByHash map[string]BundleFile
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

	f := BundleFile{
		Path:     filepath.Base(filePath),
		DiskPath: filePath,
		Size:     size,
	}
	if size <= maxInlineContentBytes {
		f.Content = content
	}

	return Bundle{
		RootHash:    hash,
		FilesByHash: map[string]BundleFile{hash: f},
	}, nil
}

// ToDTO converts the Bundle into the wire BundleDTO, dropping local-only fields.
func (b Bundle) ToDTO() api.BundleDTO {
	files := make([]api.BundleFileDTO, 0, len(b.FilesByHash))
	for hash, f := range b.FilesByHash {
		files = append(files, api.BundleFileDTO{Path: f.Path, Hash: hash, Size: f.Size})
	}
	return api.BundleDTO{RootHash: b.RootHash, Files: files}
}
