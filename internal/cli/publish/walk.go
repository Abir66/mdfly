package publish

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/Abir66/mdfly/internal/markdown"
)

// ForBundle reads the root markdown file at rootPath and walks it transitively:
// it follows markdown links (`[](./other.md)`) into reachable `.md` files and
// collects every referenced asset (`![](./img.png)`). The walk is breadth-first
// with a visited-set keyed by absolute on-disk path, so cycles and diamonds
// visit each file exactly once.
//
// The project root is the root file's directory; every file's logical key is its
// path relative to that root. A reference that resolves outside the project root
// (even via `../` or an absolute path) is skipped with a warning and left
// verbatim; one that climbs above the root but folds back inside is uploaded
// under its in-root key. External references (http(s), protocol-relative, other
// schemes) are not followed. A reference whose on-disk target does not exist is
// logged and skipped, leaving the link verbatim — the publish still succeeds.
func ForBundle(rootPath string) (Bundle, error) {
	absRoot, err := filepath.Abs(rootPath)
	if err != nil {
		return Bundle{}, err
	}
	projectRoot := filepath.Dir(absRoot)

	files := map[string]BundleFile{}
	visited := map[string]bool{}
	queue := []string{absRoot}

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if visited[cur] {
			continue
		}
		visited[cur] = true

		content, err := os.ReadFile(cur)
		if err != nil {
			if cur == absRoot {
				return Bundle{}, err
			}
			slog.Warn("referenced file not found, leaving link verbatim", "path", cur, "err", err)
			continue
		}

		key := relKey(projectRoot, cur)
		files[key] = newBundleFile(key, cur, content)

		if !isMarkdown(cur) {
			continue
		}
		for _, target := range reachableTargets(filepath.Dir(cur), projectRoot, content) {
			if !visited[target] {
				queue = append(queue, target)
			}
		}
	}

	return Bundle{
		RootPath:    relKey(projectRoot, absRoot),
		ProjectRoot: projectRoot,
		FilesByPath: files,
	}, nil
}

// reachableTargets resolves every non-external image and link reference in a
// markdown file to its absolute on-disk path, relative to referrerDir.
// References whose target resolves outside projectRoot (after folding any `../`)
// are skipped with a warning and left verbatim in the published markdown.
func reachableTargets(referrerDir, projectRoot string, content []byte) []string {
	refs := append(markdown.ImageRefs(content), markdown.LinkRefs(content)...)
	var targets []string
	for _, ref := range refs {
		ref = stripFragment(ref)
		if ref == "" || markdown.IsExternalRef(ref) {
			continue
		}
		disk := filepath.FromSlash(ref)
		target := filepath.Clean(disk)
		if !filepath.IsAbs(disk) {
			target = filepath.Clean(filepath.Join(referrerDir, disk))
		}
		if escapesRoot(projectRoot, target) {
			slog.Warn("reference outside project root, leaving link verbatim", "ref", ref, "resolved", target)
			continue
		}
		targets = append(targets, target)
	}
	return targets
}

// escapesRoot reports whether target resolves outside projectRoot.
func escapesRoot(projectRoot, target string) bool {
	rel, err := filepath.Rel(projectRoot, target)
	if err != nil {
		return true
	}
	rel = filepath.ToSlash(rel)
	return rel == ".." || strings.HasPrefix(rel, "../")
}

// relKey returns target's slash-separated path relative to projectRoot, keeping
// a leading "../" when target sits above the root.
func relKey(projectRoot, target string) string {
	rel, err := filepath.Rel(projectRoot, target)
	if err != nil {
		return filepath.ToSlash(target)
	}
	return filepath.ToSlash(rel)
}

func isMarkdown(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".md")
}

func stripFragment(ref string) string {
	before, _, _ := strings.Cut(ref, "#")
	return before
}
