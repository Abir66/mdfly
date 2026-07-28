package purge_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/Abir66/mdfly/internal/server/db"
	"github.com/Abir66/mdfly/internal/server/service/purge"
)

// fakeCDN records every purge call and can be told to fail.
type fakeCDN struct {
	calls [][]string
	err   error
	done  chan struct{}
}

func (f *fakeCDN) PurgePrefixes(_ context.Context, prefixes []string) error {
	f.calls = append(f.calls, prefixes)
	if f.done != nil {
		close(f.done)
	}
	return f.err
}

// fakeStore is an in-memory purge_queue.
type fakeStore struct {
	queue     map[string]int // slug → attempts
	due       []db.PurgeTask
	backoffs  map[string]time.Time
	doneSlugs []string
	claimErr  error
}

func newFakeStore(due ...db.PurgeTask) *fakeStore {
	s := &fakeStore{
		queue:    map[string]int{},
		backoffs: map[string]time.Time{},
	}
	for _, t := range due {
		s.queue[t.Slug] = t.Attempts
		s.due = append(s.due, t)
	}
	return s
}

func (s *fakeStore) ClaimPurgeDue(_ context.Context, _ time.Time, limit int) ([]db.PurgeTask, error) {
	if s.claimErr != nil {
		return nil, s.claimErr
	}
	if limit < len(s.due) {
		return s.due[:limit], nil
	}
	return s.due, nil
}

// MarkPurgeDone mirrors the fenced DELETE: a claim whose attempts no longer match
// the row leaves it queued.
func (s *fakeStore) MarkPurgeDone(_ context.Context, slug string, attempts int) error {
	if got, ok := s.queue[slug]; ok && got != attempts {
		return nil
	}
	delete(s.queue, slug)
	s.doneSlugs = append(s.doneSlugs, slug)
	return nil
}

// BackoffPurge mirrors the fenced UPDATE: same attempts check, then one more
// attempt and a later due time.
func (s *fakeStore) BackoffPurge(_ context.Context, slug string, attempts int, next time.Time) error {
	if got, ok := s.queue[slug]; ok && got != attempts {
		return nil
	}
	s.queue[slug]++
	s.backoffs[slug] = next
	return nil
}

var testNow = time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)

func newService(store *fakeStore, cdn *fakeCDN) *purge.Service {
	return &purge.Service{
		Db:   store,
		CDN:  cdn,
		Host: "mdfly.dev",
		Now:  func() time.Time { return testNow },
	}
}

// TestPurge_issuesTwoPrefixes pins the invalidation surface: the human page and
// the LLM twin, each as a prefix covering the bare page plus every sub-path.
func TestPurge_issuesTwoPrefixes(t *testing.T) {
	store, cdn := newFakeStore(), &fakeCDN{}
	svc := newService(store, cdn)

	if err := svc.Purge(context.Background(), db.PurgeTask{Slug: "abc123"}); err != nil {
		t.Fatalf("Purge: %v", err)
	}

	if len(cdn.calls) != 1 {
		t.Fatalf("cdn called %d times, want 1", len(cdn.calls))
	}
	want := []string{"mdfly.dev/abc123", "mdfly.dev/llm/abc123"}
	if len(cdn.calls[0]) != len(want) {
		t.Fatalf("purged %v, want %v", cdn.calls[0], want)
	}
	for i, p := range want {
		if cdn.calls[0][i] != p {
			t.Errorf("prefix[%d] = %s, want %s", i, cdn.calls[0][i], p)
		}
	}
}

// TestPurge_clearsQueueRowOnSuccess and its idempotent repeat: a late or
// duplicated purge re-issues the prefixes and clears a row that may already be
// gone, without erroring.
func TestPurge_clearsQueueRowOnSuccess(t *testing.T) {
	store, cdn := newFakeStore(db.PurgeTask{Slug: "abc123"}), &fakeCDN{}
	svc := newService(store, cdn)
	ctx := context.Background()

	if err := svc.Purge(ctx, db.PurgeTask{Slug: "abc123"}); err != nil {
		t.Fatalf("Purge: %v", err)
	}
	if _, still := store.queue["abc123"]; still {
		t.Error("queue row survived a successful purge")
	}

	if err := svc.Purge(ctx, db.PurgeTask{Slug: "abc123"}); err != nil {
		t.Errorf("duplicate Purge: %v", err)
	}
	if len(cdn.calls) != 2 {
		t.Errorf("cdn called %d times over two purges, want 2", len(cdn.calls))
	}
}

// TestPurge_keepsRowOnCloudflareFailure covers the retry contract: a failed purge
// leaves the row queued rather than clearing it.
func TestPurge_keepsRowOnCloudflareFailure(t *testing.T) {
	store := newFakeStore(db.PurgeTask{Slug: "abc123"})
	cdn := &fakeCDN{err: errors.New("429 Too Many Requests")}
	svc := newService(store, cdn)

	if err := svc.Purge(context.Background(), db.PurgeTask{Slug: "abc123"}); err == nil {
		t.Fatal("Purge returned nil on a Cloudflare failure")
	}
	if _, still := store.queue["abc123"]; !still {
		t.Error("queue row cleared despite a failed purge")
	}
	if len(store.doneSlugs) != 0 {
		t.Errorf("MarkPurgeDone called on failure: %v", store.doneSlugs)
	}
}

// TestPurge_rejectsUnwiredCDN keeps a purge from silently succeeding when the
// Cloudflare client was never wired: the row must stay queued.
func TestPurge_rejectsUnwiredCDN(t *testing.T) {
	store := newFakeStore(db.PurgeTask{Slug: "abc123"})
	svc := &purge.Service{Db: store, Host: "mdfly.dev"}

	err := svc.Purge(context.Background(), db.PurgeTask{Slug: "abc123"})
	if !errors.Is(err, purge.ErrMissingCDN) {
		t.Fatalf("Purge with no CDN = %v, want ErrMissingCDN", err)
	}
	if _, still := store.queue["abc123"]; !still {
		t.Error("queue row cleared without a CDN client")
	}
}

// TestPurge_rejectsMissingHost catches a misconfigured base URL before it sends
// a prefix that would purge nothing.
func TestPurge_rejectsMissingHost(t *testing.T) {
	svc := &purge.Service{Db: newFakeStore(), CDN: &fakeCDN{}}

	if err := svc.Purge(context.Background(), db.PurgeTask{Slug: "abc123"}); !errors.Is(err, purge.ErrMissingHost) {
		t.Fatalf("Purge with no host = %v, want ErrMissingHost", err)
	}
}

// TestPurge_rejectsMissingStore refuses before Cloudflare is called: with no queue
// a successful purge could never be recorded, so the edge call would repeat every
// tick against a row nothing can clear.
func TestPurge_rejectsMissingStore(t *testing.T) {
	cdn := &fakeCDN{}
	svc := &purge.Service{CDN: cdn, Host: "mdfly.dev"}

	if err := svc.Purge(context.Background(), db.PurgeTask{Slug: "abc123"}); !errors.Is(err, purge.ErrMissingStore) {
		t.Fatalf("Purge with no store = %v, want ErrMissingStore", err)
	}
	if len(cdn.calls) != 0 {
		t.Errorf("cdn called without a store: %v", cdn.calls)
	}
}

// TestDrain_rejectsMissingStore is the drain counterpart: the pass reports the
// misconfiguration instead of an empty queue.
func TestDrain_rejectsMissingStore(t *testing.T) {
	cdn := &fakeCDN{}
	svc := &purge.Service{CDN: cdn, Host: "mdfly.dev"}

	res, err := svc.Drain(context.Background())
	if !errors.Is(err, purge.ErrMissingStore) {
		t.Fatalf("Drain with no store = %v, want ErrMissingStore", err)
	}
	if res != (purge.Result{}) {
		t.Errorf("result = %+v, want zero", res)
	}
	if len(cdn.calls) != 0 {
		t.Errorf("cdn called without a store: %v", cdn.calls)
	}
}

// TestPurge_staleClaimLeavesReEnqueuedRow covers the attempts fence: a write that
// re-enqueued the slug mid-purge resets its attempts, so the older claim's clear
// is a no-op and the newer content keeps its queued purge.
func TestPurge_staleClaimLeavesReEnqueuedRow(t *testing.T) {
	store := newFakeStore(db.PurgeTask{Slug: "abc123"}) // re-enqueued: attempts back to 0
	svc := newService(store, &fakeCDN{})

	if err := svc.Purge(context.Background(), db.PurgeTask{Slug: "abc123", Attempts: 2}); err != nil {
		t.Fatalf("Purge: %v", err)
	}
	if _, still := store.queue["abc123"]; !still {
		t.Error("stale claim cleared a re-enqueued row")
	}
}

// TestDrain_purgesEveryDueSlug covers the happy drain pass: each claimed slug is
// purged and dropped from the queue.
func TestDrain_purgesEveryDueSlug(t *testing.T) {
	store := newFakeStore(db.PurgeTask{Slug: "aaa"}, db.PurgeTask{Slug: "bbb"})
	cdn := &fakeCDN{}
	svc := newService(store, cdn)

	res, err := svc.Drain(context.Background())
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if res.Purged != 2 || res.Failed != 0 {
		t.Errorf("result = %+v, want 2 purged, 0 failed", res)
	}
	if len(store.queue) != 0 {
		t.Errorf("queue still holds %v", store.queue)
	}
}

// TestDrain_backsOffFailuresExponentially covers the retry curve: a failed slug
// stays queued with a later due time that grows with attempts, capped so a long
// outage cannot push a retry out indefinitely.
func TestDrain_backsOffFailuresExponentially(t *testing.T) {
	tests := []struct {
		attempts  int
		wantDelay time.Duration
	}{
		{0, time.Minute},
		{1, 2 * time.Minute},
		{4, 16 * time.Minute},
		{30, 6 * time.Hour}, // capped
	}

	for _, tt := range tests {
		store := newFakeStore(db.PurgeTask{Slug: "aaa", Attempts: tt.attempts})
		svc := newService(store, &fakeCDN{err: errors.New("boom")})

		res, err := svc.Drain(context.Background())
		if err == nil {
			t.Errorf("attempts=%d: Drain returned nil error", tt.attempts)
		}
		if res.Failed != 1 || res.Purged != 0 {
			t.Errorf("attempts=%d: result = %+v, want 0 purged, 1 failed", tt.attempts, res)
		}
		got, ok := store.backoffs["aaa"]
		if !ok {
			t.Fatalf("attempts=%d: no backoff recorded", tt.attempts)
		}
		if want := testNow.Add(tt.wantDelay); !got.Equal(want) {
			t.Errorf("attempts=%d: next attempt at %s, want %s", tt.attempts, got, want)
		}
	}
}

// TestDrain_oneFailureDoesNotStallTheBatch keeps a single bad slug from blocking
// the rest of the queue.
func TestDrain_oneFailureDoesNotStallTheBatch(t *testing.T) {
	store := newFakeStore(db.PurgeTask{Slug: "aaa"}, db.PurgeTask{Slug: "bbb"})
	cdn := &failOnceCDN{failFor: "aaa"}
	svc := &purge.Service{Db: store, CDN: cdn, Host: "mdfly.dev", Now: func() time.Time { return testNow }}

	res, err := svc.Drain(context.Background())
	if err == nil {
		t.Error("Drain returned nil error with a failing slug")
	}
	if res.Purged != 1 || res.Failed != 1 {
		t.Errorf("result = %+v, want 1 purged, 1 failed", res)
	}
	if _, still := store.queue["bbb"]; still {
		t.Error("healthy slug left queued")
	}
	if _, still := store.queue["aaa"]; !still {
		t.Error("failed slug dropped from the queue")
	}
}

// failOnceCDN fails only for the slug named in failFor.
type failOnceCDN struct {
	failFor string
}

func (f *failOnceCDN) PurgePrefixes(_ context.Context, prefixes []string) error {
	if slices.Contains(prefixes, "mdfly.dev/"+f.failFor) {
		return errors.New("boom")
	}
	return nil
}

// TestDrain_claimFailureIsReported keeps a broken queue read from looking like an
// empty queue.
func TestDrain_claimFailureIsReported(t *testing.T) {
	store := newFakeStore()
	store.claimErr = errors.New("db down")
	svc := newService(store, &fakeCDN{})

	if _, err := svc.Drain(context.Background()); err == nil {
		t.Error("Drain returned nil error when the claim failed")
	}
}

// TestAttemptInline runs the post-commit fast path and waits for its goroutine:
// the slug is purged and cleared without the caller's request context.
func TestAttemptInline(t *testing.T) {
	store := newFakeStore(db.PurgeTask{Slug: "abc123"})
	cdn := &fakeCDN{done: make(chan struct{})}
	svc := newService(store, cdn)

	ctx, cancel := context.WithCancel(context.Background())
	svc.AttemptInline(ctx, "abc123")
	cancel() // the request finished; the detached purge must still run

	select {
	case <-cdn.done:
	case <-time.After(2 * time.Second):
		t.Fatal("inline purge never called the CDN")
	}
}

// TestHostFromBaseURL derives the purge host from the configured base URL: the
// prefix Cloudflare wants carries no scheme, path, trailing slash or query.
func TestHostFromBaseURL(t *testing.T) {
	tests := []struct{ in, want string }{
		{"https://mdfly.dev", "mdfly.dev"},
		{"https://mdfly.dev/", "mdfly.dev"},
		{"http://localhost:8080", "localhost:8080"},
		{"mdfly.dev", "mdfly.dev"},
		{"https://mdfly.dev/base", "mdfly.dev"},
		{"https://mdfly.dev/base/", "mdfly.dev"},
		{"https://mdfly.dev/base?x=1", "mdfly.dev"},
		{"http://localhost:8080/base?x=1", "localhost:8080"},
		{"mdfly.dev/base", "mdfly.dev"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := purge.HostFromBaseURL(tt.in); got != tt.want {
			t.Errorf("HostFromBaseURL(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
