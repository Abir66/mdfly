package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Abir66/mdfly/internal/cli/localstate"
)

// noNetMock fails the test if any HTTP request reaches it, proving a verb is
// purely local.
func noNetMock(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected HTTP call: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func seedTimed(t *testing.T, dir, slug string, updated time.Time, path *string) {
	t.Helper()
	store := localstate.New(dir)
	rec := localstate.Record{
		Slug: slug, URL: "https://mdfly.dev/" + slug, Tier: "anon",
		Source: localstate.SourceText, UpdatedAt: updated, Path: path,
	}
	if path != nil {
		rec.Source = localstate.SourceFile
	}
	if err := store.Upsert(rec); err != nil {
		t.Fatalf("seed %s: %v", slug, err)
	}
}

func TestList_newestFirst_noNetwork(t *testing.T) {
	dir := t.TempDir()
	older := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	seedTimed(t, dir, "old", older, nil)
	seedTimed(t, dir, "new", newer, nil)
	m := noNetMock(t)

	out, _, err := executeIn(t, dir, "--api", m.URL, "list")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	iNew, iOld := strings.Index(out, "new"), strings.Index(out, "old")
	if iNew < 0 || iOld < 0 {
		t.Fatalf("both records must be present:\n%s", out)
	}
	if iNew > iOld {
		t.Errorf("newest record must print first:\n%s", out)
	}
}

func TestList_textPublishShowsText(t *testing.T) {
	dir := t.TempDir()
	seedTimed(t, dir, "s1", time.Now().UTC(), nil)
	m := noNetMock(t)

	out, _, err := executeIn(t, dir, "--api", m.URL, "list")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(out, "text") {
		t.Errorf("text publish should show \"text\" path:\n%s", out)
	}
}

func TestList_json_emitsArray(t *testing.T) {
	dir := t.TempDir()
	abs := filepath.Join(dir, "a.md")
	seedTimed(t, dir, "s1", time.Now().UTC(), &abs)
	m := noNetMock(t)

	out, _, err := executeIn(t, dir, "--json", "--api", m.URL, "list")
	if err != nil {
		t.Fatalf("list --json: %v", err)
	}
	var arr []struct {
		Slug string  `json:"slug"`
		Path *string `json:"path"`
	}
	if err := json.Unmarshal([]byte(out), &arr); err != nil {
		t.Fatalf("stdout not a JSON array: %v\n%s", err, out)
	}
	if len(arr) != 1 || arr[0].Slug != "s1" || arr[0].Path == nil {
		t.Errorf("json array = %+v, want one record s1 with path", arr)
	}
}

func TestList_empty_cleanMessage_exit0(t *testing.T) {
	dir := t.TempDir()
	m := noNetMock(t)

	out, errOut, err := executeIn(t, dir, "--api", m.URL, "list")
	if err != nil {
		t.Fatalf("empty list should exit 0: %v", err)
	}
	if strings.TrimSpace(out) != "" {
		t.Errorf("empty list stdout should be empty, got %q", out)
	}
	if !strings.Contains(errOut, "nothing published") {
		t.Errorf("empty list should print a clean stderr message, got %q", errOut)
	}
}

func TestList_empty_json_emitsEmptyArray(t *testing.T) {
	dir := t.TempDir()
	m := noNetMock(t)

	out, _, err := executeIn(t, dir, "--json", "--api", m.URL, "list")
	if err != nil {
		t.Fatalf("empty list --json: %v", err)
	}
	if strings.TrimSpace(out) != "[]" {
		t.Errorf("empty list --json stdout = %q, want []", out)
	}
}
