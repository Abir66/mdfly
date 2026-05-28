package markdown

import (
	"path"
	"regexp"
	"strings"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

var (
	reURLScheme   = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.\-]*:`)
	reImgSrcParts = regexp.MustCompile(`(<img[^>]*\bsrc=")([^"]*)(")`)
)

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

// IsExternalRef reports whether a reference points outside the bundle and must
// not be downloaded: protocol-relative ("//host/x") or any scheme ("https:",
// "data:", "mailto:"). Relative paths ("./x", "imgs/x", "../x") are local.
func IsExternalRef(ref string) bool {
	return strings.HasPrefix(ref, "//") || reURLScheme.MatchString(ref)
}

// NormalizeAssetPath turns a local image reference into its bundle-relative
// logical path: leading "./" stripped and the path cleaned.
func NormalizeAssetPath(ref string) string {
	return path.Clean(strings.TrimPrefix(ref, "./"))
}

// RewriteImageRefs rewrites every <img src> in rendered HTML using resolve.
// When resolve returns ok, the src is replaced; otherwise it is left verbatim
// (external URLs and unknown assets are untouched).
func RewriteImageRefs(htmlBody []byte, resolve func(src string) (string, bool)) []byte {
	return reImgSrcParts.ReplaceAllFunc(htmlBody, func(m []byte) []byte {
		sub := reImgSrcParts.FindSubmatch(m)
		newSrc, ok := resolve(string(sub[2]))
		if !ok {
			return m
		}
		out := make([]byte, 0, len(sub[1])+len(newSrc)+len(sub[3]))
		out = append(out, sub[1]...)
		out = append(out, newSrc...)
		out = append(out, sub[3]...)
		return out
	})
}
