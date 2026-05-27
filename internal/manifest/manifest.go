package manifest

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"

	"github.com/Abir66/mdfly/internal/api"
)

// ForSingleFile reads filePath, computes its SHA256, and returns the manifest + raw bytes.
func ForSingleFile(filePath string) (api.Manifest, []byte, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return api.Manifest{}, nil, err
	}
	sum := sha256.Sum256(content)
	hash := hex.EncodeToString(sum[:])
	base := filepath.Base(filePath)
	return api.Manifest{
		Root:  base,
		Files: []api.ManifestFile{{Path: base, Hash: hash, Size: int64(len(content))}},
	}, content, nil
}
