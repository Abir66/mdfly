package handlers_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Abir66/mdfly/internal/api"
)

// publishWithToken publishes a single-file bundle bound to editToken and returns
// its slug.
func publishWithToken(t *testing.T, srv *httptest.Server, idempKey, rootPath string, content []byte, editToken string) string {
	t.Helper()
	bundle := singleFileBundle(rootPath, content)
	initR := postJSON(t, srv.URL+"/v1/publish/init", api.InitRequest{
		IdempotencyKey: idempKey, Bundle: bundle, EditToken: editToken,
	})
	initBody := decodeInitResponse(t, initR)
	putBlob(t, initBody.PresignedURLs[rootPath], content)
	commitR := postJSON(t, srv.URL+"/v1/publish/commit", api.CommitRequest{IdempotencyKey: idempKey})
	return decodeCommitResponse(t, commitR).Slug
}

// deleteDoc sends DELETE /v1/documents/{slug} with the given bearer token,
// returning the response.
func deleteDoc(t *testing.T, baseURL, slug, token string) *http.Response {
	t.Helper()
	url := baseURL + "/v1/documents/" + slug
	req, err := http.NewRequest(http.MethodDelete, url, nil)
	if err != nil {
		t.Fatalf("build DELETE: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE %s: %v", url, err)
	}
	return resp
}

func TestDelete_validTokenSoftDeletesAndViewGone(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	dsn := startPostgres(t)
	env := startMinio(t)
	srv := newTestServer(t, dsn, env, "https://mdfly.dev")

	const token = "mftk_secrettoken"
	slug := publishWithToken(t, srv, "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeed001", "hi.md", []byte("# Hi\n"), token)

	if status, _ := getString(t, srv.URL+"/"+slug); status != http.StatusOK {
		t.Fatalf("pre-delete view status=%d, want 200", status)
	}

	resp := deleteDoc(t, srv.URL, slug, token)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status=%d, want 204", resp.StatusCode)
	}

	// View path and nested path both 410 for a deleted row.
	if status, _ := getString(t, srv.URL+"/"+slug); status != http.StatusGone {
		t.Errorf("post-delete GET /%s status=%d, want 410", slug, status)
	}
	if status, _ := getString(t, srv.URL+"/"+slug+"/hi.md"); status != http.StatusGone {
		t.Errorf("post-delete GET /%s/hi.md status=%d, want 410", slug, status)
	}
}

func TestDelete_wrongOrMissingTokenRejected(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	dsn := startPostgres(t)
	env := startMinio(t)
	srv := newTestServer(t, dsn, env, "https://mdfly.dev")

	const token = "mftk_secrettoken"
	slug := publishWithToken(t, srv, "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeed002", "hi.md", []byte("# Hi\n"), token)

	// Missing token → 401.
	resp := deleteDoc(t, srv.URL, slug, "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("missing-token status=%d, want 401", resp.StatusCode)
	}
	// Wrong token → 403.
	resp = deleteDoc(t, srv.URL, slug, "mftk_wrong")
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("wrong-token status=%d, want 403", resp.StatusCode)
	}
	// Both carry the shared invalid_edit_token code, and the doc is untouched.
	if status, _ := getString(t, srv.URL+"/"+slug); status != http.StatusOK {
		t.Errorf("doc must survive a rejected delete: view status=%d, want 200", status)
	}
}

func TestDelete_idempotentOnAlreadyDeleted(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	dsn := startPostgres(t)
	env := startMinio(t)
	srv := newTestServer(t, dsn, env, "https://mdfly.dev")

	const token = "mftk_secrettoken"
	slug := publishWithToken(t, srv, "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeed003", "hi.md", []byte("# Hi\n"), token)

	for i := range 2 {
		resp := deleteDoc(t, srv.URL, slug, token)
		resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("delete #%d status=%d, want 204 (idempotent)", i+1, resp.StatusCode)
		}
	}
}

// TestDelete_terminalStatuses covers the two GC-produced states: an 'abandoned'
// row was never published so it is indistinguishable from missing (404), while
// an 'expired' row is already gone and deleting it is a no-op success.
func TestDelete_terminalStatuses(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	dsn := startPostgres(t)
	env := startMinio(t)
	srv := newTestServer(t, dsn, env, "https://mdfly.dev")

	const token = "mftk_secrettoken"
	slug := publishWithToken(t, srv, "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeed004", "hi.md", []byte("# Hi\n"), token)

	setDocumentStatus(t, dsn, slug, "abandoned")
	resp := deleteDoc(t, srv.URL, slug, token)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("delete abandoned status=%d, want 404", resp.StatusCode)
	}

	setDocumentStatus(t, dsn, slug, "expired")
	resp = deleteDoc(t, srv.URL, slug, token)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("delete expired status=%d, want 204", resp.StatusCode)
	}
}

func TestDelete_missingSlugIs404(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	dsn := startPostgres(t)
	env := startMinio(t)
	srv := newTestServer(t, dsn, env, "https://mdfly.dev")

	resp := deleteDoc(t, srv.URL, "neverexisted99", "mftk_whatever")
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("delete missing slug status=%d, want 404", resp.StatusCode)
	}
}
