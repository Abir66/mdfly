package db

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Abir66/mdfly/internal/server/manifest"
)

// ErrNotFound is returned when a document row does not exist.
var ErrNotFound = errors.New("document not found")

// ErrGone is returned when a document row exists but has been soft-deleted.
// Callers map it to 410 Gone, distinct from the 404 of ErrNotFound.
var ErrGone = errors.New("document gone")

// DocumentStatus is the lifecycle state of a document row.
type DocumentStatus string

const (
	StatusPending   DocumentStatus = "pending"
	StatusPublished DocumentStatus = "published"
	// StatusDeleted is a soft-deleted row: the slug stays reserved (never
	// re-mintable) but the document serves 410 (CONTEXT.md "Delete").
	StatusDeleted DocumentStatus = "deleted"
	// StatusExpired is an anonymous row past its expires_at, flipped by the
	// lifecycle GC. Serves 410 like StatusDeleted (ADR-0005).
	StatusExpired DocumentStatus = "expired"
	// StatusAbandoned is a pending row that never committed within the abandon
	// grace. Serves 404 — no URL ever resolved it (ADR-0005).
	StatusAbandoned DocumentStatus = "abandoned"
)

// Document is a row from the documents table.
type Document struct {
	ID             int64
	Slug           string
	IdempotencyKey string
	Status         DocumentStatus
	ManifestJSON   []byte
	ManifestHash   []byte
	EditTokenHash  []byte
	BytesTotal     int64
	FileCount      int
	ExpiresAt      *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// ManifestHashHex returns ManifestHash as a lowercase hex string.
func (d *Document) ManifestHashHex() string {
	return hex.EncodeToString(d.ManifestHash)
}

// MatchesEditToken reports whether the plaintext token matches the row's stored
// edit_token_hash via a constant-time compare (ADR-0008). Returns false when the
// row has no stored hash or the token is empty; callers map those to 401/403.
func (d *Document) MatchesEditToken(token string) bool {
	if token == "" || len(d.EditTokenHash) == 0 {
		return false
	}
	presented := sha256.Sum256([]byte(token))
	return subtle.ConstantTimeCompare(presented[:], d.EditTokenHash) == 1
}

// ManifestHash returns sha256(json.Marshal(m)), the canonical content hash
// stored in the manifest_hash column. It is the single definition of payload
// identity used for idempotency mismatch detection (ADR-0006).
func ManifestHash(m manifest.Manifest) ([]byte, error) {
	_, h, err := marshalManifest(m)
	return h, err
}

func marshalManifest(m manifest.Manifest) (manifestJSON []byte, hash []byte, err error) {
	manifestJSON, err = json.Marshal(m)
	if err != nil {
		return nil, nil, err
	}
	sum := sha256.Sum256(manifestJSON)
	return manifestJSON, sum[:], nil
}

// InsertPendingParams holds the values for InsertPending.
type InsertPendingParams struct {
	Slug           string
	IdempotencyKey string
	Manifest       manifest.Manifest
	EditToken      string // plaintext; stored as SHA256(token)
}

// InsertPending inserts a documents row with status='pending'.
// On idempotency_key conflict (DO NOTHING) it fetches and returns the existing row.
func (o *ops) InsertPending(ctx context.Context, p InsertPendingParams) (*Document, error) {
	manifestJSON, manifestHash, err := marshalManifest(p.Manifest)
	if err != nil {
		return nil, fmt.Errorf("marshal manifest: %w", err)
	}

	var bytesTotal int64
	for _, f := range p.Manifest.FilesByPath {
		bytesTotal += f.Size
	}
	fileCount := len(p.Manifest.FilesByPath)

	var editTokenHash []byte
	if p.EditToken != "" {
		h := sha256.Sum256([]byte(p.EditToken))
		editTokenHash = h[:]
	}

	const q = `
INSERT INTO documents (
	slug, idempotency_key, status,
	manifest, manifest_hash,
	edit_token_hash,
	bytes_total, file_count
) VALUES ($1, $2, 'pending', $3, $4, $5, $6, $7)
ON CONFLICT (idempotency_key) DO NOTHING
RETURNING id, slug, idempotency_key, status,
          manifest, manifest_hash, edit_token_hash,
          bytes_total, file_count,
          expires_at, created_at, updated_at`

	row := o.q.QueryRow(ctx, q,
		p.Slug, p.IdempotencyKey, manifestJSON, manifestHash,
		editTokenHash, bytesTotal, fileCount,
	)
	doc, err := scanDocument(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return o.GetByIdempotencyKey(ctx, p.IdempotencyKey)
		}
		return nil, fmt.Errorf("insert pending: %w", err)
	}
	return doc, nil
}

// GetByIdempotencyKey returns the document row matching key, or ErrNotFound.
func (o *ops) GetByIdempotencyKey(ctx context.Context, key string) (*Document, error) {
	const q = `
SELECT id, slug, idempotency_key, status,
       manifest, manifest_hash, edit_token_hash,
       bytes_total, file_count,
       expires_at, created_at, updated_at
FROM documents
WHERE idempotency_key = $1`

	row := o.q.QueryRow(ctx, q, key)
	doc, err := scanDocument(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get by idempotency key: %w", err)
	}
	return doc, nil
}

// Publish atomically flips status to 'published' and sets expires_at.
// If the row is already published, returns it as-is (terminal-state idempotency).
// Returns ErrNotFound if no pending row matches key.
func (o *ops) Publish(ctx context.Context, idempotencyKey string, expiresAt time.Time) (*Document, error) {
	const q = `
UPDATE documents
SET status = 'published',
    expires_at = $2,
    updated_at = now()
WHERE idempotency_key = $1
  AND status = 'pending'
RETURNING id, slug, idempotency_key, status,
          manifest, manifest_hash, edit_token_hash,
          bytes_total, file_count,
          expires_at, created_at, updated_at`

	row := o.q.QueryRow(ctx, q, idempotencyKey, expiresAt)
	doc, err := scanDocument(row)
	if err == nil {
		return doc, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("publish: %w", err)
	}
	doc, err = o.GetByIdempotencyKey(ctx, idempotencyKey)
	if err != nil {
		return nil, err
	}
	if doc.Status == StatusPublished {
		return doc, nil
	}
	return nil, ErrNotFound
}

// UpdateManifestParams holds the values for UpdateManifest.
type UpdateManifestParams struct {
	Slug               string
	Manifest           manifest.Manifest
	ParentManifestHash []byte // nil skips the optimistic guard (--force)
	ExpiresAt          time.Time
}

// UpdateManifest atomically overwrites a published row's manifest and derived
// counters (bytes_total, file_count), refreshing updated_at and expires_at;
// slug and URL are unchanged (ADR-0006). When ParentManifestHash is non-nil the
// UPDATE is guarded by manifest_hash = parent (optimistic concurrency);
// a nil parent (--force) drops the guard. Returns ErrNotFound when no published
// row matches — the slug is gone, or a concurrent write moved manifest_hash off
// the parent (which the caller maps to a 409 conflict).
func (o *ops) UpdateManifest(ctx context.Context, p UpdateManifestParams) (*Document, error) {
	manifestJSON, manifestHash, err := marshalManifest(p.Manifest)
	if err != nil {
		return nil, fmt.Errorf("marshal manifest: %w", err)
	}

	var bytesTotal int64
	for _, f := range p.Manifest.FilesByPath {
		bytesTotal += f.Size
	}
	fileCount := len(p.Manifest.FilesByPath)

	const q = `
UPDATE documents
SET manifest = $2,
    manifest_hash = $3,
    bytes_total = $4,
    file_count = $5,
    expires_at = $6,
    updated_at = now()
WHERE slug = $1
  AND status = 'published'
  AND ($7::bytea IS NULL OR manifest_hash = $7)
RETURNING id, slug, idempotency_key, status,
          manifest, manifest_hash, edit_token_hash,
          bytes_total, file_count,
          expires_at, created_at, updated_at`

	row := o.q.QueryRow(ctx, q,
		p.Slug, manifestJSON, manifestHash, bytesTotal, fileCount,
		p.ExpiresAt, p.ParentManifestHash,
	)
	doc, err := scanDocument(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("update manifest: %w", err)
	}
	return doc, nil
}

// GetBySlug returns the viewable document row for slug. A row that was once
// public but is gone — 'deleted' or 'expired' — yields ErrGone (→ 410); a
// missing row, or one that was never public ('pending', 'abandoned'), yields
// ErrNotFound (→ 404). Only 'published' rows are served (ADR-0005).
func (o *ops) GetBySlug(ctx context.Context, sl string) (*Document, error) {
	doc, err := o.GetBySlugAny(ctx, sl)
	if err != nil {
		return nil, err
	}
	switch doc.Status {
	case StatusPublished:
		return doc, nil
	case StatusDeleted, StatusExpired:
		return nil, ErrGone
	case StatusPending, StatusAbandoned:
		return nil, ErrNotFound
	default:
		return nil, ErrNotFound
	}
}

// GetManifestBySlug returns the viewable document row for slug together with its
// decoded Bundle Manifest. It layers manifest unmarshalling onto GetBySlug so the
// view and raw read paths share one loader and one error vocabulary — ErrNotFound
// (→ 404) and ErrGone (→ 410) pass through, and a malformed stored manifest (a
// server-side invariant break) surfaces as a wrapped error.
func (o *ops) GetManifestBySlug(ctx context.Context, sl string) (*Document, manifest.Manifest, error) {
	doc, err := o.GetBySlug(ctx, sl)
	if err != nil {
		return nil, manifest.Manifest{}, err
	}
	var m manifest.Manifest
	if err := json.Unmarshal(doc.ManifestJSON, &m); err != nil {
		return nil, manifest.Manifest{}, fmt.Errorf("unmarshal manifest for %s: %w", sl, err)
	}
	return doc, m, nil
}

// GetBySlugAny returns the document row for slug regardless of status, or
// ErrNotFound if no row exists. Used by the delete workflow, which must read a
// terminal row to stay idempotent.
func (o *ops) GetBySlugAny(ctx context.Context, sl string) (*Document, error) {
	const q = `
SELECT id, slug, idempotency_key, status,
       manifest, manifest_hash, edit_token_hash,
       bytes_total, file_count,
       expires_at, created_at, updated_at
FROM documents
WHERE slug = $1`

	row := o.q.QueryRow(ctx, q, sl)
	doc, err := scanDocument(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get by slug: %w", err)
	}
	return doc, nil
}

// SoftDelete flips slug's row to 'deleted' and stamps deleted_at, keeping the
// slug reserved (ADR-0001). Returns the updated row, or ErrNotFound if no row
// exists. Re-deleting an already-deleted row is a no-op that returns it as-is.
func (o *ops) SoftDelete(ctx context.Context, sl string) (*Document, error) {
	const q = `
UPDATE documents
SET status = 'deleted',
    deleted_at = COALESCE(deleted_at, now()),
    updated_at = now()
WHERE slug = $1
RETURNING id, slug, idempotency_key, status,
          manifest, manifest_hash, edit_token_hash,
          bytes_total, file_count,
          expires_at, created_at, updated_at`

	row := o.q.QueryRow(ctx, q, sl)
	doc, err := scanDocument(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("soft delete: %w", err)
	}
	return doc, nil
}

// MarkExpired flips up to limit anonymous published rows whose expires_at is at
// or before now to 'expired' (ADR-0005) and returns how many moved. Owned
// Documents carry no expires_at, so the predicate — which matches
// documents_anon_expiry_idx — never selects them. The status guard keeps
// repeated passes idempotent: an already-expired row is not re-selected.
// Candidates are locked with FOR UPDATE SKIP LOCKED, so a concurrent sweep or
// SoftDelete on the same row is stepped over rather than waited on.
func (o *ops) MarkExpired(ctx context.Context, now time.Time, limit int) (int64, error) {
	const q = `
WITH candidates AS (
	SELECT id FROM documents
	WHERE expires_at IS NOT NULL
	  AND deleted_at IS NULL
	  AND status = 'published'
	  AND expires_at <= $1
	ORDER BY expires_at
	LIMIT $2
	FOR UPDATE SKIP LOCKED
)
UPDATE documents d
SET status = 'expired',
    updated_at = now()
FROM candidates c
WHERE d.id = c.id`

	tag, err := o.q.Exec(ctx, q, now, limit)
	if err != nil {
		return 0, fmt.Errorf("mark expired: %w", err)
	}
	return tag.RowsAffected(), nil
}

// MarkAbandoned flips up to limit pending rows created at or before olderThan to
// 'abandoned' (ADR-0005) and returns how many moved. The predicate matches
// documents_pending_gc_idx, and the status guard makes repeated passes
// idempotent. Candidates are locked like MarkExpired's, so a concurrent Publish
// or sweep on the same row is skipped rather than blocked.
func (o *ops) MarkAbandoned(ctx context.Context, olderThan time.Time, limit int) (int64, error) {
	const q = `
WITH candidates AS (
	SELECT id FROM documents
	WHERE status = 'pending'
	  AND created_at <= $1
	ORDER BY created_at
	LIMIT $2
	FOR UPDATE SKIP LOCKED
)
UPDATE documents d
SET status = 'abandoned',
    updated_at = now()
FROM candidates c
WHERE d.id = c.id`

	tag, err := o.q.Exec(ctx, q, olderThan, limit)
	if err != nil {
		return 0, fmt.Errorf("mark abandoned: %w", err)
	}
	return tag.RowsAffected(), nil
}

// BlobGCCandidate is a terminal row whose R2 blobs are still present.
type BlobGCCandidate struct {
	ID     int64
	Slug   string
	Status DocumentStatus
}

// ListBlobGCCandidates returns up to limit terminal rows whose blobs still need
// deleting, oldest transition first (ADR-0005). 'abandoned' rows qualify at
// once — no URL ever served them — while 'deleted' and 'expired' rows wait
// until their transition timestamp is at or before gracedBefore. The predicate
// matches documents_blob_gc_idx, and rows already stamped with blobs_deleted_at
// are excluded, so a swept row is never re-listed.
func (o *ops) ListBlobGCCandidates(ctx context.Context, gracedBefore time.Time, limit int) ([]BlobGCCandidate, error) {
	const q = `
SELECT id, slug, status
FROM documents
WHERE blobs_deleted_at IS NULL
  AND status IN ('deleted', 'expired', 'abandoned')
  AND (status = 'abandoned' OR updated_at <= $1)
ORDER BY updated_at
LIMIT $2`

	rows, err := o.q.Query(ctx, q, gracedBefore, limit)
	if err != nil {
		return nil, fmt.Errorf("list blob gc candidates: %w", err)
	}
	defer rows.Close()

	var out []BlobGCCandidate
	for rows.Next() {
		var cand BlobGCCandidate
		if err := rows.Scan(&cand.ID, &cand.Slug, &cand.Status); err != nil {
			return nil, fmt.Errorf("scan blob gc candidate: %w", err)
		}
		out = append(out, cand)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list blob gc candidates: %w", err)
	}
	return out, nil
}

// SetBlobsDeletedAt records that a row's R2 blobs are gone, dropping it from the
// blob-GC work-list. updated_at is deliberately left alone: it is the transition
// timestamp the grace windows order by. Re-stamping an already-stamped row is a
// no-op success.
func (o *ops) SetBlobsDeletedAt(ctx context.Context, id int64) error {
	const q = `
UPDATE documents
SET blobs_deleted_at = now()
WHERE id = $1 AND blobs_deleted_at IS NULL`

	if _, err := o.q.Exec(ctx, q, id); err != nil {
		return fmt.Errorf("set blobs deleted at: %w", err)
	}
	return nil
}

func scanDocument(row pgx.Row) (*Document, error) {
	var d Document
	err := row.Scan(
		&d.ID, &d.Slug, &d.IdempotencyKey, &d.Status,
		&d.ManifestJSON, &d.ManifestHash, &d.EditTokenHash,
		&d.BytesTotal, &d.FileCount,
		&d.ExpiresAt, &d.CreatedAt, &d.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &d, nil
}
