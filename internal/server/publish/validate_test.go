package publish_test

import (
	"net/http"
	"testing"

	"github.com/Abir66/mdfly/internal/api"
	"github.com/Abir66/mdfly/internal/server/publish"
)

func validReq() api.InitRequest {
	return api.InitRequest{
		IdempotencyKey: "550e8400-e29b-41d4-a716-446655440000",
		Bundle: api.BundleDTO{
			RootPath: "hello.md",
			Files: []api.BundleFileDTO{
				{Path: "hello.md", Hash: "abc", Size: 42},
			},
		},
	}
}

func TestValidateInit_valid(t *testing.T) {
	if err := publish.ValidateInit(validReq()); err != nil {
		t.Fatalf("valid request rejected: %+v", err)
	}
}

func TestValidateInit_missingIdempotencyKey(t *testing.T) {
	req := validReq()
	req.IdempotencyKey = ""
	err := publish.ValidateInit(req)
	if err == nil {
		t.Fatal("expected error for empty idempotency_key")
	}
	if err.Status != http.StatusBadRequest || err.Code != "bad_request" {
		t.Errorf("got %d/%s, want 400/bad_request", err.Status, err.Code)
	}
}

func TestValidateInit_emptyRootPath(t *testing.T) {
	req := validReq()
	req.Bundle.RootPath = ""
	err := publish.ValidateInit(req)
	if err == nil || err.Status != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty root_path, got %+v", err)
	}
}

func TestValidateInit_emptyFiles(t *testing.T) {
	req := validReq()
	req.Bundle.Files = nil
	err := publish.ValidateInit(req)
	if err == nil || err.Status != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty files, got %+v", err)
	}
}

func TestValidateInit_fileMissingHash(t *testing.T) {
	req := validReq()
	req.Bundle.Files[0].Hash = ""
	err := publish.ValidateInit(req)
	if err == nil || err.Status != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing hash, got %+v", err)
	}
}

func TestValidateInit_fileMissingPath(t *testing.T) {
	req := validReq()
	req.Bundle.Files[0].Path = ""
	err := publish.ValidateInit(req)
	if err == nil || err.Status != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing path, got %+v", err)
	}
}

func TestValidateInit_rootPathNotInFiles(t *testing.T) {
	req := api.InitRequest{
		IdempotencyKey: "550e8400-e29b-41d4-a716-446655440000",
		Bundle: api.BundleDTO{
			RootPath: "missing.md",
			Files: []api.BundleFileDTO{
				{Path: "other.md", Hash: "abc", Size: 1},
			},
		},
	}
	err := publish.ValidateInit(req)
	if err == nil || err.Status != http.StatusBadRequest {
		t.Fatalf("expected 400 for root_path not in files, got %+v", err)
	}
}

func TestValidateInit_fileNegativeSize(t *testing.T) {
	req := validReq()
	req.Bundle.Files[0].Size = -1
	err := publish.ValidateInit(req)
	if err == nil || err.Status != http.StatusBadRequest {
		t.Fatalf("expected 400 for negative size, got %+v", err)
	}
}

func TestValidateCommit_missingIdempotencyKey(t *testing.T) {
	err := publish.ValidateCommit(api.CommitRequest{})
	if err == nil || err.Status != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing idempotency_key, got %+v", err)
	}
}

func TestValidateCommit_valid(t *testing.T) {
	err := publish.ValidateCommit(api.CommitRequest{IdempotencyKey: "k"})
	if err != nil {
		t.Fatalf("valid commit rejected: %+v", err)
	}
}
