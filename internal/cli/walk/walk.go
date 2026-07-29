// Package walk resolves the set of files a Publish uploads, keyed by
// project-root-relative logical path. The project root is the Root file's
// directory for a file publish, or the current working directory for a Text
// Publish. Only a markdown Root is parsed for references; a non-markdown Root
// (text, code, binary) has no links to follow and yields a single-file bundle.
//
// Reference gating follows ADR-0010 and CONTEXT.md: non-`.md` assets directly
// referenced by an included file are always pulled in; linked `.md` files are
// followed transitively only under Options.Recursive. References that resolve
// outside the project root — via `../`, an absolute path, `file://`, or a
// symlink whose resolved target escapes the root — are skipped with a warning
// and left verbatim; `http`/`https`/`data:` and other external schemes are
// never fetched. Symlinks are resolved before the boundary check so an escaping
// link's target bytes are never read.
package walk

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/Abir66/mdfly/internal/markdown"
)

const (
	syntheticRootBase = "index"
	markdownExt       = ".md"
)

// File is one file selected by the walk. DiskPath is empty for the synthetic
// root of a Text Publish; Content holds the bytes read for every included file.
type File struct {
	LogicalPath string
	DiskPath    string
	Content     []byte
}

// Result is the outcome of a walk: the logical key of the Root, the absolute
// project root, and every selected file keyed by its logical path.
type Result struct {
	RootPath    string
	ProjectRoot string
	Files       map[string]File
}

// Options tunes the walk. Recursive follows linked `.md` files transitively.
type Options struct {
	Recursive bool
}

// Walk selects the files reachable from the Root file at rootPath. A markdown
// Root is walked for references; any other Root yields a single-file Result.
func Walk(rootPath string, opts Options) (Result, error) {
	absRoot, err := filepath.Abs(rootPath)
	if err != nil {
		return Result{}, err
	}
	content, err := os.ReadFile(absRoot)
	if err != nil {
		return Result{}, err
	}

	projectRoot := filepath.Dir(absRoot)
	w := newWalker(projectRoot, opts)
	rootKey := relKey(projectRoot, absRoot)
	w.files[rootKey] = File{LogicalPath: rootKey, DiskPath: absRoot, Content: content}

	if isMarkdown(absRoot) {
		w.crawl(filepath.Dir(absRoot), content)
	}
	return Result{RootPath: rootKey, ProjectRoot: projectRoot, Files: w.files}, nil
}

// WalkText selects the files reachable from inline text treated as markdown.
// The project root is cwd; the Root is synthesized as index.md, auto-renamed to
// the first free name when the walk already contains an index.md.
func WalkText(content []byte, cwd string, opts Options) (Result, error) {
	projectRoot, err := filepath.Abs(cwd)
	if err != nil {
		return Result{}, err
	}
	w := newWalker(projectRoot, opts)
	w.crawl(projectRoot, content)

	rootKey := freeName(w.files, syntheticRootBase, markdownExt)
	w.files[rootKey] = File{LogicalPath: rootKey, Content: content}
	return Result{RootPath: rootKey, ProjectRoot: projectRoot, Files: w.files}, nil
}

type walker struct {
	projectRoot string // logical project root (keys are relative to this)
	rootReal    string // symlink-resolved project root (boundary comparison)
	recursive   bool
	files       map[string]File
	visited     map[string]bool
}

func newWalker(projectRoot string, opts Options) *walker {
	rootReal, err := filepath.EvalSymlinks(projectRoot)
	if err != nil {
		rootReal = projectRoot
	}
	return &walker{
		projectRoot: projectRoot,
		rootReal:    rootReal,
		recursive:   opts.Recursive,
		files:       map[string]File{},
		visited:     map[string]bool{},
	}
}

// crawl breadth-first-visits every reference reachable from a seed markdown
// file, reading and keying each included file exactly once. Linked markdown is
// followed only under recursive; assets are always included.
func (w *walker) crawl(seedDir string, seedContent []byte) {
	queue := w.targets(seedDir, seedContent)
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if w.visited[cur] {
			continue
		}
		w.visited[cur] = true

		md := isMarkdown(cur)
		if md && !w.recursive {
			continue
		}

		content, err := os.ReadFile(cur)
		if err != nil {
			slog.Warn("referenced file not found, leaving link verbatim", "path", cur, "err", err)
			continue
		}
		key := relKey(w.projectRoot, cur)
		w.files[key] = File{LogicalPath: key, DiskPath: cur, Content: content}

		if md {
			queue = append(queue, w.targets(filepath.Dir(cur), content)...)
		}
	}
}

// targets resolves every local reference in a markdown file to an in-root
// on-disk path. External refs are left untouched; refs whose resolved target
// escapes the project root are skipped with a warning.
func (w *walker) targets(referrerDir string, content []byte) []string {
	refs := append(markdown.ImageRefs(content), markdown.LinkRefs(content)...)
	var out []string
	for _, ref := range refs {
		ref = stripFragment(ref)
		if ref == "" {
			continue
		}
		if markdown.IsExternalRef(ref) {
			if isFileScheme(ref) {
				slog.Warn("reference outside project root, leaving link verbatim", "ref", ref)
			}
			continue
		}
		if target, ok := w.resolveInRoot(referrerDir, ref); ok {
			out = append(out, target)
		}
	}
	return out
}

// resolveInRoot turns a local reference into its logical on-disk path and
// reports whether it stays inside the project root. The boundary check runs
// against the symlink-resolved target so an escaping link is rejected before
// its bytes are ever read.
func (w *walker) resolveInRoot(referrerDir, ref string) (string, bool) {
	disk := filepath.FromSlash(ref)
	target := filepath.Clean(disk)
	if !filepath.IsAbs(disk) {
		target = filepath.Clean(filepath.Join(referrerDir, disk))
	}

	check := target
	if resolved, err := filepath.EvalSymlinks(target); err == nil {
		check = resolved
	}
	if escapesRoot(w.rootReal, check) {
		slog.Warn("reference outside project root, leaving link verbatim", "ref", ref, "resolved", check)
		return "", false
	}
	return target, true
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

// relKey returns target's slash-separated path relative to projectRoot.
func relKey(projectRoot, target string) string {
	rel, err := filepath.Rel(projectRoot, target)
	if err != nil {
		return filepath.ToSlash(target)
	}
	return filepath.ToSlash(rel)
}

// freeName returns base+ext, or the first base-N+ext not already a key in files.
func freeName(files map[string]File, base, ext string) string {
	name := base + ext
	for i := 1; ; i++ {
		if _, taken := files[name]; !taken {
			return name
		}
		name = fmt.Sprintf("%s-%d%s", base, i, ext)
	}
}

func isMarkdown(path string) bool {
	return strings.EqualFold(filepath.Ext(path), markdownExt)
}

func isFileScheme(ref string) bool {
	return strings.HasPrefix(strings.ToLower(ref), "file:")
}

func stripFragment(ref string) string {
	before, _, _ := strings.Cut(ref, "#")
	return before
}
