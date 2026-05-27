package handlers

import (
	"html"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/Abir66/mdfly/internal/api"
	pgstore "github.com/Abir66/mdfly/internal/server/store/postgres"
	r2store "github.com/Abir66/mdfly/internal/server/store/r2"
)

// ViewDeps holds dependencies for the view handler.
type ViewDeps struct {
	PG *pgstore.Store
	R2 *r2store.Store
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
			if errors.Is(err, pgstore.ErrNotFound) {
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
			if errors.Is(err, r2store.ErrBlobMissing) {
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
			return r2store.BlobKey(f.Hash, r2store.ExtFromPath(f.Path)), nil
		}
	}
	return "", fmt.Errorf("root file %q not found in manifest", manifest.Root)
}
