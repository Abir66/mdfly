package db

import (
	"context"
	"fmt"
	"time"
)

// PurgeTask is one queued CDN invalidation claimed by the drain pass.
type PurgeTask struct {
	Slug     string
	Attempts int
}

// EnqueuePurge records that slug's edge cache needs invalidating (ADR-0031).
// Call it on a Tx that also carries the row flip, so a committed write can never
// be left un-enqueued. The slug primary key deduplicates: a second write to the
// same document reuses the row and resets its backoff, because the newest
// content still needs purging.
func (o *ops) EnqueuePurge(ctx context.Context, slug string) error {
	const q = `
INSERT INTO purge_queue (slug) VALUES ($1)
ON CONFLICT (slug) DO UPDATE
SET enqueued_at = now(),
    attempts = 0,
    next_attempt_at = now()`

	if _, err := o.q.Exec(ctx, q, slug); err != nil {
		return fmt.Errorf("enqueue purge: %w", err)
	}
	return nil
}

// ClaimPurgeDue returns up to limit queued slugs whose next attempt is due at
// or before now, soonest first. It reads without leasing the rows: ticks of one
// job never overlap (the runner invokes them sequentially), and a purge is
// idempotent anyway, so a slug claimed twice costs one redundant Cloudflare call
// and nothing else.
func (o *ops) ClaimPurgeDue(ctx context.Context, now time.Time, limit int) ([]PurgeTask, error) {
	const q = `
SELECT slug, attempts
FROM purge_queue
WHERE next_attempt_at <= $1
ORDER BY next_attempt_at
LIMIT $2`

	rows, err := o.q.Query(ctx, q, now, limit)
	if err != nil {
		return nil, fmt.Errorf("claim purge due: %w", err)
	}
	defer rows.Close()

	var out []PurgeTask
	for rows.Next() {
		var t PurgeTask
		if err := rows.Scan(&t.Slug, &t.Attempts); err != nil {
			return nil, fmt.Errorf("scan purge task: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("claim purge due: %w", err)
	}
	return out, nil
}

// MarkPurgeDone drops slug from the queue after a successful purge. attempts is
// the value the row carried when it was claimed and fences the delete: a write
// that landed mid-purge reset the row's attempts, so the delete becomes a no-op
// and the newer content keeps its queued purge. Clearing a slug that is already
// gone is a no-op success, which keeps a duplicated drain harmless.
func (o *ops) MarkPurgeDone(ctx context.Context, slug string, attempts int) error {
	const q = `DELETE FROM purge_queue WHERE slug = $1 AND attempts = $2`

	if _, err := o.q.Exec(ctx, q, slug, attempts); err != nil {
		return fmt.Errorf("mark purge done: %w", err)
	}
	return nil
}

// BackoffPurge keeps slug queued after a failed purge, counting the attempt and
// pushing the next one out to nextAttemptAt. The caller owns the backoff curve.
// attempts fences the update the same way MarkPurgeDone's does, so a stale claim
// cannot push a freshly enqueued purge into the future.
func (o *ops) BackoffPurge(ctx context.Context, slug string, attempts int, nextAttemptAt time.Time) error {
	const q = `
UPDATE purge_queue
SET attempts = attempts + 1,
    next_attempt_at = $3
WHERE slug = $1 AND attempts = $2`

	if _, err := o.q.Exec(ctx, q, slug, attempts, nextAttemptAt); err != nil {
		return fmt.Errorf("backoff purge: %w", err)
	}
	return nil
}
