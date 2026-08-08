package publish

import (
	"fmt"
	"os"

	"github.com/Abir66/mdfly/internal/cli/input"
	"github.com/Abir66/mdfly/internal/cli/walk"
)

// bundleForSource builds a Bundle from a resolved content source: a file walk
// rooted at the file's directory, or a Text Publish walk anchored at the cwd
// with a synthetic index.md root. recursive follows linked .md files
// transitively (-r); assets are always pulled in.
func bundleForSource(src input.Source, recursive bool) (Bundle, error) {
	opts := walk.Options{Recursive: recursive}
	switch src.Kind {
	case input.KindFile:
		res, err := walk.Walk(src.Path, opts)
		if err != nil {
			return Bundle{}, err
		}
		return bundleFromWalk(res), nil
	case input.KindText:
		cwd, err := os.Getwd()
		if err != nil {
			return Bundle{}, err
		}
		res, err := walk.WalkText(src.Content, cwd, opts)
		if err != nil {
			return Bundle{}, err
		}
		return bundleFromWalk(res), nil
	default:
		return Bundle{}, fmt.Errorf("unknown content source kind %d", src.Kind)
	}
}

// ForBundle walks the root file at rootPath into a Bundle keyed by
// project-root-relative logical path. It delegates selection to the walk module
// (see internal/cli/walk) and adapts the result into BundleFiles with content
// hashes. The walk runs recursively so linked `.md` files are followed
// transitively; the -r flag is wired through Run via bundleForSource.
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
		files[key] = newBundleFile(f.LogicalPath, f.Content)
	}
	return Bundle{
		RootPath:    res.RootPath,
		ProjectRoot: res.ProjectRoot,
		FilesByPath: files,
	}
}
