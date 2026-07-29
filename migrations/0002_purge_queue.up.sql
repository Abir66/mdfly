-- Durable CDN purge queue (ADR-0012). One row per slug awaiting invalidation:
-- the slug primary key deduplicates repeated writes to the same document, and
-- next_attempt_at carries the retry backoff the drain job orders by.
CREATE TABLE purge_queue (
    slug            TEXT PRIMARY KEY,
    enqueued_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    attempts        INT NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Work-list for the drain pass: rows whose next attempt is due.
CREATE INDEX purge_queue_due_idx ON purge_queue (next_attempt_at);
