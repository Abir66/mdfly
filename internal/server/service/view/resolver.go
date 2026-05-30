package view

import (
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Abir66/mdfly/internal/markdown"
	"github.com/Abir66/mdfly/internal/server/manifest"
	"github.com/Abir66/mdfly/internal/server/storage"
)

const (
	markdownExt = ".md"
	// maxUp caps ?up=N so a malicious request can't force a huge
	// strings.Repeat("../", N) allocation. No real bundle nests this deep.
	maxUp = 16
)

// nestedKey reconstructs the manifest key for GET /{slug}/{path...}. rest is the
// project-root-relative path with .md stripped; up is the optional ?up=N count of
// "../" prefixes for keys above the project root. ok is false for an empty path
// or an up value that is malformed, negative, or above maxUp.
func nestedKey(rest, up string) (string, bool) {
	rest = strings.Trim(rest, "/")
	if rest == "" {
		return "", false
	}
	n := 0
	if up != "" {
		parsed, err := strconv.Atoi(up)
		if err != nil || parsed < 0 || parsed > maxUp {
			return "", false
		}
		n = parsed
	}
	return strings.Repeat("../", n) + rest + markdownExt, true
}

// resolveOGImage maps the document's OG-image path to an absolute URL. External
// refs (and body images already rewritten to a CDN URL) are used verbatim;
// in-bundle refs are resolved through the page resolver.
func resolveOGImage(ogPath string, resolve markdown.RefResolver) string {
	if markdown.IsExternalRef(ogPath) {
		return ogPath
	}
	url, _ := resolve(ogPath)
	return url
}

// pageResolver returns a resolver mapping a markdown reference to its final URL:
// an in-bundle markdown file → /{slug}/{key without .md} (with ?up=N when above
// root); an in-bundle asset → its public CDN URL. Empty, external, or unknown
// references return ok=false so the caller leaves them verbatim. References are
// resolved relative to referrerDir (the rendered page's directory).
func pageResolver(r2 *storage.Client, slug string, mfst manifest.Manifest, referrerDir string) markdown.RefResolver {
	return func(ref string) (string, bool) {
		if ref == "" || markdown.IsExternalRef(ref) {
			return "", false
		}
		baseRef, suffix := splitRefSuffix(ref)
		key, ok := resolveKey(referrerDir, mfst.ProjectRoot, baseRef)
		if !ok {
			return "", false
		}
		f, ok := mfst.FilesByPath[key]
		if !ok {
			return "", false
		}
		if isMarkdownKey(key) {
			return slugPageURL(slug, key) + suffix, true
		}
		return r2.BlobPublicURL(storage.BlobKey(slug, f.Hash, storage.ExtFromPath(key))) + suffix, true
	}
}

// slugPageURL builds the viewer URL for a markdown manifest key. A key above the
// project root (leading "../") encodes its depth as ?up=N because browsers and
// proxies strip dot-segments from the path.
func slugPageURL(slug, key string) string {
	up := 0
	for strings.HasPrefix(key, "../") {
		up++
		key = key[len("../"):]
	}
	url := "/" + slug + "/" + strings.TrimSuffix(key, markdownExt)
	if up > 0 {
		url += "?up=" + strconv.Itoa(up)
	}
	return url
}

func isMarkdownKey(key string) bool {
	return storage.ExtFromPath(key) == markdownExt
}

// splitRefSuffix separates a markdown reference into the path portion and its
// query/fragment suffix (the first "?" or "#" onward). The suffix is preserved
// verbatim so a rewritten URL keeps refs like ./y.md#intro or ./img.png?v=1.
func splitRefSuffix(ref string) (base, suffix string) {
	if i := strings.IndexAny(ref, "?#"); i >= 0 {
		return ref[:i], ref[i:]
	}
	return ref, ""
}

// resolveKey turns a markdown reference into a project-root-relative manifest
// key. Every reference is resolved into the publisher's absolute path space and
// then made relative to projectRoot, so absolute refs, plain relatives, and
// relatives that climb above the root before landing back inside it all yield
// the same key the CLI walk stored. A key keeps a leading "../" when the target
// genuinely sits above the root. When projectRoot is unknown (legacy bundles),
// relative refs fall back to a logical join and absolute refs are unresolvable.
func resolveKey(referrerDir, projectRoot, ref string) (string, bool) {
	if projectRoot == "" {
		if path.IsAbs(ref) {
			return "", false
		}
		return markdown.ResolveLogicalPath(referrerDir, ref), true
	}

	abs := filepath.FromSlash(ref)
	if !path.IsAbs(ref) {
		abs = filepath.Join(projectRoot, filepath.FromSlash(referrerDir), abs)
	}
	rel, err := filepath.Rel(projectRoot, filepath.Clean(abs))
	if err != nil {
		return "", false
	}
	return filepath.ToSlash(rel), true
}
