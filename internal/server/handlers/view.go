package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Abir66/mdfly/internal/markdown"
	"github.com/Abir66/mdfly/internal/server/db"
	"github.com/Abir66/mdfly/internal/server/manifest"
	"github.com/Abir66/mdfly/internal/server/ssr"
	"github.com/Abir66/mdfly/internal/server/storage"
)

const (
	markdownRenderTimeout = 2 * time.Second
	markdownExt           = ".md"
)

// ViewDeps holds dependencies for the view handler.
type ViewDeps struct {
	PG *db.Client
	R2 *storage.Client
}

// View handles GET /{slug}, rendering the bundle's root document.
func View(deps ViewDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := r.PathValue("slug")
		if slug == "" {
			http.NotFound(w, r)
			return
		}
		mfst, ok := loadManifest(w, r, deps, slug)
		if !ok {
			return
		}
		serveDocPage(w, r, deps, slug, mfst, mfst.RootPath)
	}
}

// ViewPath handles GET /{slug}/{path...}, rendering a nested .md page. The path
// segment is the project-root-relative key with the .md extension stripped;
// ?up=N reconstructs a key that sits N levels above the project root.
func ViewPath(deps ViewDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := r.PathValue("slug")
		if slug == "" {
			http.NotFound(w, r)
			return
		}
		key, ok := nestedKey(r.PathValue("path"), r.URL.Query().Get("up"))
		if !ok {
			http.NotFound(w, r)
			return
		}
		mfst, ok := loadManifest(w, r, deps, slug)
		if !ok {
			return
		}
		serveDocPage(w, r, deps, slug, mfst, key)
	}
}

// loadManifest fetches and decodes the document's manifest, writing the
// appropriate error response and returning ok=false on failure.
func loadManifest(w http.ResponseWriter, r *http.Request, deps ViewDeps, slug string) (manifest.Manifest, bool) {
	doc, err := deps.PG.GetBySlug(r.Context(), slug)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			http.NotFound(w, r)
		} else {
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
		return manifest.Manifest{}, false
	}
	var mfst manifest.Manifest
	if err := json.Unmarshal(doc.ManifestJSON, &mfst); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return manifest.Manifest{}, false
	}
	return mfst, true
}

// serveDocPage renders the file at key within the bundle and writes the page.
// An unknown key is a 404.
func serveDocPage(w http.ResponseWriter, r *http.Request, deps ViewDeps, slug string, mfst manifest.Manifest, key string) {
	f, ok := mfst.FilesByPath[key]
	if !ok {
		http.NotFound(w, r)
		return
	}

	content, err := deps.R2.GetBlob(r.Context(), storage.BlobKey(slug, f.Hash, storage.ExtFromPath(key)))
	if err != nil {
		if errors.Is(err, storage.ErrBlobMissing) {
			http.Error(w, "blob not found", http.StatusInternalServerError)
		} else {
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
		return
	}

	resolve := pageResolver(deps.R2, slug, mfst, path.Dir(key))
	rendered, meta, err := markdown.RenderRefsWithTimeout(content, resolve, markdownRenderTimeout)
	if err != nil {
		http.Error(w, "render error", http.StatusInternalServerError)
		return
	}

	ogImageURL := resolveOGImage(meta.OGImagePath, resolve)

	pageHTML, err := ssr.RenderPage(ssr.PageData{
		Title:      meta.Title,
		Excerpt:    meta.Excerpt,
		OGImageURL: ogImageURL,
		Body:       template.HTML(rendered),
	})
	if err != nil {
		http.Error(w, "render error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, pageHTML)
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

// nestedKey reconstructs the manifest key for GET /{slug}/{path...}. rest is the
// project-root-relative path with .md stripped; up is the optional ?up=N count of
// "../" prefixes for keys above the project root. ok is false for an empty path
// or a malformed up value.
func nestedKey(rest, up string) (string, bool) {
	rest = strings.Trim(rest, "/")
	if rest == "" {
		return "", false
	}
	n := 0
	if up != "" {
		parsed, err := strconv.Atoi(up)
		if err != nil || parsed < 0 {
			return "", false
		}
		n = parsed
	}
	return strings.Repeat("../", n) + rest + markdownExt, true
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
		key, ok := resolveKey(referrerDir, mfst.ProjectRoot, ref)
		if !ok {
			return "", false
		}
		f, ok := mfst.FilesByPath[key]
		if !ok {
			return "", false
		}
		if isMarkdownKey(key) {
			return slugPageURL(slug, key), true
		}
		return r2.BlobPublicURL(storage.BlobKey(slug, f.Hash, storage.ExtFromPath(key))), true
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
