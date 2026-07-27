package server

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

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
