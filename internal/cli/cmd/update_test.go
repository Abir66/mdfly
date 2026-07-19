package cmd

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Abir66/mdfly/internal/api"
	"github.com/Abir66/mdfly/internal/cli/localstate"
	"github.com/Abir66/mdfly/internal/cli/output"
)

// updateMock serves the update flow for a single-file bundle, echoing a fixed
// slug and a caller-chosen committed manifest hash.
func updateMock(t *testing.T, rootPath, slug, newHash string) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/update/init", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, api.UpdateInitResponse{PresignedURLs: map[string]string{rootPath: srv.URL + "/blob"}})
	})
	mux.HandleFunc("PUT /blob", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("POST /v1/update/commit", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, api.UpdateCommitResponse{URL: srv.URL + "/" + slug, Slug: slug, ManifestHash: newHash})
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// errEnvelopeMock serves a single status+code error envelope on update/init.
func errEnvelopeMock(t *testing.T, status int, code string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/update/init", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		writeJSON(w, api.ErrorResponse{Error: api.ErrorBody{Code: code, Message: "server says no"}})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestUpdate_oneSlugFileInPlace(t *testing.T) {
	dir := t.TempDir()
	mdFile := filepath.Join(dir, "doc.md")
	if err := os.WriteFile(mdFile, []byte("# New\n"), 0644); err != nil {
		t.Fatal(err)
	}
	abs, _ := filepath.Abs(mdFile)
	srv := updateMock(t, "doc.md", "s1", "newhash")
	seedRecord(t, dir, "s1", "mftk_seed", "oldhash", &abs)

	out, errOut, err := executeIn(t, dir, "--api", srv.URL, "update", mdFile)
	if err != nil {
		t.Fatalf("update: %v (stderr=%s)", err, errOut)
	}
	if strings.TrimSpace(out) != srv.URL+"/s1" {
		t.Errorf("stdout=%q, want %s/s1", out, srv.URL)
	}

	ledger, _ := localstate.New(dir).Load()
	rec, ok := ledger.Get("s1")
	if !ok || rec.ManifestHash != "newhash" {
		t.Errorf("record not updated: %+v ok=%v", rec, ok)
	}
}

func TestUpdate_textUpdateBySlug(t *testing.T) {
	dir := t.TempDir()
	srv := updateMock(t, "index.md", "s2", "texthash")
	seedRecord(t, dir, "s2", "mftk_seed", "old", nil) // text record, no path

	out, errOut, err := executeIn(t, dir, "--api", srv.URL, "update", "--slug", "s2", "-m", "# Inline\n")
	if err != nil {
		t.Fatalf("text update: %v (stderr=%s)", err, errOut)
	}
	if strings.TrimSpace(out) != srv.URL+"/s2" {
		t.Errorf("stdout=%q, want %s/s2", out, srv.URL)
	}
	ledger, _ := localstate.New(dir).Load()
	if rec, _ := ledger.Get("s2"); rec.Source != localstate.SourceText || rec.Path != nil {
		t.Errorf("text update should clear path: %+v", rec)
	}
}

func TestUpdate_renamedSourceReattachesPath(t *testing.T) {
	dir := t.TempDir()
	renamed := filepath.Join(dir, "renamed.md")
	if err := os.WriteFile(renamed, []byte("# Moved\n"), 0644); err != nil {
		t.Fatal(err)
	}
	newAbs, _ := filepath.Abs(renamed)
	oldPath := filepath.Join(dir, "old-name.md")
	srv := updateMock(t, "renamed.md", "s3", "mh3")
	seedRecord(t, dir, "s3", "mftk_seed", "mh0", &oldPath)

	if _, errOut, err := executeIn(t, dir, "--api", srv.URL, "update", "--slug", "s3", renamed); err != nil {
		t.Fatalf("renamed update: %v (stderr=%s)", err, errOut)
	}
	ledger, _ := localstate.New(dir).Load()
	rec, _ := ledger.Get("s3")
	if rec.Path == nil || *rec.Path != newAbs {
		t.Errorf("path not re-attached: got %v, want %s", rec.Path, newAbs)
	}
}

func TestUpdate_conflictSurfaces(t *testing.T) {
	dir := t.TempDir()
	mdFile := filepath.Join(dir, "doc.md")
	if err := os.WriteFile(mdFile, []byte("# C\n"), 0644); err != nil {
		t.Fatal(err)
	}
	abs, _ := filepath.Abs(mdFile)
	srv := errEnvelopeMock(t, http.StatusConflict, api.CodeUpdateConflict)
	seedRecord(t, dir, "s4", "mftk_seed", "stale", &abs)

	_, _, err := executeIn(t, dir, "--api", srv.URL, "update", mdFile)
	if !isUpdateConflict(err) {
		t.Fatalf("want update conflict, got %v", err)
	}
}

func TestUpdate_goneSlugPrunesRecord(t *testing.T) {
	dir := t.TempDir()
	mdFile := filepath.Join(dir, "doc.md")
	if err := os.WriteFile(mdFile, []byte("# G\n"), 0644); err != nil {
		t.Fatal(err)
	}
	abs, _ := filepath.Abs(mdFile)
	srv := errEnvelopeMock(t, http.StatusGone, api.CodeGone)
	seedRecord(t, dir, "s5", "mftk_seed", "mh", &abs)

	if _, _, err := executeIn(t, dir, "--api", srv.URL, "update", mdFile); err == nil {
		t.Fatal("gone slug should error")
	}
	if recordExists(t, dir, "s5") {
		t.Error("gone slug should prune the local record")
	}
}

func TestUpdate_zeroSlugHintsPublish(t *testing.T) {
	dir := t.TempDir()
	mdFile := filepath.Join(dir, "untracked.md")
	if err := os.WriteFile(mdFile, []byte("# U\n"), 0644); err != nil {
		t.Fatal(err)
	}

	_, _, err := executeIn(t, dir, "update", mdFile)
	var usageErr *output.UsageError
	if !errors.As(err, &usageErr) || !strings.Contains(err.Error(), "publish") {
		t.Fatalf("want a publish-hint usage error, got %v", err)
	}
}

func TestUpdate_noFileNoSlugIsUsageError(t *testing.T) {
	dir := t.TempDir()
	_, _, err := executeIn(t, dir, "update")
	var usageErr *output.UsageError
	if !errors.As(err, &usageErr) {
		t.Fatalf("want a usage error, got %v", err)
	}
}
