package db

import (
	"context"
	"crypto/sha256"
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

// DocumentStatus is the lifecycle state of a document row.
type DocumentStatus string

const (
	StatusPending   DocumentStatus = "pending"
	StatusPublished DocumentStatus = "published"
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

// InsertPendingParams holds the values for InsertPending.
type InsertPendingParams struct {
	Slug           string
	IdempotencyKey string
	Manifest       manifest.Manifest
	EditToken      string // plaintext; stored as SHA256(token)
}

// InsertPending inserts a documents row with status='pending'.
// On idempotency_key conflict (DO NOTHING) it fetches and returns the existing row.
func (c *Client) InsertPending(ctx context.Context, p InsertPendingParams) (*Document, error) {
	manifestJSON, err := json.Marshal(p.Manifest)
	if err != nil {
		return nil, fmt.Errorf("marshal manifest: %w", err)
	}
	mhRaw := sha256.Sum256(manifestJSON)
	manifestHash := mhRaw[:]

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

	row := c.pool.QueryRow(ctx, q,
		p.Slug, p.IdempotencyKey, manifestJSON, manifestHash,
		editTokenHash, bytesTotal, fileCount,
	)
	doc, err := scanDocument(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return c.GetByIdempotencyKey(ctx, p.IdempotencyKey)
		}
		return nil, fmt.Errorf("insert pending: %w", err)
	}
	return doc, nil
}

// GetByIdempotencyKey returns the document row matching key, or ErrNotFound.
func (c *Client) GetByIdempotencyKey(ctx context.Context, key string) (*Document, error) {
	const q = `
SELECT id, slug, idempotency_key, status,
       manifest, manifest_hash, edit_token_hash,
       bytes_total, file_count,
       expires_at, created_at, updated_at
FROM documents
WHERE idempotency_key = $1`

	row := c.pool.QueryRow(ctx, q, key)
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
func (c *Client) Publish(ctx context.Context, idempotencyKey string, expiresAt time.Time) (*Document, error) {
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

	row := c.pool.QueryRow(ctx, q, idempotencyKey, expiresAt)
	doc, err := scanDocument(row)
	if err == nil {
		return doc, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("publish: %w", err)
	}
	doc, err = c.GetByIdempotencyKey(ctx, idempotencyKey)
	if err != nil {
		return nil, err
	}
	if doc.Status == StatusPublished {
		return doc, nil
	}
	return nil, ErrNotFound
}

// GetBySlug returns the published document row for slug, or ErrNotFound.
func (c *Client) GetBySlug(ctx context.Context, sl string) (*Document, error) {
	const q = `
SELECT id, slug, idempotency_key, status,
       manifest, manifest_hash, edit_token_hash,
       bytes_total, file_count,
       expires_at, created_at, updated_at
FROM documents
WHERE slug = $1 AND status = 'published'`

	row := c.pool.QueryRow(ctx, q, sl)
	doc, err := scanDocument(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get by slug: %w", err)
	}
	return doc, nil
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
