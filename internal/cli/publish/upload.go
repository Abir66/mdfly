package publish

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"

	"golang.org/x/sync/errgroup"
)

// uploadConcurrency bounds the parallel presigned-PUT pool (PRD CLI Anon-Tier
// §publish). init and commit stay sequential; only the upload phase fans out.
const uploadConcurrency = 6

// uploadBlobs uploads every presigned blob through a bounded parallel pool.
// Each blob keeps its own retry so a flaky blob does not stall the pool; the
// first upload error cancels the group (first-error-wins) and is returned.
// Upload order is nondeterministic, so the returned error never depends on it.
// When progress is non-nil an "uploaded N/M" line is written per completed blob.
func uploadBlobs(ctx context.Context, client *http.Client, bundle Bundle, presigned map[string]string, progress io.Writer) error {
	total := len(presigned)
	for path := range presigned {
		if _, ok := bundle.FilesByPath[path]; !ok {
			return fmt.Errorf("presigned URL for unknown path %s", path)
		}
	}

	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(uploadConcurrency)

	var (
		progressMu sync.Mutex
		done       int
	)
	for path, presignedURL := range presigned {
		f := bundle.FilesByPath[path]
		g.Go(func() error {
			content, err := blobContent(f)
			if err != nil {
				return err
			}
			if _, err := withRetry(ctx, func() (struct{}, error) {
				return struct{}{}, putBlob(ctx, client, presignedURL, content)
			}); err != nil {
				return fmt.Errorf("upload blob %s: %w", f.Path, err)
			}
			if progress != nil {
				progressMu.Lock()
				done++
				fmt.Fprintf(progress, "uploaded %d/%d\n", done, total)
				progressMu.Unlock()
			}
			return nil
		})
	}
	return g.Wait()
}

// blobContent returns f's bytes, using the preloaded content for small files
// and re-reading from disk otherwise.
func blobContent(f BundleFile) ([]byte, error) {
	if f.Content != nil {
		return f.Content, nil
	}
	content, err := os.ReadFile(f.DiskPath)
	if err != nil {
		return nil, fmt.Errorf("read file %s: %w", f.Path, err)
	}
	return content, nil
}
