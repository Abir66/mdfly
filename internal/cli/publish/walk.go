package publish

import (
	"github.com/Abir66/mdfly/internal/cli/walk"
)

// ForBundle walks the root file at rootPath into a Bundle keyed by
// project-root-relative logical path. It delegates selection to the walk module
// (see internal/cli/walk) and adapts the result into BundleFiles with content
// hashes. The walk runs recursively so linked `.md` files are followed
// transitively; -r gating is wired through in the S32 publish rewire.
func ForBundle(rootPath string) (Bundle, error) {
	res, err := walk.Walk(rootPath, walk.Options{Recursive: true})
	if err != nil {
		return Bundle{}, err
	}
	return bundleFromWalk(res), nil
}

// bundleFromWalk converts a walk.Result into a Bundle, hashing each selected
// file's content.
func bundleFromWalk(res walk.Result) Bundle {
	files := make(map[string]BundleFile, len(res.Files))
	for key, f := range res.Files {
		files[key] = newBundleFile(f.LogicalPath, f.DiskPath, f.Content)
	}
	return Bundle{
		RootPath:    res.RootPath,
		ProjectRoot: res.ProjectRoot,
		FilesByPath: files,
	}
}
