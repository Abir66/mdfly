package publish

import (
	"encoding/hex"

	"github.com/Abir66/mdfly/internal/api"
	"github.com/Abir66/mdfly/internal/server/httpx"
)

// ValidateInit checks that an InitRequest is structurally sound. Returns nil
// on success or a *httpx.Error the caller can write directly.
func ValidateInit(req api.InitRequest) *httpx.Error {
	if req.IdempotencyKey == "" {
		return httpx.BadRequest("idempotency_key required")
	}
	return validateBundle(req.Bundle)
}

// ValidateCommit checks that a CommitRequest carries the required key.
func ValidateCommit(req api.CommitRequest) *httpx.Error {
	if req.IdempotencyKey == "" {
		return httpx.BadRequest("idempotency_key required")
	}
	return nil
}

func validateBundle(b api.BundleDTO) *httpx.Error {
	if b.RootPath == "" || len(b.Files) == 0 {
		return httpx.BadRequest("bundle.root_path and bundle.files required")
	}
	seenRoot := false
	for _, f := range b.Files {
		if f.Hash == "" {
			return httpx.BadRequest("bundle file hash required")
		}
		if len(f.Hash) != 64 {
			return httpx.BadRequest("bundle file hash must be 64-char hex SHA256")
		}
		if _, err := hex.DecodeString(f.Hash); err != nil {
			return httpx.BadRequest("bundle file hash must be valid hex SHA256")
		}
		if f.Path == "" {
			return httpx.BadRequest("bundle file path required")
		}
		if f.Size < 0 {
			return httpx.BadRequest("bundle file size must be non-negative")
		}
		if f.Path == b.RootPath {
			seenRoot = true
		}
	}
	if !seenRoot {
		return httpx.BadRequest("bundle.root_path not present in bundle.files")
	}
	return nil
}
