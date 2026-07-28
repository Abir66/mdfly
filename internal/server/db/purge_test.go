package db_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// purgeRow is the queue state of one slug.
type purgeRow struct {
	attempts      int
	nextAttemptAt time.Time
}

func readPurgeRow(t *testing.T, pool *pgxpool.Pool, slug string) (purgeRow, bool) {
	t.Helper()
	var r purgeRow
	err := pool.QueryRow(context.Background(),
		`SELECT attempts, next_attempt_at FROM purge_queue WHERE slug = $1`, slug).
		Scan(&r.attempts, &r.nextAttemptAt)
	if err != nil {
		return purgeRow{}, false
	}
	return r, true
}

func countPurgeRows(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM purge_queue`).Scan(&n); err != nil {
		t.Fatalf("count purge rows: %v", err)
	}
	return n
}

// TestEnqueuePurge_dedupesBySlug covers the slug primary key: repeated writes to
// one document collapse to a single row, and the re-enqueue resets a backed-off
// row so the fresh content is retried on the next pass.
func TestEnqueuePurge_dedupesBySlug(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	client, pool := lifecycleDB(t)
	ctx := context.Background()

	if err := client.EnqueuePurge(ctx, "abc123"); err != nil {
		t.Fatalf("first enqueue: %v", err)
	}
	future := time.Now().Add(time.Hour)
	if err := client.BackoffPurge(ctx, "abc123", 0, future); err != nil {
		t.Fatalf("backoff: %v", err)
	}
	if err := client.EnqueuePurge(ctx, "abc123"); err != nil {
		t.Fatalf("second enqueue: %v", err)
	}

	if got := countPurgeRows(t, pool); got != 1 {
		t.Fatalf("purge_queue rows = %d, want 1", got)
	}
	row, ok := readPurgeRow(t, pool, "abc123")
	if !ok {
		t.Fatal("purge row missing after re-enqueue")
	}
	if row.attempts != 0 {
		t.Errorf("attempts = %d after re-enqueue, want 0", row.attempts)
	}
	if !row.nextAttemptAt.Before(future) {
		t.Errorf("next_attempt_at = %s, want reset before the backed-off %s", row.nextAttemptAt, future)
	}
}

// TestEnqueuePurge_rollsBackWithTheRowFlip pins the transactional contract: an
// enqueue that shares the flip's transaction disappears with it, so a rolled-back
// write never leaves a purge behind.
func TestEnqueuePurge_rollsBackWithTheRowFlip(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	client, pool := lifecycleDB(t)
	ctx := context.Background()
	seedDocument(t, pool, seedRow{slug: "rollback1", status: "published"})

	tx, err := client.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := tx.SoftDelete(ctx, "rollback1"); err != nil {
		t.Fatalf("soft delete in tx: %v", err)
	}
	if err := tx.EnqueuePurge(ctx, "rollback1"); err != nil {
		t.Fatalf("enqueue in tx: %v", err)
	}
	tx.Rollback(ctx)

	if got := countPurgeRows(t, pool); got != 0 {
		t.Errorf("purge_queue rows = %d after rollback, want 0", got)
	}
	if got := statusOf(t, pool, "rollback1"); got != "published" {
		t.Errorf("status = %q after rollback, want published", got)
	}
}

// TestEnqueuePurge_commitsWithTheRowFlip is the committed counterpart: both the
// flip and its enqueue survive.
func TestEnqueuePurge_commitsWithTheRowFlip(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	client, pool := lifecycleDB(t)
	ctx := context.Background()
	seedDocument(t, pool, seedRow{slug: "commit1", status: "published"})

	tx, err := client.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.SoftDelete(ctx, "commit1"); err != nil {
		t.Fatalf("soft delete in tx: %v", err)
	}
	if err := tx.EnqueuePurge(ctx, "commit1"); err != nil {
		t.Fatalf("enqueue in tx: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	if _, ok := readPurgeRow(t, pool, "commit1"); !ok {
		t.Error("purge row missing after commit")
	}
	if got := statusOf(t, pool, "commit1"); got != "deleted" {
		t.Errorf("status = %q after commit, want deleted", got)
	}
}

// TestClaimPurgeDue covers the drain work-list: only rows whose next_attempt_at
// has arrived, soonest first, bounded by the batch limit.
func TestClaimPurgeDue(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	client, _ := lifecycleDB(t)
	ctx := context.Background()
	now := time.Now()

	for _, slug := range []string{"due-old", "due-new", "not-yet"} {
		if err := client.EnqueuePurge(ctx, slug); err != nil {
			t.Fatalf("enqueue %s: %v", slug, err)
		}
	}
	if err := client.BackoffPurge(ctx, "due-old", 0, now.Add(-2*time.Hour)); err != nil {
		t.Fatalf("backoff due-old: %v", err)
	}
	if err := client.BackoffPurge(ctx, "due-new", 0, now.Add(-time.Hour)); err != nil {
		t.Fatalf("backoff due-new: %v", err)
	}
	if err := client.BackoffPurge(ctx, "not-yet", 0, now.Add(time.Hour)); err != nil {
		t.Fatalf("backoff not-yet: %v", err)
	}

	tasks, err := client.ClaimPurgeDue(ctx, now, 10)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("claimed %d tasks, want 2 (%v)", len(tasks), tasks)
	}
	if tasks[0].Slug != "due-old" || tasks[1].Slug != "due-new" {
		t.Errorf("claim order = %s, %s; want due-old, due-new", tasks[0].Slug, tasks[1].Slug)
	}
	if tasks[0].Attempts != 1 {
		t.Errorf("attempts = %d, want the backed-off 1", tasks[0].Attempts)
	}

	limited, err := client.ClaimPurgeDue(ctx, now, 1)
	if err != nil {
		t.Fatalf("claim with limit: %v", err)
	}
	if len(limited) != 1 {
		t.Errorf("claimed %d tasks under limit 1", len(limited))
	}
}

// TestMarkPurgeDone covers success: the row leaves the queue, and clearing an
// already-cleared slug is a no-op rather than an error.
func TestMarkPurgeDone(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	client, pool := lifecycleDB(t)
	ctx := context.Background()

	if err := client.EnqueuePurge(ctx, "done1"); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := client.MarkPurgeDone(ctx, "done1", 0); err != nil {
		t.Fatalf("mark done: %v", err)
	}
	if got := countPurgeRows(t, pool); got != 0 {
		t.Errorf("purge_queue rows = %d after mark done, want 0", got)
	}
	if err := client.MarkPurgeDone(ctx, "done1", 0); err != nil {
		t.Errorf("re-mark done: %v", err)
	}
}

// TestBackoffPurge covers failure: the row stays queued with one more attempt
// and a later due time.
func TestBackoffPurge(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	client, pool := lifecycleDB(t)
	ctx := context.Background()

	if err := client.EnqueuePurge(ctx, "retry1"); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	next := time.Now().Add(30 * time.Minute)
	if err := client.BackoffPurge(ctx, "retry1", 0, next); err != nil {
		t.Fatalf("backoff: %v", err)
	}

	row, ok := readPurgeRow(t, pool, "retry1")
	if !ok {
		t.Fatal("purge row deleted by backoff")
	}
	if row.attempts != 1 {
		t.Errorf("attempts = %d, want 1", row.attempts)
	}
	if row.nextAttemptAt.Sub(next).Abs() > time.Second {
		t.Errorf("next_attempt_at = %s, want ~%s", row.nextAttemptAt, next)
	}
}

// TestPurgeCompletion_fencesStaleClaims covers the attempts fence on both
// completion paths: a claim carrying an attempts value the row no longer has lost
// the race to a fresh write, so neither the clear nor the backoff touches the row.
func TestPurgeCompletion_fencesStaleClaims(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	client, pool := lifecycleDB(t)
	ctx := context.Background()

	if err := client.EnqueuePurge(ctx, "raced1"); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	const staleAttempts = 3
	if err := client.MarkPurgeDone(ctx, "raced1", staleAttempts); err != nil {
		t.Fatalf("stale mark done: %v", err)
	}
	if got := countPurgeRows(t, pool); got != 1 {
		t.Fatalf("purge_queue rows = %d after a stale clear, want the row kept", got)
	}

	future := time.Now().Add(time.Hour)
	if err := client.BackoffPurge(ctx, "raced1", staleAttempts, future); err != nil {
		t.Fatalf("stale backoff: %v", err)
	}
	row, ok := readPurgeRow(t, pool, "raced1")
	if !ok {
		t.Fatal("purge row deleted by a stale backoff")
	}
	if row.attempts != 0 {
		t.Errorf("attempts = %d after a stale backoff, want 0", row.attempts)
	}
	if !row.nextAttemptAt.Before(future) {
		t.Errorf("next_attempt_at = %s, want the enqueued time, not the stale %s", row.nextAttemptAt, future)
	}

	if err := client.MarkPurgeDone(ctx, "raced1", 0); err != nil {
		t.Fatalf("matching mark done: %v", err)
	}
	if got := countPurgeRows(t, pool); got != 0 {
		t.Errorf("purge_queue rows = %d after a matching clear, want 0", got)
	}
}

// TestClaimPurgeDue_emptyQueue keeps the drain's zero case explicit: no rows is
// not an error.
func TestClaimPurgeDue_emptyQueue(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	client, _ := lifecycleDB(t)

	tasks, err := client.ClaimPurgeDue(context.Background(), time.Now(), 10)
	if err != nil {
		t.Fatalf("claim on empty queue: %v", err)
	}
	if len(tasks) != 0 {
		t.Errorf("claimed %d tasks from an empty queue", len(tasks))
	}
}
