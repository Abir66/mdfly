package gc_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/Abir66/mdfly/internal/server/db"
	"github.com/Abir66/mdfly/internal/server/service/gc"
)

// fakeStore records the arguments each transition was called with and replays
// canned counts and errors.
type fakeStore struct {
	expiredNow      time.Time
	expiredLimit    int
	expiredCount    int64
	expiredErr      error
	abandonedBefore time.Time
	abandonedLimit  int
	abandonedCount  int64
	abandonedErr    error

	blobGraced     time.Time
	blobLimit      int
	candidates     []db.BlobGCCandidate
	stamped        []int64
	stampErr       error
	stampErrOnce   bool
	stampCallCount int
}

func (f *fakeStore) MarkExpired(_ context.Context, now time.Time, limit int) (int64, error) {
	f.expiredNow, f.expiredLimit = now, limit
	return f.expiredCount, f.expiredErr
}

func (f *fakeStore) MarkAbandoned(_ context.Context, olderThan time.Time, limit int) (int64, error) {
	f.abandonedBefore, f.abandonedLimit = olderThan, limit
	return f.abandonedCount, f.abandonedErr
}

func (f *fakeStore) ListBlobGCCandidates(_ context.Context, gracedBefore time.Time, limit int) ([]db.BlobGCCandidate, error) {
	f.blobGraced, f.blobLimit = gracedBefore, limit
	remaining := make([]db.BlobGCCandidate, 0, len(f.candidates))
	for _, c := range f.candidates {
		if !containsID(f.stamped, c.ID) {
			remaining = append(remaining, c)
		}
	}
	return remaining, nil
}

func (f *fakeStore) SetBlobsDeletedAt(_ context.Context, id int64) error {
	f.stampCallCount++
	if f.stampErr != nil && (!f.stampErrOnce || f.stampCallCount == 1) {
		return f.stampErr
	}
	f.stamped = append(f.stamped, id)
	return nil
}

func containsID(ids []int64, id int64) bool { return slices.Contains(ids, id) }

// fakeBlobs records every slug prefix it was asked to delete.
type fakeBlobs struct {
	deleted []string
	err     error
	failFor string
}

func (f *fakeBlobs) DeletePrefix(_ context.Context, slug string) error {
	f.deleted = append(f.deleted, slug)
	if f.err != nil && (f.failFor == "" || f.failFor == slug) {
		return f.err
	}
	return nil
}

func fixedClock(t time.Time) func() time.Time { return func() time.Time { return t } }

// newService builds a Service with both graces set, so a test that cares about
// one step does not have to spell out the other's config.
func newService(store gc.Store, blobs gc.Blobs) *gc.Service {
	return &gc.Service{
		Db:              store,
		Blobs:           blobs,
		AbandonGrace:    time.Hour,
		BlobDeleteGrace: 24 * time.Hour,
	}
}

func TestSweep_appliesClockAndGrace(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	store := &fakeStore{expiredCount: 3, abandonedCount: 2}
	svc := newService(store, &fakeBlobs{})
	svc.BatchSize = 50
	svc.Now = fixedClock(now)

	res, err := svc.Sweep(context.Background())
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}

	if !store.expiredNow.Equal(now) {
		t.Errorf("MarkExpired now=%s, want %s", store.expiredNow, now)
	}
	if want := now.Add(-time.Hour); !store.abandonedBefore.Equal(want) {
		t.Errorf("MarkAbandoned olderThan=%s, want %s", store.abandonedBefore, want)
	}
	if res.Expired != 3 || res.Abandoned != 2 {
		t.Errorf("result = %+v, want {Expired:3 Abandoned:2}", res)
	}
}

func TestSweep_boundsBatchSize(t *testing.T) {
	cases := []struct {
		name      string
		batchSize int
		want      int
	}{
		{"explicit", 7, 7},
		{"zero falls back to default", 0, gc.DefaultBatchSize},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeStore{}
			svc := newService(store, &fakeBlobs{})
			svc.BatchSize = tc.batchSize

			if _, err := svc.Sweep(context.Background()); err != nil {
				t.Fatalf("Sweep: %v", err)
			}
			if store.expiredLimit != tc.want || store.abandonedLimit != tc.want {
				t.Errorf("limits = (%d, %d), want %d", store.expiredLimit, store.abandonedLimit, tc.want)
			}
		})
	}
}

func TestSweep_defaultClockIsWallClock(t *testing.T) {
	store := &fakeStore{}
	svc := newService(store, &fakeBlobs{})

	before := time.Now()
	if _, err := svc.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if store.expiredNow.Before(before) || store.expiredNow.After(time.Now()) {
		t.Errorf("MarkExpired now=%s, want between %s and now", store.expiredNow, before)
	}
}

// A non-positive grace would abandon rows still mid-publish, so the pass must
// fail before touching either transition.
func TestSweep_rejectsNonPositiveAbandonGrace(t *testing.T) {
	for _, tc := range []struct {
		name  string
		grace time.Duration
	}{
		{"zero", 0},
		{"negative", -time.Minute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeStore{expiredCount: 3, abandonedCount: 2}
			svc := newService(store, &fakeBlobs{})
			svc.AbandonGrace = tc.grace

			res, err := svc.Sweep(context.Background())
			if !errors.Is(err, gc.ErrInvalidAbandonGrace) {
				t.Fatalf("err = %v, want ErrInvalidAbandonGrace", err)
			}
			if res != (gc.Result{}) {
				t.Errorf("result = %+v, want zero", res)
			}
			if store.expiredLimit != 0 || store.abandonedLimit != 0 {
				t.Error("a transition ran despite the invalid grace")
			}
		})
	}
}

// A failing transition must not skip the other one: the pass reports both the
// error and whatever the surviving transition moved.
func TestSweep_bothTransitionsRunWhenOneFails(t *testing.T) {
	boom := errors.New("boom")
	store := &fakeStore{expiredErr: boom, abandonedCount: 4}
	svc := newService(store, &fakeBlobs{})

	res, err := svc.Sweep(context.Background())
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want it to wrap boom", err)
	}
	if store.abandonedLimit == 0 {
		t.Error("MarkAbandoned was not called after MarkExpired failed")
	}
	if store.blobLimit == 0 {
		t.Error("the blob step was not reached after MarkExpired failed")
	}
	if res.Abandoned != 4 {
		t.Errorf("res.Abandoned = %d, want 4", res.Abandoned)
	}
}

// The blob step asks for candidates past the blob-delete grace, deletes each
// slug's prefix, and stamps only what it deleted.
func TestSweep_deletesBlobsPastGraceAndStamps(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	store := &fakeStore{candidates: []db.BlobGCCandidate{
		{ID: 1, Slug: "aaa", Status: db.StatusDeleted},
		{ID: 2, Slug: "bbb", Status: db.StatusAbandoned},
	}}
	blobs := &fakeBlobs{}
	svc := newService(store, blobs)
	svc.Now = fixedClock(now)
	svc.BatchSize = 50

	res, err := svc.Sweep(context.Background())
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}

	if want := now.Add(-24 * time.Hour); !store.blobGraced.Equal(want) {
		t.Errorf("ListBlobGCCandidates gracedBefore=%s, want %s", store.blobGraced, want)
	}
	if store.blobLimit != 50 {
		t.Errorf("ListBlobGCCandidates limit=%d, want 50", store.blobLimit)
	}
	if want := []string{"aaa", "bbb"}; len(blobs.deleted) != 2 || blobs.deleted[0] != want[0] || blobs.deleted[1] != want[1] {
		t.Errorf("deleted prefixes = %v, want %v", blobs.deleted, want)
	}
	if len(store.stamped) != 2 || !containsID(store.stamped, 1) || !containsID(store.stamped, 2) {
		t.Errorf("stamped ids = %v, want [1 2]", store.stamped)
	}
	if res.BlobsDeleted != 2 {
		t.Errorf("res.BlobsDeleted = %d, want 2", res.BlobsDeleted)
	}
}

// A failed prefix delete must not stamp the row — the blobs are still there —
// and must not stop the rest of the batch.
func TestSweep_failedDeleteLeavesRowUnstamped(t *testing.T) {
	store := &fakeStore{candidates: []db.BlobGCCandidate{
		{ID: 1, Slug: "broken", Status: db.StatusDeleted},
		{ID: 2, Slug: "fine", Status: db.StatusExpired},
	}}
	blobs := &fakeBlobs{err: errors.New("r2 down"), failFor: "broken"}

	res, err := newService(store, blobs).Sweep(context.Background())
	if err == nil {
		t.Fatal("Sweep: want error when a prefix delete fails")
	}
	if len(store.stamped) != 1 || store.stamped[0] != 2 {
		t.Errorf("stamped ids = %v, want only [2]", store.stamped)
	}
	if res.BlobsDeleted != 1 {
		t.Errorf("res.BlobsDeleted = %d, want 1", res.BlobsDeleted)
	}
}

// A crash between DeleteObjects and the stamp leaves the row on the work-list.
// The next pass re-deletes the (now empty) prefix and stamps it — harmless.
func TestSweep_reDeletesWhenStampFailed(t *testing.T) {
	store := &fakeStore{
		candidates:   []db.BlobGCCandidate{{ID: 1, Slug: "crashed", Status: db.StatusDeleted}},
		stampErr:     errors.New("db down"),
		stampErrOnce: true,
	}
	blobs := &fakeBlobs{}
	svc := newService(store, blobs)

	if _, err := svc.Sweep(context.Background()); err == nil {
		t.Fatal("first Sweep: want error when the stamp fails")
	}
	if len(store.stamped) != 0 {
		t.Fatalf("stamped ids = %v after failed stamp, want none", store.stamped)
	}

	res, err := svc.Sweep(context.Background())
	if err != nil {
		t.Fatalf("second Sweep: %v", err)
	}
	if want := []string{"crashed", "crashed"}; len(blobs.deleted) != 2 || blobs.deleted[1] != want[1] {
		t.Errorf("deleted prefixes = %v, want %v", blobs.deleted, want)
	}
	if len(store.stamped) != 1 || store.stamped[0] != 1 {
		t.Errorf("stamped ids = %v, want [1]", store.stamped)
	}
	if res.BlobsDeleted != 1 {
		t.Errorf("res.BlobsDeleted = %d, want 1", res.BlobsDeleted)
	}

	res, err = svc.Sweep(context.Background())
	if err != nil {
		t.Fatalf("third Sweep: %v", err)
	}
	if res.BlobsDeleted != 0 || len(blobs.deleted) != 2 {
		t.Errorf("stamped row was re-listed: deleted=%v res=%+v", blobs.deleted, res)
	}
}

// A non-positive blob-delete grace would drop blobs the moment a row goes
// terminal, leaving no drain window, so the pass refuses to run at all.
func TestSweep_rejectsNonPositiveBlobDeleteGrace(t *testing.T) {
	for _, tc := range []struct {
		name  string
		grace time.Duration
	}{
		{"zero", 0},
		{"negative", -time.Hour},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeStore{candidates: []db.BlobGCCandidate{{ID: 1, Slug: "aaa"}}}
			blobs := &fakeBlobs{}
			svc := newService(store, blobs)
			svc.BlobDeleteGrace = tc.grace

			res, err := svc.Sweep(context.Background())
			if !errors.Is(err, gc.ErrInvalidBlobDeleteGrace) {
				t.Fatalf("err = %v, want ErrInvalidBlobDeleteGrace", err)
			}
			if res != (gc.Result{}) {
				t.Errorf("result = %+v, want zero", res)
			}
			if len(blobs.deleted) != 0 || store.expiredLimit != 0 {
				t.Error("a step ran despite the invalid grace")
			}
		})
	}
}
