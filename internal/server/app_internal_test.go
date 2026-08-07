package server

import (
	"context"
	"net"
	"net/http"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Abir66/mdfly/internal/server/db"
	"github.com/Abir66/mdfly/internal/server/jobs"
	"github.com/Abir66/mdfly/internal/server/service/purge"
	"github.com/Abir66/mdfly/internal/server/static"
)

// lazyPool returns a pool that never connects: pgxpool dials on first use, so
// assembly can be exercised without infrastructure.
func lazyPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), "postgres://mdfly:secret@127.0.0.1:1/mdfly")
	if err != nil {
		t.Fatalf("open lazy pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestParseRole(t *testing.T) {
	for _, name := range []string{"serve", "jobs"} {
		role, err := ParseRole(name)
		if err != nil || string(role) != name {
			t.Errorf("ParseRole(%q) = %q, %v", name, role, err)
		}
	}
	for _, name := range []string{"", "server", "job", "SERVE"} {
		if _, err := ParseRole(name); err == nil {
			t.Errorf("ParseRole(%q) accepted an unknown subcommand", name)
		}
	}
}

// TestAssemble_webRoleTicksNothing pins the split (ADR-0003): the HTTP process
// registers no tickers, so the two web processes that overlap during a deploy
// swap cannot double every job.
func TestAssemble_webRoleTicksNothing(t *testing.T) {
	app, err := assemble(context.Background(), Config{BaseURL: "https://mdfly.dev"}, RoleServe, lazyPool(t))
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	if app.jobs != nil {
		t.Errorf("web role registered a job runner: %v", app.jobs.Names())
	}
	if app.db == nil || app.storage == nil {
		t.Error("web role must still wire the DB pool and the R2 client")
	}
	if app.publish == nil || app.view == nil || app.document == nil || app.static == nil {
		t.Error("web role is missing the services its routes serve")
	}
}

// TestAssemble_jobsRoleTicksAndServesNothing pins the other half: exactly the two
// tickers, and no Redis — a malformed REDIS_URL would fail the boot if the jobs
// role built a limiter at all.
func TestAssemble_jobsRoleTicksAndServesNothing(t *testing.T) {
	cfg := Config{
		BaseURL:    "https://mdfly.dev",
		Jobs:       JobsConfig{LifecycleGCInterval: time.Hour, PurgeDrainInterval: time.Minute},
		RateLimit:  RateLimitConfig{URL: "not-a-redis-url"},
		Cloudflare: CloudflareConfig{ZoneID: "zone", Token: "token"},
	}

	app, err := assemble(context.Background(), cfg, RoleJobs, lazyPool(t))
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	if app.jobs == nil {
		t.Fatal("jobs role registered no job runner")
	}
	want := []string{jobLifecycleGC, jobPurgeDrain}
	if got := app.jobs.Names(); !slices.Equal(got, want) {
		t.Errorf("registered jobs = %v, want %v", got, want)
	}
	if app.limiter != nil || app.counter != nil {
		t.Error("jobs role built a Redis limiter")
	}
	if app.db == nil || app.storage == nil {
		t.Error("jobs role must still wire the DB pool and the R2 client")
	}
}

// TestRun_jobsRoleStartsTickersAndOpensNoListener covers the jobs process's whole
// lifecycle: tickers run while Run blocks, stop once it returns, and nothing ever
// answers on cfg.Addr.
func TestRun_jobsRoleStartsTickersAndOpensNoListener(t *testing.T) {
	runner := jobs.New(jobs.SystemClock{}, nil)
	var runs atomic.Int64
	runner.Register("probe", time.Millisecond, func(context.Context) error { runs.Add(1); return nil })

	addr := freeAddr(t)
	app := &App{
		role: RoleJobs,
		cfg:  Config{Addr: addr, ShutdownTimeout: 5 * time.Second},
		jobs: runner,
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	done := make(chan error, 1)
	go func() { done <- app.Run(ctx) }()

	waitFor(t, func() bool { return runs.Load() > 0 })

	conn, err := net.DialTimeout("tcp", addr, time.Second)
	if err == nil {
		conn.Close()
		t.Fatalf("jobs role opened a listener on %s", addr)
	}

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

// TestRun_webRoleServes pins the other side of the same split: the HTTP process
// answers on cfg.Addr and shuts down on context cancellation.
func TestRun_webRoleServes(t *testing.T) {
	addr := freeAddr(t)
	app := &App{
		role:   RoleServe,
		cfg:    Config{Addr: addr, ShutdownTimeout: 5 * time.Second},
		static: static.New(),
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	done := make(chan error, 1)
	go func() { done <- app.Run(ctx) }()

	waitFor(t, func() bool {
		resp, err := http.Get("http://" + addr + "/healthz")
		if err != nil {
			return false
		}
		resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	})

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after context cancellation")
	}
}

// freeAddr returns a loopback address nothing is listening on.
func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	addr := l.Addr().String()
	l.Close()
	return addr
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

	runner := jobs.New(clock, nil)
	registerJobs(runner, JobsConfig{
		LifecycleGCInterval: interval,
		AbandonGrace:        grace,
		BlobDeleteGrace:     blobGrace,
	}, store, fakeBlobDeleter{}, nil)

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

// TestRegisterJobs_purgeDrain pins the drain's wiring: it gets its own ticker on
// JobsConfig.PurgeDrainInterval, and none at all when Cloudflare is unconfigured
// (nil purger), so an unpurgeable process does not churn the queue.
func TestRegisterJobs_purgeDrain(t *testing.T) {
	const (
		gcInterval    = 42 * time.Minute
		drainInterval = 7 * time.Minute
	)
	cfg := JobsConfig{
		LifecycleGCInterval: gcInterval,
		PurgeDrainInterval:  drainInterval,
		AbandonGrace:        time.Hour,
		BlobDeleteGrace:     time.Hour,
	}

	clock := &fakeClock{ticks: make(chan time.Time), created: make(chan time.Duration, 2)}
	runner := jobs.New(clock, nil)
	registerJobs(runner, cfg, &fakeGCStore{}, fakeBlobDeleter{}, &purge.Service{})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	runner.Start(ctx)

	intervals := map[time.Duration]bool{}
	for range 2 {
		select {
		case d := <-clock.created:
			intervals[d] = true
		case <-time.After(5 * time.Second):
			t.Fatal("fewer than two tickers created")
		}
	}
	if !intervals[drainInterval] {
		t.Errorf("no ticker on the purge drain interval %s, got %v", drainInterval, intervals)
	}

	bareClock := &fakeClock{ticks: make(chan time.Time), created: make(chan time.Duration, 2)}
	bareRunner := jobs.New(bareClock, nil)
	registerJobs(bareRunner, cfg, &fakeGCStore{}, fakeBlobDeleter{}, nil)
	bareRunner.Start(ctx)

	if got := <-bareClock.created; got != gcInterval {
		t.Errorf("first ticker interval = %s, want the lifecycle gc %s", got, gcInterval)
	}
	select {
	case d := <-bareClock.created:
		t.Errorf("second ticker created (%s) with no purge service wired", d)
	case <-time.After(100 * time.Millisecond):
	}
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
