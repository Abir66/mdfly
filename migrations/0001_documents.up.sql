CREATE TABLE documents (
    id               BIGSERIAL PRIMARY KEY,
    slug             TEXT NOT NULL UNIQUE,
    idempotency_key  UUID NOT NULL UNIQUE,
    status           TEXT NOT NULL CHECK (status IN ('pending', 'published', 'deleted', 'expired', 'abandoned')),
    owner_user_id    BIGINT,
    edit_token_hash  BYTEA,
    manifest         JSONB NOT NULL,
    manifest_hash    BYTEA NOT NULL,
    title            TEXT,
    excerpt          TEXT,
    og_image_hash    BYTEA,
    bytes_total      BIGINT NOT NULL,
    file_count       INT NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at       TIMESTAMPTZ,
    deleted_at       TIMESTAMPTZ,
    blobs_deleted_at TIMESTAMPTZ
);

CREATE INDEX documents_pending_gc_idx
    ON documents (created_at)
    WHERE status = 'pending';

CREATE INDEX documents_anon_expiry_idx
    ON documents (expires_at)
    WHERE expires_at IS NOT NULL AND deleted_at IS NULL;

CREATE INDEX documents_owner_idx
    ON documents (owner_user_id, updated_at DESC)
    WHERE owner_user_id IS NOT NULL AND deleted_at IS NULL;

-- Work-list for the blob-deletion pass (ADR-0005): terminal rows whose R2 blobs
-- are still present. updated_at is the transition timestamp for all three
-- terminal statuses, so it orders the per-status grace window.
CREATE INDEX documents_blob_gc_idx
    ON documents (updated_at)
    WHERE blobs_deleted_at IS NULL AND status IN ('deleted', 'expired', 'abandoned');
