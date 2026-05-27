package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/Abir66/mdfly/internal/api"
	"github.com/Abir66/mdfly/internal/server/db"
	"github.com/Abir66/mdfly/internal/server/storage"
	"github.com/Abir66/mdfly/internal/slug"
)

const (
	presignTTL    = 10 * time.Minute
	anonExpiresIn = 30 * 24 * time.Hour
)

// PublishDeps holds the dependencies injected into publish handlers.
type PublishDeps struct {
	PG      *db.Client
	R2      *storage.Client
	BaseURL string // e.g. "https://mdfly.dev" — used to construct the returned URL
}

// Init handles POST /v1/publish/init.
func Init(deps PublishDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req api.InitRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
			return
		}
		if req.IdempotencyKey == "" {
			writeError(w, http.StatusBadRequest, "bad_request", "idempotency_key required")
			return
		}
		if req.Manifest.Root == "" || len(req.Manifest.Files) == 0 {
			writeError(w, http.StatusBadRequest, "bad_request", "manifest.root and manifest.files required")
			return
		}
		for _, f := range req.Manifest.Files {
			if f.Path == "" {
				writeError(w, http.StatusBadRequest, "bad_request", "manifest file path required")
				return
			}
			if f.Hash == "" {
				writeError(w, http.StatusBadRequest, "bad_request", "manifest file hash required")
				return
			}
			if f.Size < 0 {
				writeError(w, http.StatusBadRequest, "bad_request", "manifest file size must be non-negative")
				return
			}
		}

		sl, err := generateSlug()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "slug generation failed")
			return
		}

		doc, err := deps.PG.InsertPending(r.Context(), db.InsertPendingParams{
			Slug:           sl,
			IdempotencyKey: req.IdempotencyKey,
			Manifest:       req.Manifest,
			EditToken:      req.EditToken,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "failed to create document")
			return
		}

		missingHashes, presignedURLs, err := buildPresignedURLs(r.Context(), deps.R2, req.Manifest)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "failed to generate presigned URLs")
			return
		}

		writeJSON(w, http.StatusOK, api.InitResponse{
			Slug:          doc.Slug,
			MissingHashes: missingHashes,
			PresignedURLs: presignedURLs,
			InlineAccept:  false,
		})
	}
}

// Commit handles POST /v1/publish/commit.
func Commit(deps PublishDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req api.CommitRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
			return
		}
		if req.IdempotencyKey == "" {
			writeError(w, http.StatusBadRequest, "bad_request", "idempotency_key required")
			return
		}

		doc, err := deps.PG.GetByIdempotencyKey(r.Context(), req.IdempotencyKey)
		if err != nil {
			if errors.Is(err, db.ErrNotFound) {
				writeError(w, http.StatusNotFound, "not_found", "no document for idempotency_key")
				return
			}
			writeError(w, http.StatusInternalServerError, "internal_error", "failed to fetch document")
			return
		}

		// Terminal-state idempotency: already published → return success.
		if doc.Status == db.StatusPublished {
			writeJSON(w, http.StatusOK, api.CommitResponse{
				URL:          documentURL(deps.BaseURL, doc.Slug),
				Slug:         doc.Slug,
				ManifestHash: doc.ManifestHashHex(),
			})
			return
		}

		// HEAD-verify every blob exists in R2.
		var manifest api.Manifest
		if err := json.Unmarshal(doc.ManifestJSON, &manifest); err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "corrupt manifest")
			return
		}
		for _, f := range manifest.Files {
			key := storage.BlobKey(f.Hash, storage.ExtFromPath(f.Path))
			if err := deps.R2.HeadBlob(r.Context(), key, f.Size); err != nil {
				if errors.Is(err, storage.ErrBlobMissing) {
					writeError(w, http.StatusConflict, "blob_missing",
						fmt.Sprintf("blob %s not found or size mismatch", f.Hash))
					return
				}
				writeError(w, http.StatusInternalServerError, "internal_error", "blob verification failed")
				return
			}
		}

		expiresAt := time.Now().Add(anonExpiresIn)
		published, err := deps.PG.Publish(r.Context(), req.IdempotencyKey, expiresAt)
		if err != nil {
			if errors.Is(err, db.ErrNotFound) {
				writeError(w, http.StatusNotFound, "not_found", "no pending document for idempotency_key")
				return
			}
			writeError(w, http.StatusInternalServerError, "internal_error", "failed to publish document")
			return
		}

		writeJSON(w, http.StatusOK, api.CommitResponse{
			URL:          documentURL(deps.BaseURL, published.Slug),
			Slug:         published.Slug,
			ManifestHash: published.ManifestHashHex(),
		})
	}
}

// buildPresignedURLs HEAD-checks each manifest file against R2 and returns
// the list of missing hashes plus a presigned URL for each missing blob.
func buildPresignedURLs(ctx context.Context, r2 *storage.Client, manifest api.Manifest) ([]string, map[string]string, error) {
	var missingHashes []string
	presignedURLs := make(map[string]string)

	for _, f := range manifest.Files {
		key := storage.BlobKey(f.Hash, storage.ExtFromPath(f.Path))
		err := r2.HeadBlob(ctx, key, f.Size)
		if err != nil {
			if !errors.Is(err, storage.ErrBlobMissing) {
				return nil, nil, fmt.Errorf("head blob %s: %w", f.Hash, err)
			}
			url, err := r2.PresignPUT(ctx, key, f.Size, f.Hash, presignTTL)
			if err != nil {
				return nil, nil, fmt.Errorf("presign %s: %w", f.Hash, err)
			}
			missingHashes = append(missingHashes, f.Hash)
			presignedURLs[f.Hash] = url
		}
	}

	if missingHashes == nil {
		missingHashes = []string{}
	}

	return missingHashes, presignedURLs, nil
}

func generateSlug() (string, error) {
	for i := 0; i < 10; i++ {
		s, err := slug.Generate()
		if err != nil {
			return "", err
		}
		if !slug.IsReserved(s) {
			return s, nil
		}
	}
	return "", fmt.Errorf("slug generation: exhausted retries")
}

func documentURL(base, sl string) string {
	return base + "/" + sl
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, api.ErrorResponse{
		Error: api.ErrorBody{Code: code, Message: msg},
	})
}
