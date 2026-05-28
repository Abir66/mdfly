package handlers

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
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

		rendered, _, err := markdown.RenderWithTimeout(content, markdownRenderTimeout)
		if err != nil {
			http.Error(w, "render error", http.StatusInternalServerError)
			return
		}

		ogImageURL := resolveOGImageURL(deps.R2, doc, mfst)

		pageHTML, err := ssr.RenderPage(ssr.PageData{
			Title:      doc.Title,
			Excerpt:    doc.Excerpt,
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

// resolveOGImageURL finds the public URL for the og image hash using the manifest.
func resolveOGImageURL(r2 *storage.Client, doc *db.Document, mfst manifest.Manifest) string {
	if len(doc.OGImageHash) == 0 {
		return ""
	}
	wantHash := hex.EncodeToString(doc.OGImageHash)
	for path, f := range mfst.FilesByPath {
		if f.Hash == wantHash {
			key := storage.BlobKey(doc.Slug, f.Hash, storage.ExtFromPath(path))
			return r2.BlobPublicURL(key)
		}
	}
	return ""
}
