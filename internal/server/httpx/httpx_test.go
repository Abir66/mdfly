package httpx_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Abir66/mdfly/internal/api"
	"github.com/Abir66/mdfly/internal/server/httpx"
)

func TestBadRequest_status400(t *testing.T) {
	e := httpx.BadRequest("nope")
	if e.Status != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", e.Status)
	}
	if e.Code != "bad_request" {
		t.Errorf("code=%q, want bad_request", e.Code)
	}
	if e.Msg != "nope" {
		t.Errorf("msg=%q, want nope", e.Msg)
	}
}

func TestNotFound_status404(t *testing.T) {
	e := httpx.NotFound("gone")
	if e.Status != http.StatusNotFound || e.Code != "not_found" {
		t.Errorf("got %d/%s, want 404/not_found", e.Status, e.Code)
	}
}

func TestConflict_customCode(t *testing.T) {
	e := httpx.Conflict("blob_missing", "missing")
	if e.Status != http.StatusConflict || e.Code != "blob_missing" {
		t.Errorf("got %d/%s, want 409/blob_missing", e.Status, e.Code)
	}
}

func TestUnprocessable_status422(t *testing.T) {
	e := httpx.Unprocessable("idempotency_key_payload_mismatch", "collision")
	if e.Status != http.StatusUnprocessableEntity {
		t.Errorf("status=%d, want 422", e.Status)
	}
	if e.Code != "idempotency_key_payload_mismatch" {
		t.Errorf("code=%q, want idempotency_key_payload_mismatch", e.Code)
	}
	if _, ok := e.Details.(map[string]any); !ok {
		t.Errorf("details=%v, want empty map", e.Details)
	}
}

func TestInternal_status500(t *testing.T) {
	e := httpx.Internal("boom")
	if e.Status != http.StatusInternalServerError || e.Code != "internal_error" {
		t.Errorf("got %d/%s, want 500/internal_error", e.Status, e.Code)
	}
}

func TestError_implementsError(t *testing.T) {
	var err error = httpx.BadRequest("x")
	if err.Error() != "x" {
		t.Errorf("Error()=%q, want x", err.Error())
	}
}

func TestWriteError_envelope(t *testing.T) {
	w := httptest.NewRecorder()
	httpx.WriteError(w, httpx.BadRequest("invalid"))

	if w.Code != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type=%q, want application/json", ct)
	}
	var body api.ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Error.Code != "bad_request" || body.Error.Message != "invalid" {
		t.Errorf("body=%+v, want bad_request/invalid", body.Error)
	}
}

func TestWriteJSON_setsContentType(t *testing.T) {
	w := httptest.NewRecorder()
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"k": "v"})

	if w.Code != http.StatusOK {
		t.Errorf("status=%d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type=%q, want application/json", ct)
	}
	var got map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["k"] != "v" {
		t.Errorf("body[k]=%q, want v", got["k"])
	}
}
