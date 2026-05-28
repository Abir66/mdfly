package publish

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Abir66/mdfly/internal/markdown"
)

// ForBundle reads the root markdown file at rootPath, walks its image
// references (`![](./path)`), and returns a Bundle containing the root plus
// every reachable relative image asset.
//
// External references (http(s), protocol-relative, other schemes) are skipped,
// not downloaded. A referenced asset that does not exist on disk is a fatal
// error so the CLI fails before any network call. Each logical path is added
// at most once.
func ForBundle(rootPath string) (Bundle, error) {
	content, err := os.ReadFile(rootPath)
	if err != nil {
		return Bundle{}, err
	}

	rootLogical := filepath.Base(rootPath)
	files := map[string]BundleFile{rootLogical: newBundleFile(rootLogical, rootPath, content)}

	rootDir := filepath.Dir(rootPath)
	for _, ref := range markdown.ImageRefs(content) {
		if markdown.IsExternalRef(ref) {
			continue
		}
		logical := markdown.NormalizeAssetPath(ref)
		if _, seen := files[logical]; seen {
			continue
		}
		diskPath := filepath.Join(rootDir, filepath.FromSlash(strings.TrimPrefix(ref, "./")))
		rel, relErr := filepath.Rel(rootDir, diskPath)
		if relErr != nil || strings.HasPrefix(rel, "..") {
			return Bundle{}, fmt.Errorf("referenced asset %q escapes document directory", ref)
		}
		assetContent, err := os.ReadFile(diskPath)
		if err != nil {
			return Bundle{}, fmt.Errorf("referenced asset %q: %w", ref, err)
		}
		files[logical] = newBundleFile(logical, diskPath, assetContent)
	}

	return Bundle{RootPath: rootLogical, FilesByPath: files}, nil
}
