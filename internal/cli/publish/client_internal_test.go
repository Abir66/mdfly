package publish

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"time"

	"github.com/Abir66/mdfly/internal/api"
	"github.com/Abir66/mdfly/internal/blobmeta"
	"github.com/Abir66/mdfly/internal/server/storage"
)

// TestPutBlob_sendsSignedMetadata proves the upload carries exactly the headers
// the server signed into the presigned URL. Content-Type and Cache-Control are
// signed, so any drift between the two sides is a 403 at publish time, not a
// cosmetically wrong header — the assertion compares against the server's own
// presign rather than a literal.
func TestPutBlob_sendsSignedMetadata(t *testing.T) {
	const key = "documents/abc123/deadbeef.svg"

	r2 := storage.New(storage.Config{
		Endpoint: "https://example.r2.cloudflarestorage.com", AccessKeyID: "AK",
		SecretAccessKey: "SK", Bucket: "b",
	})
	presigned, err := r2.PresignPUT(context.Background(), key, 4, time.Minute)
	if err != nil {
		t.Fatalf("PresignPUT: %v", err)
	}
	signed, err := url.Parse(presigned)
	if err != nil {
		t.Fatalf("parse presigned URL: %v", err)
	}
	wantSigned := "cache-control;content-length;content-type;host"
	if got := signed.Query().Get("X-Amz-SignedHeaders"); got != wantSigned {
		t.Fatalf("X-Amz-SignedHeaders=%q, want %q", got, wantSigned)
	}

	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
	}))
	defer srv.Close()

	if err := putBlob(context.Background(), srv.Client(), srv.URL, "imgs/logo.svg", []byte("blob")); err != nil {
		t.Fatalf("putBlob: %v", err)
	}
	if want := "image/svg+xml"; got.Get("Content-Type") != want {
		t.Errorf("Content-Type=%q, want %q", got.Get("Content-Type"), want)
	}
	if got.Get("Cache-Control") != blobmeta.CacheControl {
		t.Errorf("Cache-Control=%q, want %q", got.Get("Cache-Control"), blobmeta.CacheControl)
	}
	// The server types the blob by its R2 key and the CLI by the logical path.
	// They are different strings; only their shared extension makes them agree.
	if got.Get("Content-Type") != blobmeta.ContentType(key) {
		t.Errorf("Content-Type=%q disagrees with the server's %q for key %s",
			got.Get("Content-Type"), blobmeta.ContentType(key), key)
	}
}

// TestPostJSON_parsesEnvelope proves a coded 4xx envelope becomes an *APIError
// carrying the status, machine code, human message, and details verbatim.
func TestPostJSON_parsesEnvelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		w.Write([]byte(`{"error":{"code":"slug_taken","message":"slug 'notes' is taken","details":{"slug":"notes"}}}`)) //nolint:errcheck
	}))
	defer srv.Close()

	_, err := postJSON[map[string]any](context.Background(), srv.Client(), srv.URL, map[string]any{})
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err=%v, want *APIError", err)
	}
	if apiErr.Status != http.StatusConflict {
		t.Errorf("status=%d, want 409", apiErr.Status)
	}
	if apiErr.Code != api.CodeSlugTaken {
		t.Errorf("code=%q, want %q", apiErr.Code, api.CodeSlugTaken)
	}
	if apiErr.Message != "slug 'notes' is taken" {
		t.Errorf("message=%q", apiErr.Message)
	}
	if apiErr.Details == nil {
		t.Error("details lost, want the {slug:notes} object preserved")
	}
}

// TestPostJSON_unparseableBody falls back to the raw body as the message while
// still capturing the status.
func TestPostJSON_unparseableBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gateway boom", http.StatusBadGateway)
	}))
	defer srv.Close()

	_, err := postJSON[map[string]any](context.Background(), srv.Client(), srv.URL, map[string]any{})
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err=%v, want *APIError", err)
	}
	if apiErr.Status != http.StatusBadGateway {
		t.Errorf("status=%d, want 502", apiErr.Status)
	}
	if apiErr.Code != "" {
		t.Errorf("code=%q, want empty (no envelope)", apiErr.Code)
	}
}
