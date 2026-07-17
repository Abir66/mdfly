package publish_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Abir66/mdfly/internal/api"
	"github.com/Abir66/mdfly/internal/cli/input"
	"github.com/Abir66/mdfly/internal/cli/localstate"
	"github.com/Abir66/mdfly/internal/cli/publish"
)

// uploadTracker observes the presigned-PUT phase: which paths were uploaded and
// the peak number of concurrent uploads, so tests can assert the pool fans out
// but stays within the cap. failPath, when set, makes that blob's PUT fail hard.
type uploadTracker struct {
	mu           sync.Mutex
	uploaded     map[string]bool
	inFlight     int
	maxFlight    int
	failPath     string
	commitCalled atomic.Bool
}

// multiBlobServer serves the 3-phase flow for a multi-file bundle, routing each
// presigned PUT to /blob/<path> and recording concurrency on the tracker.
func multiBlobServer(t *testing.T, tr *uploadTracker) *httptest.Server {
	t.Helper()
	tr.uploaded = map[string]bool{}
	var srv *httptest.Server
	mux := http.NewServeMux()

	mux.HandleFunc("POST /v1/publish/init", func(w http.ResponseWriter, r *http.Request) {
		var req api.InitRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		urls := map[string]string{}
		for _, f := range req.Bundle.Files {
			urls[f.Path] = srv.URL + "/blob/" + f.Path
		}
		writeJSON(w, api.InitResponse{Slug: "slugmulti", PresignedURLs: urls})
	})

	mux.HandleFunc("PUT /blob/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/blob/")
		io.ReadAll(r.Body) //nolint:errcheck

		tr.mu.Lock()
		tr.inFlight++
		if tr.inFlight > tr.maxFlight {
			tr.maxFlight = tr.inFlight
		}
		fail := path == tr.failPath
		tr.mu.Unlock()

		time.Sleep(15 * time.Millisecond) // widen the concurrency window

		tr.mu.Lock()
		tr.inFlight--
		if !fail {
			tr.uploaded[path] = true
		}
		tr.mu.Unlock()

		if fail {
			http.Error(w, "blob rejected", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	mux.HandleFunc("POST /v1/publish/commit", func(w http.ResponseWriter, r *http.Request) {
		tr.commitCalled.Store(true)
		writeJSON(w, api.CommitResponse{URL: srv.URL + "/slugmulti", Slug: "slugmulti", ManifestHash: "mh1"})
	})

	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// imageHeavyBundle writes a markdown root linking n local images and returns
// the root path.
func imageHeavyBundle(t *testing.T, n int) string {
	t.Helper()
	dir := t.TempDir()
	var body strings.Builder
	body.WriteString("# Gallery\n\n")
	for i := range n {
		name := fmt.Sprintf("img%d.png", i)
		writeFile(t, filepath.Join(dir, name), fmt.Appendf(nil, "png-%d", i))
		fmt.Fprintf(&body, "![i](./%s)\n", name)
	}
	root := filepath.Join(dir, "index.md")
	writeFile(t, root, []byte(body.String()))
	return root
}

func TestRun_persistsRecordAndToken(t *testing.T) {
	content := []byte("# Persist\n")
	dir := t.TempDir()
	mdFile := filepath.Join(dir, "note.md")
	writeFile(t, mdFile, content)
	srv := mockServer(t, "note.md", content)

	stateDir := t.TempDir()
	res, err := publish.Run(publish.Options{
		APIBase:  srv.URL,
		StateDir: stateDir,
		Source:   input.Source{Kind: input.KindFile, Path: mdFile},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	store := localstate.New(stateDir)
	led, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	rec, ok := led.Get(res.Slug)
	if !ok {
		t.Fatalf("no record for slug %q", res.Slug)
	}
	if rec.URL != res.URL || rec.Source != localstate.SourceFile || rec.Tier != "anon" {
		t.Errorf("record mismatch: %+v", rec)
	}
	if rec.Path == nil || *rec.Path != mdFile {
		t.Errorf("record path=%v, want %q", rec.Path, mdFile)
	}
	if tok, ok, _ := store.LoadToken(res.Slug); !ok || !strings.HasPrefix(tok, "mftk_") {
		t.Errorf("edit token not persisted: %q ok=%v", tok, ok)
	}
}

func TestRun_textPublishPersistsNilPath(t *testing.T) {
	content := []byte("# Inline\n")
	srv := mockServer(t, "index.md", content)

	stateDir := t.TempDir()
	res, err := publish.Run(publish.Options{
		APIBase:  srv.URL,
		StateDir: stateDir,
		Source:   input.Source{Kind: input.KindText, Content: content},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	rec, _ := mustRecord(t, stateDir, res.Slug)
	if rec.Source != localstate.SourceText || rec.Path != nil {
		t.Errorf("text publish record: source=%q path=%v, want text/nil", rec.Source, rec.Path)
	}
}

func TestRun_parallelUploadWithinCap(t *testing.T) {
	root := imageHeavyBundle(t, 20)
	var tr uploadTracker
	srv := multiBlobServer(t, &tr)

	if _, err := publish.Run(publish.Options{
		APIBase:  srv.URL,
		StateDir: t.TempDir(),
		Source:   input.Source{Kind: input.KindFile, Path: root},
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(tr.uploaded) != 21 { // 20 images + root
		t.Errorf("uploaded %d blobs, want 21", len(tr.uploaded))
	}
	if tr.maxFlight < 2 {
		t.Errorf("maxFlight=%d, uploads did not run in parallel", tr.maxFlight)
	}
	if tr.maxFlight > 6 {
		t.Errorf("maxFlight=%d exceeds concurrency cap 6", tr.maxFlight)
	}
}

func TestRun_firstUploadErrorFails(t *testing.T) {
	root := imageHeavyBundle(t, 10)
	tr := uploadTracker{failPath: "img3.png"}
	srv := multiBlobServer(t, &tr)

	stateDir := t.TempDir()
	res, err := publish.Run(publish.Options{
		APIBase:  srv.URL,
		StateDir: stateDir,
		Source:   input.Source{Kind: input.KindFile, Path: root},
	})
	if err == nil {
		t.Fatal("want error when a blob upload fails, got nil")
	}
	// A failed upload must abort before commit — no published output, no record.
	if tr.commitCalled.Load() {
		t.Error("commit was called despite an upload failure")
	}
	if res.URL != "" || res.Slug != "" {
		t.Errorf("failed publish returned output: %+v", res)
	}
	if led, err := localstate.New(stateDir).Load(); err != nil {
		t.Fatalf("Load: %v", err)
	} else if n := len(led.All()); n != 0 {
		t.Errorf("failed publish persisted %d records, want 0", n)
	}
}

func TestRun_reusesIdempotencyKey(t *testing.T) {
	content := []byte("# Key\n")
	dir := t.TempDir()
	mdFile := filepath.Join(dir, "k.md")
	writeFile(t, mdFile, content)

	var initKey, commitKey string
	var srv *httptest.Server
	hash := sha256Hex(content)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/publish/init", func(w http.ResponseWriter, r *http.Request) {
		var req api.InitRequest
		json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck
		initKey = req.IdempotencyKey
		writeJSON(w, api.InitResponse{Slug: "k1", PresignedURLs: map[string]string{"k.md": srv.URL + "/blob/" + hash}})
	})
	mux.HandleFunc("PUT /blob/", func(w http.ResponseWriter, r *http.Request) {
		io.ReadAll(r.Body) //nolint:errcheck
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("POST /v1/publish/commit", func(w http.ResponseWriter, r *http.Request) {
		var req api.CommitRequest
		json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck
		commitKey = req.IdempotencyKey
		writeJSON(w, api.CommitResponse{URL: srv.URL + "/k1", Slug: "k1"})
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	if _, err := publish.Run(publish.Options{
		APIBase:  srv.URL,
		StateDir: t.TempDir(),
		Source:   input.Source{Kind: input.KindFile, Path: mdFile},
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if initKey == "" || initKey != commitKey {
		t.Errorf("idempotency key: init=%q commit=%q, want equal and non-empty", initKey, commitKey)
	}
}

func TestRun_overCapBundleFailsBeforeInit(t *testing.T) {
	dir := t.TempDir()
	big := make([]byte, api.AnonMaxSingleFileBytes+1)
	mdFile := filepath.Join(dir, "big.md")
	writeFile(t, mdFile, big)

	var initCalled atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		initCalled.Store(true)
		http.Error(w, "should not be called", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	_, err := publish.Run(publish.Options{
		APIBase:  srv.URL,
		StateDir: t.TempDir(),
		Source:   input.Source{Kind: input.KindFile, Path: mdFile},
	})
	var limitErr *publish.BundleLimitError
	if err == nil || !errors.As(err, &limitErr) {
		t.Fatalf("want BundleLimitError, got %v", err)
	}
	if initCalled.Load() {
		t.Error("init was called despite over-cap bundle")
	}
}

func mustRecord(t *testing.T, stateDir, slug string) (localstate.Record, *localstate.Store) {
	t.Helper()
	store := localstate.New(stateDir)
	led, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	rec, ok := led.Get(slug)
	if !ok {
		t.Fatalf("no record for slug %q", slug)
	}
	return rec, store
}
