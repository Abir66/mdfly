package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"path"
	"path/filepath"
	"time"

	"github.com/Abir66/mdfly/internal/markdown"
	"github.com/Abir66/mdfly/internal/server/db"
	"github.com/Abir66/mdfly/internal/server/manifest"
	"github.com/Abir66/mdfly/internal/server/ssr"
	"github.com/Abir66/mdfly/internal/server/storage"
)

const markdownRenderTimeout = 2 * time.Second

// ViewDeps holds dependencies for the view handler.
type ViewDeps struct {
	PG *db.Client
	R2 *storage.Client
}

// View handles GET /{slug}.
func View(deps ViewDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sl := r.PathValue("slug")
		if sl == "" {
			http.NotFound(w, r)
			return
		}

		doc, err := deps.PG.GetBySlug(r.Context(), sl)
		if err != nil {
			if errors.Is(err, db.ErrNotFound) {
				http.NotFound(w, r)
				return
			}
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		var mfst manifest.Manifest
		if err := json.Unmarshal(doc.ManifestJSON, &mfst); err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		rootKey, err := rootBlobKey(doc.Slug, mfst)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		content, err := deps.R2.GetBlob(r.Context(), rootKey)
		if err != nil {
			if errors.Is(err, storage.ErrBlobMissing) {
				http.Error(w, "blob not found", http.StatusInternalServerError)
				return
			}
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		rendered, meta, err := markdown.RenderWithTimeout(content, markdownRenderTimeout)
		if err != nil {
			http.Error(w, "render error", http.StatusInternalServerError)
			return
		}

		resolve := assetResolver(deps.R2, doc.Slug, mfst)
		rendered = markdown.RewriteImageRefs(rendered, resolve)
		var ogImageURL string
		if markdown.IsExternalRef(meta.OGImagePath) {
			ogImageURL = meta.OGImagePath
		} else {
			ogImageURL, _ = resolve(meta.OGImagePath)
		}

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
}

func rootBlobKey(slug string, mfst manifest.Manifest) (string, error) {
	f, ok := mfst.RootFile()
	if !ok {
		return "", fmt.Errorf("root path %q not found in manifest", mfst.RootPath)
	}
	return storage.BlobKey(slug, f.Hash, storage.ExtFromPath(mfst.RootPath)), nil
}

// assetResolver returns a function that maps a markdown asset reference to its
// public CDN URL by resolving it to a project-root-relative key and looking that
// up in the manifest. References are resolved relative to the root document's
// directory; absolute references are resolved against the manifest's project
// root. It returns ok=false for empty, external, or unknown references, leaving
// them verbatim.
func assetResolver(r2 *storage.Client, slug string, mfst manifest.Manifest) func(string) (string, bool) {
	rootDir := path.Dir(mfst.RootPath)
	return func(ref string) (string, bool) {
		if ref == "" || markdown.IsExternalRef(ref) {
			return "", false
		}
		key, ok := resolveKey(rootDir, mfst.ProjectRoot, ref)
		if !ok {
			return "", false
		}
		f, ok := mfst.FilesByPath[key]
		if !ok {
			return "", false
		}
		return r2.BlobPublicURL(storage.BlobKey(slug, f.Hash, storage.ExtFromPath(key))), true
	}
}

// resolveKey turns a markdown reference into a project-root-relative manifest
// key. Relative references resolve against referrerDir; absolute references
// resolve against projectRoot via Rel.
func resolveKey(referrerDir, projectRoot, ref string) (string, bool) {
	if path.IsAbs(ref) {
		if projectRoot == "" {
			return "", false
		}
		rel, err := filepath.Rel(projectRoot, filepath.FromSlash(ref))
		if err != nil {
			return "", false
		}
		return filepath.ToSlash(rel), true
	}
	return markdown.ResolveLogicalPath(referrerDir, ref), true
}
