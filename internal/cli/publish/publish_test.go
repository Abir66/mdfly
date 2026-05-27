package publish_test

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Abir66/mdfly/internal/api"
	"github.com/Abir66/mdfly/internal/cli/publish"
)

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// mockServer builds an httptest.Server that handles the 3-phase publish flow.
// It stores the uploaded blob so the commit handler can verify it.
func mockServer(t *testing.T, content []byte) *httptest.Server {
	t.Helper()
	hash := sha256Hex(content)
	var blobUploaded bool
	var srv *httptest.Server

	mux := http.NewServeMux()

	mux.HandleFunc("POST /v1/publish/init", func(w http.ResponseWriter, r *http.Request) {
		var req api.InitRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		// presigned PUT goes to /blob/<hash> on this same server
		presignedURL := srv.URL + "/blob/" + hash
		writeJSON(w, api.InitResponse{
			Slug:          "testslug1",
			MissingHashes: []string{hash},
			PresignedURLs: map[string]string{hash: presignedURL},
		})
	})

	mux.HandleFunc("PUT /blob/", func(w http.ResponseWriter, r *http.Request) {
		// Verify Content-Length and checksum headers are present
		if r.ContentLength <= 0 {
			http.Error(w, "missing Content-Length", http.StatusBadRequest)
			return
		}
		if r.Header.Get("x-amz-checksum-sha256") == "" {
			http.Error(w, "missing checksum header", http.StatusBadRequest)
			return
		}
		// Verify checksum matches content
		body, _ := io.ReadAll(r.Body)
		sum := sha256.Sum256(body)
		gotB64 := base64.StdEncoding.EncodeToString(sum[:])
		if r.Header.Get("x-amz-checksum-sha256") != gotB64 {
			http.Error(w, "checksum mismatch", http.StatusBadRequest)
			return
		}
		blobUploaded = true
		w.WriteHeader(http.StatusOK)
	})

	mux.HandleFunc("POST /v1/publish/commit", func(w http.ResponseWriter, r *http.Request) {
		if !blobUploaded {
			http.Error(w, "blob not uploaded", http.StatusConflict)
			return
		}
		var req api.CommitRequest
		json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck
		writeJSON(w, api.CommitResponse{
			URL:  srv.URL + "/" + "testslug1",
			Slug: "testslug1",
		})
	})

	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}

func TestRun_happyPath(t *testing.T) {
	content := []byte("# Hello\n\nTest document.\n")
	dir := t.TempDir()
	mdFile := filepath.Join(dir, "hello.md")
	if err := os.WriteFile(mdFile, content, 0644); err != nil {
		t.Fatal(err)
	}

	srv := mockServer(t, content)

	url, err := publish.Run(srv.URL, mdFile)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(url, "testslug1") {
		t.Errorf("URL=%q, want it to contain testslug1", url)
	}
}

func TestRun_fileNotFound(t *testing.T) {
	_, err := publish.Run("http://localhost:9999", "/nonexistent/file.md")
	if err == nil {
		t.Error("want error for nonexistent file, got nil")
	}
}

func TestRun_initError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"code":"internal_error","message":"server boom"}}`, http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	mdFile := filepath.Join(dir, "test.md")
	os.WriteFile(mdFile, []byte("content"), 0644) //nolint:errcheck

	_, err := publish.Run(srv.URL, mdFile)
	if err == nil {
		t.Error("want error on server 500, got nil")
	}
}

func TestRun_sendsEditToken(t *testing.T) {
	content := []byte("# Token test\n")
	dir := t.TempDir()
	mdFile := filepath.Join(dir, "t.md")
	os.WriteFile(mdFile, content, 0644) //nolint:errcheck

	var gotToken string
	var srv *httptest.Server
	hash := sha256Hex(content)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/publish/init", func(w http.ResponseWriter, r *http.Request) {
		var req api.InitRequest
		json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck
		gotToken = req.EditToken
		writeJSON(w, api.InitResponse{
			Slug:          "tok1",
			MissingHashes: []string{hash},
			PresignedURLs: map[string]string{hash: srv.URL + "/blob/" + hash},
		})
	})
	mux.HandleFunc("PUT /blob/", func(w http.ResponseWriter, r *http.Request) {
		io.ReadAll(r.Body) //nolint:errcheck
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("POST /v1/publish/commit", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, api.CommitResponse{URL: srv.URL + "/tok1", Slug: "tok1"})
	})

	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	if _, err := publish.Run(srv.URL, mdFile); err != nil {
		t.Fatalf("publish.Run failed: %v", err)
	}

	if !strings.HasPrefix(gotToken, "mftk_") {
		t.Errorf("edit_token=%q, want mftk_ prefix", gotToken)
	}
}
