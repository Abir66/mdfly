package db

import (
	"context"
	"fmt"
	"time"
)

// RecordJobRun stamps the outcome of one job tick, satisfying jobs.Recorder. The
// jobs process has no HTTP surface, so this row is how its liveness is read
// (ADR-0003). runErr nil means the pass succeeded.
//
// The name primary key keeps one row per job: a pass overwrites its own row
// rather than appending, so the table never needs a GC of its own. A failure
// leaves the last success in place and counts up; a success clears the count but
// keeps the last failure, so a recovered job still shows what went wrong.
func (o *ops) RecordJobRun(ctx context.Context, name string, runErr error, at time.Time) error {
	const q = `
INSERT INTO job_runs (name, last_success_at, last_failure_at, last_error, consecutive_failures)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (name) DO UPDATE
SET last_success_at = COALESCE(EXCLUDED.last_success_at, job_runs.last_success_at),
    last_failure_at = COALESCE(EXCLUDED.last_failure_at, job_runs.last_failure_at),
    last_error = COALESCE(EXCLUDED.last_error, job_runs.last_error),
    consecutive_failures = CASE
        WHEN EXCLUDED.last_success_at IS NOT NULL THEN 0
        ELSE job_runs.consecutive_failures + 1
    END`

	var successAt, failureAt *time.Time
	var errText *string
	failures := 0
	if runErr == nil {
		successAt = &at
	} else {
		failureAt = &at
		text := runErr.Error()
		errText = &text
		failures = 1
	}

	if _, err := o.q.Exec(ctx, q, name, successAt, failureAt, errText, failures); err != nil {
		return fmt.Errorf("record job run %s: %w", name, err)
	}
	return nil
}
