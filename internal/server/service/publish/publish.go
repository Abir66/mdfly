// Package publish runs the 3-phase publish wire protocol (CONTEXT.md "Publish",
// ADR-0011): init mints a slug + presigned PUTs, commit HEAD-verifies blobs and
// atomically flips the row to 'published'. HTTP handlers in
// internal/server/handlers are thin glue around Service.
package publish

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/Abir66/mdfly/internal/api"
	"github.com/Abir66/mdfly/internal/server/db"
	"github.com/Abir66/mdfly/internal/server/httpx"
	"github.com/Abir66/mdfly/internal/server/manifest"
	"github.com/Abir66/mdfly/internal/server/storage"
)

const (
	anonExpiresIn = 30 * 24 * time.Hour
	presignTTL    = 10 * time.Minute
	slugRetries   = 10
)

// Purger runs the best-effort CDN purge that follows a committed write. The
// durable queue row written inside the commit transaction is the real path, so a
// nil Purger only delays invalidation to the next drain tick (ADR-0031).
type Purger interface {
	AttemptInline(ctx context.Context, slug string)
}

// Service executes the publish workflow. Init and Commit return *httpx.Error on
// any failure so handlers can write the response without re-mapping.
type Service struct {
	Db      *db.Client
	Storage *storage.Client
	BaseURL string // e.g. "https://mdfly.dev"
	Purge   Purger
}

// purgeSoon kicks the inline purge if one is wired.
func (s *Service) purgeSoon(ctx context.Context, slug string) {
	if s.Purge == nil {
		return
	}
	s.Purge.AttemptInline(ctx, slug)
}

// InitResult is the data the Init handler serializes to the wire.
type InitResult struct {
	Slug          string
	PresignedURLs map[string]string
	InlineAccept  bool
}

// CommitResult is the data the Commit handler serializes to the wire.
type CommitResult struct {
	URL          string
	Slug         string
	ManifestHash string
}

// Init runs the init phase of the publish protocol.
func (s *Service) Init(ctx context.Context, req api.InitRequest) (InitResult, *httpx.Error) {
	if herr := ValidateInit(req); herr != nil {
		return InitResult{}, herr
	}

	mfst, err := manifest.FromDTO(req.Bundle)
	if err != nil {
		return InitResult{}, httpx.BadRequest("bundle.root_path not present in bundle.files")
	}

	sl, err := generateSlug()
	if err != nil {
		return InitResult{}, httpx.Internal("slug generation failed")
	}

	doc, err := s.Db.InsertPending(ctx, db.InsertPendingParams{
		Slug:           sl,
		IdempotencyKey: req.IdempotencyKey,
		Manifest:       mfst,
		EditToken:      req.EditToken,
	})
	if err != nil {
		return InitResult{}, httpx.Internal("failed to create document")
	}

	if herr := ensurePayloadMatches(doc, mfst); herr != nil {
		return InitResult{}, herr
	}

	urls, herr := buildPresignedURLs(ctx, s.Storage, doc.Slug, mfst)
	if herr != nil {
		return InitResult{}, herr
	}

	return InitResult{
		Slug:          doc.Slug,
		PresignedURLs: urls,
		InlineAccept:  false,
	}, nil
}

// ensurePayloadMatches reports a 422 when an existing row was reused under the
// same idempotency_key but the incoming manifest hashes differently (ADR-0013).
// On a fresh insert the stored hash equals the incoming hash, so this passes.
func ensurePayloadMatches(doc *db.Document, mfst manifest.Manifest) *httpx.Error {
	incoming, err := db.ManifestHash(mfst)
	if err != nil {
		return httpx.Internal("manifest hashing failed")
	}
	if !bytes.Equal(incoming, doc.ManifestHash) {
		return httpx.Unprocessable(api.CodeIdempotencyPayloadMismatch,
			"idempotency_key reused with a different payload")
	}
	return nil
}

// Commit runs the commit phase of the publish protocol. Already-published rows
// are returned as-is (terminal-state idempotency).
func (s *Service) Commit(ctx context.Context, req api.CommitRequest) (CommitResult, *httpx.Error) {
	if herr := ValidateCommit(req); herr != nil {
		return CommitResult{}, herr
	}

	doc, err := s.Db.GetByIdempotencyKey(ctx, req.IdempotencyKey)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			return CommitResult{}, httpx.NotFound("no document for idempotency_key")
		}
		return CommitResult{}, httpx.Internal("failed to fetch document")
	}

	if doc.Status == db.StatusPublished {
		return CommitResult{
			URL:          documentURL(s.BaseURL, doc.Slug),
			Slug:         doc.Slug,
			ManifestHash: doc.ManifestHashHex(),
		}, nil
	}

	var mfst manifest.Manifest
	if err := json.Unmarshal(doc.ManifestJSON, &mfst); err != nil {
		return CommitResult{}, httpx.Internal("corrupt manifest")
	}
	if herr := verifyBlobs(ctx, s.Storage, doc.Slug, mfst); herr != nil {
		return CommitResult{}, herr
	}

	expiresAt := time.Now().Add(anonExpiresIn)
	published, err := s.commitPublish(ctx, req.IdempotencyKey, expiresAt)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			return CommitResult{}, httpx.NotFound("no pending document for idempotency_key")
		}
		return CommitResult{}, httpx.Internal("failed to publish document")
	}
	s.purgeSoon(ctx, published.Slug)

	return CommitResult{
		URL:          documentURL(s.BaseURL, published.Slug),
		Slug:         published.Slug,
		ManifestHash: published.ManifestHashHex(),
	}, nil
}

// commitPublish flips the pending row to 'published' and enqueues its CDN purge
// in one transaction, so a published document can never end up live with a stale
// edge cache and no pending purge (ADR-0031).
func (s *Service) commitPublish(ctx context.Context, idempotencyKey string, expiresAt time.Time) (*db.Document, error) {
	tx, err := s.Db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	published, err := tx.Publish(ctx, idempotencyKey, expiresAt)
	if err != nil {
		return nil, err
	}
	if err := tx.EnqueuePurge(ctx, published.Slug); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return published, nil
}
