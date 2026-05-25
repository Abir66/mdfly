package postgres

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	_ "github.com/lib/pq"

	"github.com/Abir66/mdfly/internal/api"
)

// ErrNotFound is returned when a document row does not exist.
var ErrNotFound = errors.New("document not found")

// Document is a row from the documents table.
type Document struct {
	ID             int64
	Slug           string
	IdempotencyKey string
	Status         string
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

// Store wraps a *sql.DB for documents-table operations.
type Store struct {
	db *sql.DB
}

// New returns a Store backed by db.
func New(db *sql.DB) *Store {
	return &Store{db: db}
}

// NewFromDSN opens a *sql.DB from dsn and returns a Store.
func NewFromDSN(dsn string) (*Store, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return New(db), nil
}

// Close closes the underlying DB.
func (s *Store) Close() error { return s.db.Close() }

// InsertPendingParams holds the values for InsertPending.
type InsertPendingParams struct {
	Slug           string
	IdempotencyKey string
	Manifest       api.Manifest
	EditToken      string // plaintext; stored as SHA256(token)
}

// InsertPending inserts a documents row with status='pending'.
// On idempotency_key conflict (DO NOTHING) it fetches and returns the existing row.
func (s *Store) InsertPending(ctx context.Context, p InsertPendingParams) (*Document, error) {
	manifestJSON, err := json.Marshal(p.Manifest)
	if err != nil {
		return nil, fmt.Errorf("marshal manifest: %w", err)
	}
	mhRaw := sha256.Sum256(manifestJSON)
	manifestHash := mhRaw[:]

	var bytesTotal int64
	for _, f := range p.Manifest.Files {
		bytesTotal += f.Size
	}
	fileCount := len(p.Manifest.Files)

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

	row := s.db.QueryRowContext(ctx, q,
		p.Slug, p.IdempotencyKey, manifestJSON, manifestHash,
		editTokenHash, bytesTotal, fileCount,
	)
	doc, err := scanDocument(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Conflict: row already exists; fetch it.
			return s.GetByIdempotencyKey(ctx, p.IdempotencyKey)
		}
		return nil, fmt.Errorf("insert pending: %w", err)
	}
	return doc, nil
}

// GetByIdempotencyKey returns the document row matching key, or ErrNotFound.
func (s *Store) GetByIdempotencyKey(ctx context.Context, key string) (*Document, error) {
	const q = `
SELECT id, slug, idempotency_key, status,
       manifest, manifest_hash, edit_token_hash,
       bytes_total, file_count,
       expires_at, created_at, updated_at
FROM documents
WHERE idempotency_key = $1`

	row := s.db.QueryRowContext(ctx, q, key)
	doc, err := scanDocument(row)
	if errors.Is(err, sql.ErrNoRows) {
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
func (s *Store) Publish(ctx context.Context, idempotencyKey string, expiresAt time.Time) (*Document, error) {
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

	row := s.db.QueryRowContext(ctx, q, idempotencyKey, expiresAt)
	doc, err := scanDocument(row)
	if err == nil {
		return doc, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("publish: %w", err)
	}
	// Either already published or not found.
	doc, err = s.GetByIdempotencyKey(ctx, idempotencyKey)
	if err != nil {
		return nil, err
	}
	if doc.Status == "published" {
		return doc, nil
	}
	return nil, ErrNotFound
}

func scanDocument(row *sql.Row) (*Document, error) {
	var d Document
	var editTokenHash []byte
	var expiresAt sql.NullTime
	err := row.Scan(
		&d.ID, &d.Slug, &d.IdempotencyKey, &d.Status,
		&d.ManifestJSON, &d.ManifestHash, &editTokenHash,
		&d.BytesTotal, &d.FileCount,
		&expiresAt, &d.CreatedAt, &d.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	d.EditTokenHash = editTokenHash
	if expiresAt.Valid {
		t := expiresAt.Time
		d.ExpiresAt = &t
	}
	return &d, nil
}
