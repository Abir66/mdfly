package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"

	"github.com/Abir66/mdfly/internal/api"
	"github.com/Abir66/mdfly/internal/server/db"
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

		var manifest api.Manifest
		if err := json.Unmarshal(doc.ManifestJSON, &manifest); err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		rootKey, err := rootBlobKey(manifest)
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

func rootBlobKey(manifest api.Manifest) (string, error) {
	for _, f := range manifest.Files {
		if f.Path == manifest.Root {
			return storage.BlobKey(f.Hash, storage.ExtFromPath(f.Path)), nil
		}
	}
	return "", fmt.Errorf("root file %q not found in manifest", manifest.Root)
}
