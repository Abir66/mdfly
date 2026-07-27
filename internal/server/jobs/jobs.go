// Package jobs runs named periodic work on in-process tickers (ADR-0029):
// each registered job gets its own goroutine and ticker, started at boot and
// stopped cleanly on shutdown.
package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
	"time"
)

// Ticker delivers periodic ticks. Implemented by time.Ticker in production and
// by fakes in tests.
type Ticker interface {
	Chan() <-chan time.Time
	Stop()
}

// Clock creates tickers. Injected so tests drive ticks deterministically.
type Clock interface {
	NewTicker(d time.Duration) Ticker
}

// SystemClock is the production Clock, backed by time.Ticker.
type SystemClock struct{}

func (SystemClock) NewTicker(d time.Duration) Ticker { return systemTicker{time.NewTicker(d)} }

type systemTicker struct{ t *time.Ticker }

func (s systemTicker) Chan() <-chan time.Time { return s.t.C }
func (s systemTicker) Stop()                  { s.t.Stop() }

type job struct {
	name     string
	interval time.Duration
	fn       func(context.Context)
}

// Runner owns the registered jobs and their goroutines. Build with New,
// register jobs, then Start and Stop.
type Runner struct {
	clock Clock
	jobs  []job
	quit  chan struct{}
	once  sync.Once
	wg    sync.WaitGroup
}

// New returns a Runner whose tickers come from clock.
func New(clock Clock) *Runner {
	return &Runner{clock: clock, quit: make(chan struct{})}
}

// Register adds a job to run every interval. Call before Start. Panics on a
// non-positive interval, which would otherwise panic later inside the job's
// goroutine when its ticker is created.
func (r *Runner) Register(name string, interval time.Duration, fn func(context.Context)) {
	if interval <= 0 {
		panic(fmt.Sprintf("jobs: interval for %q must be positive, got %s", name, interval))
	}
	r.jobs = append(r.jobs, job{name: name, interval: interval, fn: fn})
}

// Start launches one goroutine per registered job. ctx is passed to each job
// invocation.
func (r *Runner) Start(ctx context.Context) {
	for _, j := range r.jobs {
		r.wg.Add(1)
		go r.loop(ctx, j)
	}
}

// Stop halts further ticks and waits for in-flight jobs to finish, bounded by
// ctx. Returns ctx.Err() if a job is still running when ctx expires. Safe to
// call more than once.
func (r *Runner) Stop(ctx context.Context) error {
	r.once.Do(func() { close(r.quit) })

	drained := make(chan struct{})
	go func() {
		r.wg.Wait()
		close(drained)
	}()

	select {
	case <-drained:
		return nil
	case <-ctx.Done():
		slog.Warn("jobs runner shutdown timed out with a job still running")
		return ctx.Err()
	}
}

func (r *Runner) loop(ctx context.Context, j job) {
	defer r.wg.Done()
	ticker := r.clock.NewTicker(j.interval)
	defer ticker.Stop()

	for {
		select {
		case <-r.quit:
			return
		case <-ctx.Done():
			return
		case <-ticker.Chan():
			invoke(ctx, j)
		}
	}
}

// invoke runs one job tick, recovering a panic so it takes down neither the
// process nor the job's own ticker.
func invoke(ctx context.Context, j job) {
	defer func() {
		if rec := recover(); rec != nil {
			slog.Error("job panicked", "job", j.name, "panic", rec, "stack", string(debug.Stack()))
		}
	}()
	j.fn(ctx)
}
