package markdown

import (
	"path"
	"regexp"
	"strings"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

var reURLScheme = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.\-]*:`)

// ImageRefs returns the raw destinations of every image (`![](dest)`) in the
// source markdown, in document order. Frontmatter is skipped. Destinations are
// returned verbatim (e.g. "./logo.png", "https://x/y.png").
func ImageRefs(md []byte) []string {
	_, body := parseFrontmatter(md)
	doc := mdParser.Parser().Parse(text.NewReader(body))

	var refs []string
	ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) { //nolint:errcheck — walk never errors
		if entering {
			if img, ok := n.(*ast.Image); ok {
				refs = append(refs, string(img.Destination))
			}
		}
		return ast.WalkContinue, nil
	})
	return refs
}

// LinkRefs returns the raw destinations of every markdown link (`[](dest)`) in
// the source markdown, in document order. Frontmatter is skipped. Destinations
// are returned verbatim (e.g. "./y.md", "https://x/y.md").
func LinkRefs(md []byte) []string {
	_, body := parseFrontmatter(md)
	doc := mdParser.Parser().Parse(text.NewReader(body))

	var refs []string
	ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) { //nolint:errcheck — walk never errors
		if entering {
			if link, ok := n.(*ast.Link); ok {
				refs = append(refs, string(link.Destination))
			}
		}
		return ast.WalkContinue, nil
	})
	return refs
}

// ResolveLogicalPath resolves a local reference against the referrer's logical
// directory into a cleaned logical path. referrerDir is the project-root-relative
// directory of the referring file ("." for files at the project root). The result
// keeps a leading "../" when the reference escapes the project root.
func ResolveLogicalPath(referrerDir, ref string) string {
	return path.Clean(path.Join(referrerDir, strings.TrimPrefix(ref, "./")))
}

// IsExternalRef reports whether a reference points outside the bundle and must
// not be downloaded: protocol-relative ("//host/x") or any scheme ("https:",
// "data:", "mailto:"). Relative paths ("./x", "imgs/x", "../x") are local.
func IsExternalRef(ref string) bool {
	return strings.HasPrefix(ref, "//") || reURLScheme.MatchString(ref)
}
