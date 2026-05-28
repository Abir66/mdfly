package publish

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"

	"github.com/Abir66/mdfly/internal/markdown"
	"github.com/Abir66/mdfly/internal/server/db"
	"github.com/Abir66/mdfly/internal/server/httpx"
	"github.com/Abir66/mdfly/internal/server/manifest"
	"github.com/Abir66/mdfly/internal/server/storage"
	"github.com/Abir66/mdfly/internal/slug"
)

// buildPresignedURLs returns one presigned PUT URL per unique R2 blobkey in
// mfst, keyed by a representative manifest path. Paths sharing a blobkey are
// covered by the representative's upload.
func buildPresignedURLs(ctx context.Context, r2 *storage.Client, sl string, mfst manifest.Manifest) (map[string]string, *httpx.Error) {
	urls := make(map[string]string, len(mfst.FilesByPath))
	seen := make(map[string]struct{}, len(mfst.FilesByPath))
	for path, f := range mfst.FilesByPath {
		key := storage.BlobKey(sl, f.Hash, storage.ExtFromPath(path))
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		url, err := r2.PresignPUT(ctx, key, f.Size, presignTTL)
		if err != nil {
			return nil, httpx.Internal("failed to generate presigned URLs")
		}
		urls[path] = url
	}
	return urls, nil
}

// verifyBlobs HEAD-checks every manifest blob in R2.
func verifyBlobs(ctx context.Context, r2 *storage.Client, sl string, mfst manifest.Manifest) *httpx.Error {
	for path, f := range mfst.FilesByPath {
		key := storage.BlobKey(sl, f.Hash, storage.ExtFromPath(path))
		if err := r2.HeadBlob(ctx, key, f.Size); err != nil {
			if errors.Is(err, storage.ErrBlobMissing) {
				return &httpx.Error{
					Status: http.StatusConflict,
					Code:   "blob_missing",
					Msg:    fmt.Sprintf("blob %s not found or size mismatch", f.Hash),
				}
			}
			return httpx.Internal("blob verification failed")
		}
	}
	return nil
}

func generateSlug() (string, error) {
	for range slugRetries {
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

// extractPublishMeta fetches the root blob and derives preview metadata from it.
func extractPublishMeta(ctx context.Context, r2 *storage.Client, sl string, mfst manifest.Manifest) (db.PublishMetaParams, *httpx.Error) {
	f, ok := mfst.RootFile()
	if !ok {
		return db.PublishMetaParams{}, httpx.Internal("root file missing from manifest")
	}
	key := storage.BlobKey(sl, f.Hash, storage.ExtFromPath(mfst.RootPath))
	content, err := r2.GetBlob(ctx, key)
	if err != nil {
		return db.PublishMetaParams{}, httpx.Internal("failed to fetch root blob for metadata")
	}

	_, meta, err := markdown.Render(content)
	if err != nil {
		return db.PublishMetaParams{}, httpx.Internal("failed to render markdown for metadata")
	}

	var ogHash []byte
	if meta.OGImagePath != "" {
		if f, ok := mfst.FilesByPath[meta.OGImagePath]; ok {
			decoded, err := hex.DecodeString(f.Hash)
			if err == nil {
				ogHash = decoded
			}
		}
	}

	return db.PublishMetaParams{
		Title:       meta.Title,
		Excerpt:     meta.Excerpt,
		OGImageHash: ogHash,
	}, nil
}
