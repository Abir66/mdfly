package publish

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Abir66/mdfly/internal/api"
)

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
