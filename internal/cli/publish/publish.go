package publish

import (
	"fmt"

	"github.com/Abir66/mdfly/internal/api"
	"github.com/Abir66/mdfly/internal/manifest"
	"github.com/google/uuid"
)

const (
	pathInit   = "/v1/publish/init"
	pathCommit = "/v1/publish/commit"
)

// Run orchestrates the 3-phase publish wire for a single file and returns the public URL.
func Run(apiBase, filePath string) (string, error) {
	mfst, content, err := manifest.ForSingleFile(filePath)
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

	initResp, err := postJSON[api.InitResponse](apiBase+pathInit, api.InitRequest{
		IdempotencyKey: idempotencyKey.String(),
		Manifest:       mfst,
		EditToken:      editToken,
	})
	if err != nil {
		return "", fmt.Errorf("publish/init: %w", err)
	}

	for _, h := range initResp.MissingHashes {
		if err := putBlob(initResp.PresignedURLs[h], content, h); err != nil {
			return "", fmt.Errorf("upload blob: %w", err)
		}
	}

	commitResp, err := postJSON[api.CommitResponse](apiBase+pathCommit, api.CommitRequest{
		IdempotencyKey: idempotencyKey.String(),
	})
	if err != nil {
		return "", fmt.Errorf("publish/commit: %w", err)
	}

	return commitResp.URL, nil
}
