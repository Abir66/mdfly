DROP INDEX documents_blob_gc_idx;
ALTER TABLE documents DROP COLUMN blobs_deleted_at;

-- The re-added constraint forbids the two new statuses, so those rows must move
-- to a value the narrower vocabulary holds. 'expired' becomes 'deleted' (both
-- serve 410); 'abandoned' returns to 'pending', the state it was flipped from.
-- This is lossy — the rollback cannot tell the two apart afterwards.
UPDATE documents
SET status = 'deleted', deleted_at = COALESCE(deleted_at, now())
WHERE status = 'expired';
UPDATE documents SET status = 'pending' WHERE status = 'abandoned';

ALTER TABLE documents DROP CONSTRAINT documents_status_check;
ALTER TABLE documents ADD CONSTRAINT documents_status_check
    CHECK (status IN ('pending', 'published', 'deleted'));
