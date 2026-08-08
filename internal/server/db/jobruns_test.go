package db_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// jobRunRow is the recorded liveness of one job.
type jobRunRow struct {
	lastSuccessAt       *time.Time
	lastFailureAt       *time.Time
	lastError           *string
	consecutiveFailures int
}

func readJobRun(t *testing.T, pool *pgxpool.Pool, name string) (jobRunRow, bool) {
	t.Helper()
	var r jobRunRow
	err := pool.QueryRow(context.Background(),
		`SELECT last_success_at, last_failure_at, last_error, consecutive_failures
		 FROM job_runs WHERE name = $1`, name).
		Scan(&r.lastSuccessAt, &r.lastFailureAt, &r.lastError, &r.consecutiveFailures)
	if err != nil {
		return jobRunRow{}, false
	}
	return r, true
}

func countJobRunRows(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM job_runs`).Scan(&n); err != nil {
		t.Fatalf("count job_runs rows: %v", err)
	}
	return n
}

// TestRecordJobRun_upsertsByName covers the one-row-per-job shape: repeated
// passes overwrite the job's row instead of appending, so the table cannot grow
// without bound and never needs a GC of its own.
func TestRecordJobRun_upsertsByName(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	client, pool := lifecycleDB(t)
	ctx := context.Background()

	first := time.Now().Add(-time.Hour).UTC().Truncate(time.Millisecond)
	if err := client.RecordJobRun(ctx, "lifecycle-gc", nil, first); err != nil {
		t.Fatalf("first record: %v", err)
	}
	row, ok := readJobRun(t, pool, "lifecycle-gc")
	if !ok {
		t.Fatal("first record inserted no row")
	}
	if row.lastSuccessAt == nil || !row.lastSuccessAt.Equal(first) {
		t.Fatalf("last_success_at = %v, want %s", row.lastSuccessAt, first)
	}

	second := first.Add(time.Hour)
	if err := client.RecordJobRun(ctx, "lifecycle-gc", nil, second); err != nil {
		t.Fatalf("second record: %v", err)
	}
	if got := countJobRunRows(t, pool); got != 1 {
		t.Fatalf("job_runs rows = %d, want 1 — the second pass appended instead of upserting", got)
	}
	row, _ = readJobRun(t, pool, "lifecycle-gc")
	if row.lastSuccessAt == nil || !row.lastSuccessAt.Equal(second) {
		t.Fatalf("last_success_at = %v, want the newer %s", row.lastSuccessAt, second)
	}
}

// TestRecordJobRun_countsConsecutiveFailures covers what makes silent repeated
// failure visible: failures accumulate and keep the last success in place, and a
// success clears the count.
func TestRecordJobRun_countsConsecutiveFailures(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	client, pool := lifecycleDB(t)
	ctx := context.Background()

	success := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Millisecond)
	if err := client.RecordJobRun(ctx, "purge-drain", nil, success); err != nil {
		t.Fatalf("record success: %v", err)
	}

	failedAt := success.Add(time.Hour)
	for i := range 2 {
		if err := client.RecordJobRun(ctx, "purge-drain", errors.New("cloudflare unreachable"), failedAt); err != nil {
			t.Fatalf("record failure %d: %v", i+1, err)
		}
	}

	row, ok := readJobRun(t, pool, "purge-drain")
	if !ok {
		t.Fatal("no row after failures")
	}
	if row.consecutiveFailures != 2 {
		t.Errorf("consecutive_failures = %d, want 2", row.consecutiveFailures)
	}
	if row.lastError == nil || *row.lastError != "cloudflare unreachable" {
		t.Errorf("last_error = %v, want the failure text", row.lastError)
	}
	if row.lastSuccessAt == nil || !row.lastSuccessAt.Equal(success) {
		t.Errorf("last_success_at = %v, want the earlier success %s left intact", row.lastSuccessAt, success)
	}

	recovered := failedAt.Add(time.Hour)
	if err := client.RecordJobRun(ctx, "purge-drain", nil, recovered); err != nil {
		t.Fatalf("record recovery: %v", err)
	}
	row, _ = readJobRun(t, pool, "purge-drain")
	if row.consecutiveFailures != 0 {
		t.Errorf("consecutive_failures = %d after a success, want 0", row.consecutiveFailures)
	}
	if row.lastFailureAt == nil || !row.lastFailureAt.Equal(failedAt) {
		t.Errorf("last_failure_at = %v, want the failure %s kept", row.lastFailureAt, failedAt)
	}
}
