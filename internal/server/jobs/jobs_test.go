package jobs_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Abir66/mdfly/internal/server/jobs"
)

const waitFor = 2 * time.Second

// fakeClock hands out fakeTickers so tests drive ticks instead of sleeping.
type fakeClock struct {
	mu      sync.Mutex
	tickers map[time.Duration]*fakeTicker
}

func newFakeClock() *fakeClock {
	return &fakeClock{tickers: map[time.Duration]*fakeTicker{}}
}

func (c *fakeClock) NewTicker(d time.Duration) jobs.Ticker {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeTicker{c: make(chan time.Time, 1)}
	c.tickers[d] = t
	return t
}

// ticker returns the ticker created for interval d, waiting for the job
// goroutine to create it.
func (c *fakeClock) ticker(t *testing.T, d time.Duration) *fakeTicker {
	t.Helper()
	deadline := time.Now().Add(waitFor)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		tk := c.tickers[d]
		c.mu.Unlock()
		if tk != nil {
			return tk
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("no ticker created for interval %s", d)
	return nil
}

type fakeTicker struct {
	c chan time.Time
}

func (t *fakeTicker) Chan() <-chan time.Time { return t.c }
func (t *fakeTicker) Stop()                  {}

func (t *fakeTicker) tick(tb testing.TB) {
	tb.Helper()
	select {
	case t.c <- time.Time{}:
	case <-time.After(waitFor):
		tb.Fatal("job did not consume tick")
	}
}

func recv(tb testing.TB, ch <-chan string) string {
	tb.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(waitFor):
		tb.Fatal("timed out waiting for job to run")
		return ""
	}
}

// fakeRecorder captures every outcome the runner reports, and can fail the way
// a database-backed recorder would.
type fakeRecorder struct {
	runs   chan run
	err    error
	panics bool
}

type run struct {
	name   string
	runErr error
	at     time.Time
}

func newFakeRecorder() *fakeRecorder { return &fakeRecorder{runs: make(chan run, 4)} }

func (r *fakeRecorder) RecordJobRun(_ context.Context, name string, runErr error, at time.Time) error {
	r.runs <- run{name: name, runErr: runErr, at: at}
	if r.panics {
		panic("recorder exploded")
	}
	return r.err
}

func (r *fakeRecorder) recv(tb testing.TB) run {
	tb.Helper()
	select {
	case v := <-r.runs:
		return v
	case <-time.After(waitFor):
		tb.Fatal("timed out waiting for a recorded run")
		return run{}
	}
}

func TestRecorderSeesSuccessfulTick(t *testing.T) {
	clock := newFakeClock()
	recorder := newFakeRecorder()
	runner := jobs.New(clock, recorder)

	runner.Register("purge-drain", time.Minute, func(context.Context) error { return nil })
	runner.Start(context.Background())
	t.Cleanup(func() { _ = runner.Stop(context.Background()) })

	before := time.Now()
	clock.ticker(t, time.Minute).tick(t)

	got := recorder.recv(t)
	if got.name != "purge-drain" {
		t.Errorf("recorded job = %q, want %q", got.name, "purge-drain")
	}
	if got.runErr != nil {
		t.Errorf("recorded error = %v, want nil", got.runErr)
	}
	if got.at.Before(before) {
		t.Errorf("recorded at = %s, want at or after %s", got.at, before)
	}
}

func TestRecorderSeesFailedTick(t *testing.T) {
	clock := newFakeClock()
	recorder := newFakeRecorder()
	runner := jobs.New(clock, recorder)

	failure := errors.New("drain failed")
	runner.Register("purge-drain", time.Minute, func(context.Context) error { return failure })
	runner.Start(context.Background())
	t.Cleanup(func() { _ = runner.Stop(context.Background()) })

	clock.ticker(t, time.Minute).tick(t)

	if got := recorder.recv(t); !errors.Is(got.runErr, failure) {
		t.Fatalf("recorded error = %v, want %v", got.runErr, failure)
	}
}

// TestBrokenRecorderKeepsTickerAlive pins that the recorder only observes: a
// recorder that errors or panics must not stop the job it is watching.
func TestBrokenRecorderKeepsTickerAlive(t *testing.T) {
	for name, recorder := range map[string]*fakeRecorder{
		"erroring":  {runs: make(chan run, 2), err: errors.New("insert failed")},
		"panicking": {runs: make(chan run, 2), panics: true},
	} {
		t.Run(name, func(t *testing.T) {
			clock := newFakeClock()
			runner := jobs.New(clock, recorder)

			fired := make(chan string, 2)
			runner.Register("drain", time.Minute, func(context.Context) error { fired <- "drain"; return nil })
			runner.Start(context.Background())
			t.Cleanup(func() { _ = runner.Stop(context.Background()) })

			ticker := clock.ticker(t, time.Minute)
			ticker.tick(t)
			recv(t, fired)
			ticker.tick(t)
			if got := recv(t, fired); got != "drain" {
				t.Fatalf("job stopped ticking after a %s recorder, got %q", name, got)
			}
		})
	}
}

func TestJobFiresOnEveryTick(t *testing.T) {
	clock := newFakeClock()
	runner := jobs.New(clock, nil)

	fired := make(chan string, 2)
	runner.Register("purge-drain", time.Minute, func(context.Context) error { fired <- "purge-drain"; return nil })
	runner.Start(context.Background())
	t.Cleanup(func() { _ = runner.Stop(context.Background()) })

	ticker := clock.ticker(t, time.Minute)
	ticker.tick(t)
	if got := recv(t, fired); got != "purge-drain" {
		t.Fatalf("first tick ran %q", got)
	}
	ticker.tick(t)
	if got := recv(t, fired); got != "purge-drain" {
		t.Fatalf("second tick ran %q", got)
	}
}

func TestRegisterRejectsNonPositiveInterval(t *testing.T) {
	for _, interval := range []time.Duration{0, -time.Minute} {
		func() {
			defer func() {
				if recover() == nil {
					t.Fatalf("Register accepted interval %s", interval)
				}
			}()
			jobs.New(newFakeClock(), nil).Register("bad", interval, func(context.Context) error { return nil })
		}()
	}
}

func TestJobsTickIndependently(t *testing.T) {
	clock := newFakeClock()
	runner := jobs.New(clock, nil)

	fired := make(chan string, 4)
	runner.Register("purge-drain", 15*time.Minute, func(context.Context) error { fired <- "purge-drain"; return nil })
	runner.Register("lifecycle-gc", time.Hour, func(context.Context) error { fired <- "lifecycle-gc"; return nil })
	runner.Start(context.Background())
	t.Cleanup(func() { _ = runner.Stop(context.Background()) })

	drain := clock.ticker(t, 15*time.Minute)
	gc := clock.ticker(t, time.Hour)

	drain.tick(t)
	drain.tick(t)
	if got := recv(t, fired); got != "purge-drain" {
		t.Fatalf("first run was %q", got)
	}
	if got := recv(t, fired); got != "purge-drain" {
		t.Fatalf("second run was %q, gc must not fire on the drain interval", got)
	}

	gc.tick(t)
	if got := recv(t, fired); got != "lifecycle-gc" {
		t.Fatalf("third run was %q", got)
	}
}

func TestPanickingJobIsRecoveredAndSiblingsKeepTicking(t *testing.T) {
	clock := newFakeClock()
	recorder := newFakeRecorder()
	runner := jobs.New(clock, recorder)

	fired := make(chan string, 4)
	runner.Register("boom", time.Minute, func(context.Context) error {
		fired <- "boom"
		panic("job exploded")
	})
	runner.Register("healthy", time.Hour, func(context.Context) error { fired <- "healthy"; return nil })
	runner.Start(context.Background())
	t.Cleanup(func() { _ = runner.Stop(context.Background()) })

	boom := clock.ticker(t, time.Minute)
	healthy := clock.ticker(t, time.Hour)

	boom.tick(t)
	if got := recv(t, fired); got != "boom" {
		t.Fatalf("first run was %q", got)
	}
	if got := recorder.recv(t); got.name != "boom" || got.runErr == nil {
		t.Fatalf("recorded %q with error %v, want boom recorded as a failure", got.name, got.runErr)
	}
	boom.tick(t)
	if got := recv(t, fired); got != "boom" {
		t.Fatalf("panicking job stopped ticking, got %q", got)
	}

	healthy.tick(t)
	if got := recv(t, fired); got != "healthy" {
		t.Fatalf("sibling job stopped ticking, got %q", got)
	}
}

func TestStopWaitsForInFlightJob(t *testing.T) {
	clock := newFakeClock()
	runner := jobs.New(clock, nil)

	running, release, done := make(chan string, 1), make(chan struct{}), make(chan struct{})
	runner.Register("slow", time.Minute, func(context.Context) error {
		running <- "slow"
		<-release
		close(done)
		return nil
	})
	runner.Start(context.Background())

	clock.ticker(t, time.Minute).tick(t)
	recv(t, running)

	stopped := make(chan error, 1)
	go func() { stopped <- runner.Stop(context.Background()) }()

	select {
	case <-stopped:
		t.Fatal("Stop returned while a job was still running")
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatalf("Stop: %v", err)
		}
	case <-time.After(waitFor):
		t.Fatal("Stop did not return after the job finished")
	}
	<-done
}

func TestStopReturnsErrorWhenShutdownContextExpires(t *testing.T) {
	clock := newFakeClock()
	runner := jobs.New(clock, nil)

	running, release := make(chan string, 1), make(chan struct{})
	runner.Register("stuck", time.Minute, func(context.Context) error {
		running <- "stuck"
		<-release
		return nil
	})
	runner.Start(context.Background())
	t.Cleanup(func() { close(release) })

	clock.ticker(t, time.Minute).tick(t)
	recv(t, running)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := runner.Stop(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Stop error = %v, want context.DeadlineExceeded", err)
	}
}

func TestStopHaltsFurtherTicks(t *testing.T) {
	clock := newFakeClock()
	runner := jobs.New(clock, nil)

	fired := make(chan string, 2)
	runner.Register("drain", time.Minute, func(context.Context) error { fired <- "drain"; return nil })
	runner.Start(context.Background())

	ticker := clock.ticker(t, time.Minute)
	ticker.tick(t)
	recv(t, fired)

	if err := runner.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	ticker.c <- time.Time{}
	select {
	case got := <-fired:
		t.Fatalf("job ran after Stop: %q", got)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestStopIsIdempotent(t *testing.T) {
	runner := jobs.New(newFakeClock(), nil)
	runner.Register("drain", time.Minute, func(context.Context) error { return nil })
	runner.Start(context.Background())

	for i := range 2 {
		if err := runner.Stop(context.Background()); err != nil {
			t.Fatalf("Stop #%d: %v", i+1, err)
		}
	}
}

func TestSystemClockDrivesRealTicks(t *testing.T) {
	runner := jobs.New(jobs.SystemClock{}, nil)

	fired := make(chan string, 1)
	runner.Register("tick", 5*time.Millisecond, func(context.Context) error {
		select {
		case fired <- "tick":
		default:
		}
		return nil
	})
	runner.Start(context.Background())
	t.Cleanup(func() { _ = runner.Stop(context.Background()) })

	recv(t, fired)
}
