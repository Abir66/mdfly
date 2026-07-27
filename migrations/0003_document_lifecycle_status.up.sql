ALTER TABLE documents DROP CONSTRAINT documents_status_check;
ALTER TABLE documents ADD CONSTRAINT documents_status_check
    CHECK (status IN ('pending', 'published', 'deleted', 'expired', 'abandoned'));

ALTER TABLE documents ADD COLUMN blobs_deleted_at TIMESTAMPTZ;

-- Work-list for the blob-deletion pass (ADR-0030): terminal rows whose R2 blobs
-- are still present. updated_at is the transition timestamp for all three
-- terminal statuses, so it orders the per-status grace window.
CREATE INDEX documents_blob_gc_idx
    ON documents (updated_at)
    WHERE blobs_deleted_at IS NULL AND status IN ('deleted', 'expired', 'abandoned');
