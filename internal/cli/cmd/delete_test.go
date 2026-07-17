package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Abir66/mdfly/internal/api"
	"github.com/Abir66/mdfly/internal/cli/localstate"
	"github.com/Abir66/mdfly/internal/cli/output"
)

// deleteMock serves DELETE /v1/documents/{slug}, recording each call so tests
// can assert the auth header, parent-hash query, and call count.
type deleteMock struct {
	srv      *httptest.Server
	mu       sync.Mutex
	calls    int
	lastAuth string
	status   int    // response status (default 204)
	code     string // error-envelope code for non-2xx
}

func newDeleteMock(t *testing.T) *deleteMock {
	t.Helper()
	m := &deleteMock{status: http.StatusNoContent}
	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /v1/documents/{slug}", func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		defer m.mu.Unlock()
		m.calls++
		m.lastAuth = r.Header.Get("Authorization")
		if m.status == http.StatusNoContent {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(m.status)
		json.NewEncoder(w).Encode(api.ErrorResponse{ //nolint:errcheck
			Error: api.ErrorBody{Code: m.code, Message: "server says no"},
		})
	})
	m.srv = httptest.NewServer(mux)
	t.Cleanup(m.srv.Close)
	return m
}

func seedRecord(t *testing.T, stateDir, slug, token, manifestHash string, path *string) {
	t.Helper()
	store := localstate.New(stateDir)
	rec := localstate.Record{
		Slug: slug, URL: "https://mdfly.dev/" + slug, ManifestHash: manifestHash,
		Tier: "anon", Source: localstate.SourceText, Path: path,
	}
	if path != nil {
		rec.Source = localstate.SourceFile
	}
	if err := store.Upsert(rec); err != nil {
		t.Fatalf("seed record: %v", err)
	}
	if token != "" {
		if err := store.SaveToken(slug, token); err != nil {
			t.Fatalf("seed token: %v", err)
		}
	}
}

func executeIn(t *testing.T, stateDir string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	t.Setenv("MDFLY_CONFIG_DIR", stateDir)
	root := NewRootCmd("test-1.2.3")
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetIn(&bytes.Buffer{}) // non-terminal stdin: deterministic non-interactive
	root.SetArgs(args)
	err = root.Execute()
	return out.String(), errOut.String(), err
}

func recordExists(t *testing.T, stateDir, slug string) bool {
	t.Helper()
	ledger, err := localstate.New(stateDir).Load()
	if err != nil {
		t.Fatalf("load ledger: %v", err)
	}
	_, ok := ledger.Get(slug)
	return ok
}

func TestDelete_bySlug_prunesAndSendsAuthAndHash(t *testing.T) {
	dir := t.TempDir()
	seedRecord(t, dir, "s1", "mftk_tok", "mh1", nil)
	m := newDeleteMock(t)

	out, _, err := executeIn(t, dir, "--api", m.srv.URL, "delete", "s1", "-y")
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if out != "Deleted: s1\n" {
		t.Errorf("stdout=%q, want \"Deleted: s1\\n\"", out)
	}
	if m.calls != 1 {
		t.Errorf("server calls=%d, want 1", m.calls)
	}
	if m.lastAuth != "Bearer mftk_tok" {
		t.Errorf("auth header=%q, want Bearer mftk_tok", m.lastAuth)
	}
	if recordExists(t, dir, "s1") {
		t.Error("record must be pruned after delete")
	}
}

func TestDelete_byFile_resolvesSingleSlug(t *testing.T) {
	dir := t.TempDir()
	abs := filepath.Join(dir, "notes.md")
	seedRecord(t, dir, "s1", "mftk_tok", "mh1", &abs)
	m := newDeleteMock(t)

	out, _, err := executeIn(t, dir, "--api", m.srv.URL, "delete", abs, "-y")
	if err != nil {
		t.Fatalf("delete by file: %v", err)
	}
	if out != "Deleted: s1\n" {
		t.Errorf("stdout=%q, want Deleted: s1", out)
	}
	if m.calls != 1 {
		t.Errorf("server calls=%d, want 1", m.calls)
	}
}

func TestDelete_byFile_ambiguousNeedsSlug(t *testing.T) {
	dir := t.TempDir()
	abs := filepath.Join(dir, "notes.md")
	seedRecord(t, dir, "s1", "mftk_a", "mh1", &abs)
	seedRecord(t, dir, "s2", "mftk_b", "mh2", &abs)
	m := newDeleteMock(t)

	_, _, err := executeIn(t, dir, "--api", m.srv.URL, "delete", abs, "-y")
	if err == nil {
		t.Fatal("ambiguous file should error")
	}
	if output.ExitCode(err) != output.ExitUsage {
		t.Errorf("exit=%d, want %d (usage)", output.ExitCode(err), output.ExitUsage)
	}
	if m.calls != 0 {
		t.Errorf("ambiguous delete must not call server; calls=%d", m.calls)
	}

	// --slug disambiguates.
	if _, _, err := executeIn(t, dir, "--api", m.srv.URL, "delete", abs, "--slug", "s2", "-y"); err != nil {
		t.Fatalf("delete --slug s2: %v", err)
	}
}

func TestDelete_nonTTYWithoutYesRefuses_noServerCall(t *testing.T) {
	dir := t.TempDir()
	seedRecord(t, dir, "s1", "mftk_tok", "mh1", nil)
	m := newDeleteMock(t)

	_, _, err := executeIn(t, dir, "--api", m.srv.URL, "delete", "s1")
	if err == nil {
		t.Fatal("non-TTY delete without -y should error")
	}
	if output.ExitCode(err) != output.ExitUsage {
		t.Errorf("exit=%d, want %d (usage)", output.ExitCode(err), output.ExitUsage)
	}
	if m.calls != 0 {
		t.Errorf("refused delete must not call server; calls=%d", m.calls)
	}
	if !recordExists(t, dir, "s1") {
		t.Error("refused delete must not prune the record")
	}
}

func TestDelete_serverGonePrunesAndSucceeds(t *testing.T) {
	dir := t.TempDir()
	seedRecord(t, dir, "s1", "mftk_tok", "mh1", nil)
	m := newDeleteMock(t)
	m.status = http.StatusGone
	m.code = api.CodeGone

	out, errOut, err := executeIn(t, dir, "--api", m.srv.URL, "delete", "s1", "-y")
	if err != nil {
		t.Fatalf("gone delete should self-heal, got err: %v", err)
	}
	if out != "Deleted: s1\n" {
		t.Errorf("stdout=%q, want Deleted: s1", out)
	}
	if !bytesContains(errOut, "already gone") {
		t.Errorf("stderr should note it was already gone: %q", errOut)
	}
	if recordExists(t, dir, "s1") {
		t.Error("record must be pruned on 410 self-heal")
	}
}

func TestDelete_notInStateAttemptsServerDelete(t *testing.T) {
	dir := t.TempDir() // empty state
	m := newDeleteMock(t)

	out, _, err := executeIn(t, dir, "--api", m.srv.URL, "delete", "orphan", "-y")
	if err != nil {
		t.Fatalf("recovery delete: %v", err)
	}
	if out != "Deleted: orphan\n" {
		t.Errorf("stdout=%q, want Deleted: orphan", out)
	}
	if m.calls != 1 {
		t.Errorf("recovery must attempt server delete; calls=%d", m.calls)
	}
}

func TestDelete_authErrorDoesNotPrune(t *testing.T) {
	dir := t.TempDir()
	seedRecord(t, dir, "s1", "mftk_tok", "mh1", nil)
	m := newDeleteMock(t)
	m.status = http.StatusForbidden
	m.code = api.CodeInvalidEditToken

	_, _, err := executeIn(t, dir, "--api", m.srv.URL, "delete", "s1", "-y")
	if err == nil {
		t.Fatal("403 should surface as an error")
	}
	if output.ExitCode(err) != output.ExitAuth {
		t.Errorf("exit=%d, want %d (auth)", output.ExitCode(err), output.ExitAuth)
	}
	if !recordExists(t, dir, "s1") {
		t.Error("auth failure must not prune the record")
	}
}

func TestDelete_jsonOutput(t *testing.T) {
	dir := t.TempDir()
	seedRecord(t, dir, "s1", "mftk_tok", "mh1", nil)
	m := newDeleteMock(t)

	out, _, err := executeIn(t, dir, "--json", "--api", m.srv.URL, "delete", "s1", "-y")
	if err != nil {
		t.Fatalf("delete --json: %v", err)
	}
	var obj struct {
		Slug string `json:"slug"`
	}
	if err := json.Unmarshal([]byte(out), &obj); err != nil {
		t.Fatalf("stdout not JSON: %v\n%s", err, out)
	}
	if obj.Slug != "s1" {
		t.Errorf("json slug=%q, want s1", obj.Slug)
	}
}

func bytesContains(s, sub string) bool {
	return bytes.Contains([]byte(s), []byte(sub))
}
