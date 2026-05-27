package publish

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/Abir66/mdfly/internal/api"
	"github.com/google/uuid"
)

const (
	pathInit   = "/v1/publish/init"
	pathCommit = "/v1/publish/commit"

	httpClientTimeout = 30 * time.Second
)

// Run orchestrates the 3-phase publish wire for a single file and returns the public URL.
func Run(apiBase, filePath string) (string, error) {
	bundle, err := ForSingleFile(filePath)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}

	idempotencyKey, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("generate idempotency key: %w", err)
	}

	editToken, err := newEditToken()
	if err != nil {
		return "", fmt.Errorf("generate edit token: %w", err)
	}

	ctx := context.Background()
	client := &http.Client{Timeout: httpClientTimeout}

	initURL, err := url.JoinPath(apiBase, pathInit)
	if err != nil {
		return "", fmt.Errorf("build init URL: %w", err)
	}
	initResp, err := postJSON[api.InitResponse](ctx, client, initURL, api.InitRequest{
		IdempotencyKey: idempotencyKey.String(),
		Bundle:         bundle.ToDTO(),
		EditToken:      editToken,
	})
	if err != nil {
		return "", fmt.Errorf("publish/init: %w", err)
	}

	for hash, presignedURL := range initResp.PresignedURLs {
		f, ok := bundle.FilesByHash[hash]
		if !ok {
			return "", fmt.Errorf("presigned URL for unknown hash %s", hash)
		}
		content := f.Content
		if content == nil {
			content, err = os.ReadFile(f.DiskPath)
			if err != nil {
				return "", fmt.Errorf("read file %s: %w", f.Path, err)
			}
		}
		if err := putBlob(ctx, client, presignedURL, content); err != nil {
			return "", fmt.Errorf("upload blob: %w", err)
		}
	}

	commitURL, err := url.JoinPath(apiBase, pathCommit)
	if err != nil {
		return "", fmt.Errorf("build commit URL: %w", err)
	}
	commitResp, err := postJSON[api.CommitResponse](ctx, client, commitURL, api.CommitRequest{
		IdempotencyKey: idempotencyKey.String(),
	})
	if err != nil {
		return "", fmt.Errorf("publish/commit: %w", err)
	}

	return commitResp.URL, nil
}
