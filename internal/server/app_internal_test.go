package server

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Abir66/mdfly/internal/server/db"
	"github.com/Abir66/mdfly/internal/server/jobs"
	"github.com/Abir66/mdfly/internal/server/static"
)

// TestRunStartsAndStopsJobs boots the App's lifecycle without infrastructure:
// a registered job must tick while Run blocks and stop once Run returns.
func TestRunStartsAndStopsJobs(t *testing.T) {
	runner := jobs.New(jobs.SystemClock{})
	var runs atomic.Int64
	runner.Register("probe", time.Millisecond, func(context.Context) { runs.Add(1) })

	app := &App{
		cfg:    Config{Addr: "127.0.0.1:0", ShutdownTimeout: 5 * time.Second},
		static: static.New(),
		jobs:   runner,
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	done := make(chan error, 1)
	go func() { done <- app.Run(ctx) }()

	waitFor(t, func() bool { return runs.Load() > 0 })

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after context cancellation")
	}

	after := runs.Load()
	time.Sleep(20 * time.Millisecond)
	if got := runs.Load(); got != after {
		t.Fatalf("job kept running after shutdown: %d -> %d", after, got)
	}
}

// fakeClock hands out one ticker the test drives by hand, publishing the
// interval each ticker was created with.
type fakeClock struct {
	ticks   chan time.Time
	created chan time.Duration
}

func (c *fakeClock) NewTicker(d time.Duration) jobs.Ticker {
	c.created <- d
	return fakeTicker{c.ticks}
}

type fakeTicker struct{ ch chan time.Time }

func (t fakeTicker) Chan() <-chan time.Time { return t.ch }
func (t fakeTicker) Stop()                  {}

// fakeGCStore reports the cutoff each sweep asked to abandon rows before and the
// one it asked to delete blobs before.
type fakeGCStore struct {
	abandonedBefore chan time.Time
	blobsBefore     chan time.Time
}

func (f *fakeGCStore) MarkExpired(context.Context, time.Time, int) (int64, error) { return 0, nil }

func (f *fakeGCStore) MarkAbandoned(_ context.Context, olderThan time.Time, _ int) (int64, error) {
	f.abandonedBefore <- olderThan
	return 0, nil
}

func (f *fakeGCStore) ListBlobGCCandidates(_ context.Context, gracedBefore time.Time, _ int) ([]db.BlobGCCandidate, error) {
	f.blobsBefore <- gracedBefore
	return nil, nil
}

func (f *fakeGCStore) SetBlobsDeletedAt(context.Context, int64) error { return nil }

type fakeBlobDeleter struct{}

func (fakeBlobDeleter) DeletePrefix(context.Context, string) error { return nil }

// TestRegisterJobs_lifecycleGC pins the wiring of the lifecycle sweep: it runs
// on JobsConfig.LifecycleGCInterval and applies both grace windows from
// JobsConfig.
func TestRegisterJobs_lifecycleGC(t *testing.T) {
	const (
		interval   = 42 * time.Minute
		grace      = 3 * time.Hour
		blobGrace  = 30 * time.Hour
		cutoffSlop = time.Second
	)
	clock := &fakeClock{ticks: make(chan time.Time), created: make(chan time.Duration, 1)}
	store := &fakeGCStore{
		abandonedBefore: make(chan time.Time, 1),
		blobsBefore:     make(chan time.Time, 1),
	}

	runner := jobs.New(clock)
	registerJobs(runner, JobsConfig{
		LifecycleGCInterval: interval,
		AbandonGrace:        grace,
		BlobDeleteGrace:     blobGrace,
	}, store, fakeBlobDeleter{})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	runner.Start(ctx)

	if got := <-clock.created; got != interval {
		t.Errorf("ticker interval = %s, want %s", got, interval)
	}

	before := time.Now()
	clock.ticks <- before

	assertCutoff := func(name string, got chan time.Time, applied time.Duration) {
		t.Helper()
		select {
		case cutoff := <-got:
			want := before.Add(-applied)
			if cutoff.Before(want.Add(-cutoffSlop)) || cutoff.After(time.Now().Add(-applied)) {
				t.Errorf("%s cutoff = %s, want ~%s", name, cutoff, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("lifecycle-gc job did not reach the %s step after a tick", name)
		}
	}
	assertCutoff("abandon", store.abandonedBefore, grace)
	assertCutoff("blob delete", store.blobsBefore, blobGrace)
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition not met before deadline")
}
