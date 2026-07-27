// Package document runs document-lifecycle operations outside the publish flow.
// Today that is Delete (CONTEXT.md "Delete"): an Edit-Token-authenticated soft
// delete that flips the row to 'deleted' so the slug stays reserved and its
// URL serves 410. HTTP handlers in internal/server/handlers are thin glue
// around Service.
package document

import (
	"context"
	"errors"

	"github.com/Abir66/mdfly/internal/api"
	"github.com/Abir66/mdfly/internal/server/db"
	"github.com/Abir66/mdfly/internal/server/httpx"
)

// Service executes document-lifecycle operations. Methods return *httpx.Error
// on failure so handlers can write the response without re-mapping.
type Service struct {
	Db *db.Client
}

// Delete soft-deletes slug after verifying the Edit Token. token is the
// plaintext bearer credential. Delete is unconditional (removal has no "lost
// update" to guard against) and idempotent — re-deleting an already-gone slug
// ('deleted' or 'expired') is a no-op success, while a row that was never
// public ('pending' or 'abandoned') is 404.
func (s *Service) Delete(ctx context.Context, slug, token string) *httpx.Error {
	doc, err := s.Db.GetBySlugAny(ctx, slug)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			return httpx.NotFound("not found")
		}
		return httpx.Internal("failed to load document")
	}
	if doc.Status == db.StatusPending || doc.Status == db.StatusAbandoned {
		return httpx.NotFound("not found")
	}
	if herr := verifyEditToken(doc, token); herr != nil {
		return herr
	}
	if doc.Status == db.StatusDeleted || doc.Status == db.StatusExpired {
		return nil
	}
	if _, err := s.Db.SoftDelete(ctx, slug); err != nil {
		if errors.Is(err, db.ErrNotFound) {
			return httpx.NotFound("not found")
		}
		return httpx.Internal("failed to delete document")
	}
	return nil
}

// verifyEditToken checks the plaintext token against the row's stored hash with
// a constant-time compare. A missing token is 401; a wrong one is 403.
func verifyEditToken(doc *db.Document, token string) *httpx.Error {
	if token == "" {
		return httpx.Unauthorized(api.CodeInvalidEditToken, "missing edit token")
	}
	if len(doc.EditTokenHash) == 0 {
		return httpx.Forbidden(api.CodeInvalidEditToken, "document is not editable with an edit token")
	}
	if !doc.MatchesEditToken(token) {
		return httpx.Forbidden(api.CodeInvalidEditToken, "edit token does not match")
	}
	return nil
}
