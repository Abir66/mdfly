package handlers_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Abir66/mdfly/internal/server/db"
	"github.com/Abir66/mdfly/internal/server/service/purge"
)

const purgeHost = "mdfly.dev"

// recordingCDN stands in for Cloudflare: it records every purge and can be made
// to fail, standing in for a rate-limited or down API.
type recordingCDN struct {
	mu    sync.Mutex
	calls [][]string
	fail  bool
}

func (c *recordingCDN) PurgePrefixes(_ context.Context, prefixes []string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, prefixes)
	if c.fail {
		return errors.New("cloudflare unavailable")
	}
	return nil
}

// setFail flips the CDN between healthy and down while inline purge goroutines
// may still be in flight.
func (c *recordingCDN) setFail(fail bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.fail = fail
}

func (c *recordingCDN) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.calls)
}

func (c *recordingCDN) lastCall() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.calls) == 0 {
		return nil
	}
	return c.calls[len(c.calls)-1]
}

func newPool(t *testing.T, dsn string) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// queuedSlugs returns every slug currently awaiting a CDN purge.
func queuedSlugs(t *testing.T, pool *pgxpool.Pool) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(), `SELECT slug FROM purge_queue ORDER BY slug`)
	if err != nil {
		t.Fatalf("read purge_queue: %v", err)
	}
	defer rows.Close()

	var slugs []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatalf("scan purge_queue: %v", err)
		}
		slugs = append(slugs, s)
	}
	return slugs
}

func assertQueued(t *testing.T, pool *pgxpool.Pool, want ...string) {
	t.Helper()
	got := queuedSlugs(t, pool)
	slices.Sort(want)
	if len(got) != len(want) {
		t.Fatalf("purge_queue = %v, want %v", got, want)
	}
	for i, slug := range want {
		if got[i] != slug {
			t.Errorf("purge_queue[%d] = %s, want %s", i, got[i], slug)
		}
	}
}

// TestPurgeQueue_writePathsEnqueueAndDrain walks the durable path end to end with
// Cloudflare down: every publish, update and delete leaves its slug queued, two
// updates to one slug collapse to a single row, the commits all succeed despite
// the failing purge, and a later drain against a healthy Cloudflare clears the
// queue.
func TestPurgeQueue_writePathsEnqueueAndDrain(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	dsn, env := startPostgres(t), startMinio(t)
	pool := newPool(t, dsn)
	cdn := &recordingCDN{}
	cdn.setFail(true)
	purger := &purge.Service{Db: db.New(pool), CDN: cdn, Host: purgeHost}
	srv := newTestServerWithPurge(t, dsn, env, "https://"+purgeHost, purger)

	const token = "edit-token-1"
	slug, hash := publishForUpdate(t, srv, "11111111-1111-4111-8111-111111111111", "doc.md", []byte("# one"), token)
	assertQueued(t, pool, slug)

	second := []byte("# two")
	bundle := singleFileBundle("doc.md", second)
	initBody := decodeUpdateInit(t, updateInit(t, srv, slug, hash, token, bundle))
	putBlob(t, initBody.PresignedURLs["doc.md"], second)
	hash = decodeUpdateCommit(t, updateCommit(t, srv, slug, hash, token, bundle)).ManifestHash

	third := []byte("# three")
	bundle = singleFileBundle("doc.md", third)
	initBody = decodeUpdateInit(t, updateInit(t, srv, slug, hash, token, bundle))
	putBlob(t, initBody.PresignedURLs["doc.md"], third)
	if got := decodeUpdateCommit(t, updateCommit(t, srv, slug, hash, token, bundle)); got.Slug != slug {
		t.Fatalf("update moved the slug: %s", got.Slug)
	}
	assertQueued(t, pool, slug)

	other, _ := publishForUpdate(t, srv, "22222222-2222-4222-8222-222222222222", "other.md", []byte("# other"), token)
	deleteDocument(t, srv, other, token)
	assertQueued(t, pool, slug, other)

	if status, _ := getString(t, srv.URL+"/"+slug); status != http.StatusOK {
		t.Errorf("view after failed purges = %d, want 200: the commit must not roll back", status)
	}

	cdn.setFail(false)
	res, err := purger.Drain(context.Background())
	if err != nil {
		t.Fatalf("drain: %v", err)
	}
	if res.Failed != 0 {
		t.Errorf("drain result = %+v, want no failures", res)
	}
	if got := queuedSlugs(t, pool); len(got) != 0 {
		t.Errorf("purge_queue = %v after a healthy drain, want empty", got)
	}
}

// TestPurgeQueue_inlineAttemptClearsRow covers the post-commit fast path: the
// detached goroutine purges all three prefixes and clears the queue row without
// waiting for a drain tick.
func TestPurgeQueue_inlineAttemptClearsRow(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	dsn, env := startPostgres(t), startMinio(t)
	pool := newPool(t, dsn)
	cdn := &recordingCDN{}
	purger := &purge.Service{Db: db.New(pool), CDN: cdn, Host: purgeHost}
	srv := newTestServerWithPurge(t, dsn, env, "https://"+purgeHost, purger)

	slug := publishFiles(t, srv, "33333333-3333-4333-8333-333333333333", "", "doc.md", map[string][]byte{"doc.md": []byte("# hi")})

	waitUntil(t, func() bool { return len(queuedSlugs(t, pool)) == 0 })
	if cdn.callCount() == 0 {
		t.Fatal("inline purge never reached the CDN")
	}
	want := []string{purgeHost + "/" + slug, purgeHost + "/llm/" + slug, purgeHost + "/raw/" + slug}
	got := cdn.lastCall()
	if len(got) != len(want) {
		t.Fatalf("purged %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("prefix[%d] = %s, want %s", i, got[i], want[i])
		}
	}
}

// TestPurgeQueue_enqueuesWithoutAPurger keeps the durable path independent of the
// inline one: with no purge service wired, the commit still queues its slug.
func TestPurgeQueue_enqueuesWithoutAPurger(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	dsn, env := startPostgres(t), startMinio(t)
	pool := newPool(t, dsn)
	srv := newTestServer(t, dsn, env, "https://"+purgeHost)

	slug := publishFiles(t, srv, "44444444-4444-4444-8444-444444444444", "", "doc.md", map[string][]byte{"doc.md": []byte("# hi")})
	assertQueued(t, pool, slug)
}

func waitUntil(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met before deadline")
}

// deleteDocument issues the authenticated DELETE the CLI's delete verb sends.
func deleteDocument(t *testing.T, srv *httptest.Server, slug, token string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodDelete, srv.URL+"/v1/documents/"+slug, nil)
	if err != nil {
		t.Fatalf("build DELETE: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE %s status = %d, want 204", slug, resp.StatusCode)
	}
}
