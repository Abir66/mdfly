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
// project-root-relative path with its extension intact (ADR-0010 reverses the
// strip-.md convention); up is the optional ?up=N count of "../" prefixes for
// keys above the project root. ok is false for an empty path or an up value that
// is malformed, negative, or above maxUp.
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
	return strings.Repeat("../", n) + rest, true
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
// an in-bundle markdown file → /{slug}/{key with extension} (with ?up=N when
// above root, per ADR-0010); an in-bundle asset → its public CDN URL. Empty,
// external, or unknown references return ok=false so the caller leaves them
// verbatim. References are resolved relative to referrerDir (the rendered page's
// directory).
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
			if isDirKey(mfst, key) {
				return appendSuffix(slugPageURL(slug, key), suffix), true
			}
			return "", false
		}
		if isMarkdownKey(key) {
			return appendSuffix(slugPageURL(slug, key), suffix), true
		}
		return appendSuffix(r2.BlobPublicURL(storage.BlobKey(slug, f.Hash, storage.ExtFromPath(key))), suffix), true
	}
}

// slugPageURL builds the viewer URL for a markdown manifest key. The key's
// extension is kept (ADR-0010). A key above the project root (leading "../")
// encodes its depth as ?up=N because browsers and proxies strip dot-segments
// from the path.
func slugPageURL(slug, key string) string {
	up := 0
	for strings.HasPrefix(key, "../") {
		up++
		key = key[len("../"):]
	}
	url := "/" + slug + "/" + key
	if up > 0 {
		url += "?up=" + strconv.Itoa(up)
	}
	return url
}

func isMarkdownKey(key string) bool {
	return storage.ExtFromPath(key) == markdownExt
}

// imageExts are the extensions rendered inline as <img> (SVG is safe via <img>).
var imageExts = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true,
	".gif": true, ".webp": true, ".svg": true,
}

// isImageKey reports whether key names an inline-renderable image.
func isImageKey(key string) bool {
	return imageExts[storage.ExtFromPath(key)]
}

// isDirKey reports whether key names an in-bundle directory — a path prefix of
// one or more manifest keys (no folders are stored, so this is the only test).
func isDirKey(mfst manifest.Manifest, key string) bool {
	prefix := key + "/"
	for k := range mfst.FilesByPath {
		if strings.HasPrefix(k, prefix) {
			return true
		}
	}
	return false
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

// appendSuffix joins a splitRefSuffix suffix onto an already-built URL. A "?"
// query suffix becomes "&" when the URL already carries a query (e.g. ?up=N from
// slugPageURL) so the two merge into one valid query string; fragment suffixes
// and the no-existing-query case pass through verbatim.
func appendSuffix(url, suffix string) string {
	if strings.HasPrefix(suffix, "?") && strings.Contains(url, "?") {
		return url + "&" + suffix[len("?"):]
	}
	return url + suffix
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
