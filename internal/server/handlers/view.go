package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"

	"github.com/Abir66/mdfly/internal/server/db"
	"github.com/Abir66/mdfly/internal/server/manifest"
	"github.com/Abir66/mdfly/internal/server/storage"
)

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

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, "<!doctype html><html><body><pre>%s</pre></body></html>",
			html.EscapeString(string(content)))
	}
}

func rootBlobKey(slug string, mfst manifest.Manifest) (string, error) {
	f, ok := mfst.RootFile()
	if !ok {
		return "", fmt.Errorf("root path %q not found in manifest", mfst.RootPath)
	}
	return storage.BlobKey(slug, f.Hash, storage.ExtFromPath(mfst.RootPath)), nil
}
