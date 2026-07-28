package publish

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/Abir66/mdfly/internal/api"
	"github.com/Abir66/mdfly/internal/server/db"
	"github.com/Abir66/mdfly/internal/server/httpx"
	"github.com/Abir66/mdfly/internal/server/manifest"
	"github.com/Abir66/mdfly/internal/server/storage"
)

// UpdateInitResult is the data the UpdateInit handler serializes to the wire.
type UpdateInitResult struct {
	PresignedURLs map[string]string
}

// disposition classifies an incoming update against the live row's manifest
// hash, per ADR-0027's three-way idempotency compare.
type disposition int

const (
	// dispApplied means current == new: the update already landed (retry-safe).
	dispApplied disposition = iota
	// dispApply means --force or current == parent: apply the overwrite.
	dispApply
	// dispConflict means a stale parent lost the optimistic check.
	dispConflict
)

// classifyUpdate compares the live row's current hash against the parent the
// client sent and the new hash it wants to land. An empty parent is --force.
func classifyUpdate(current, parent, next string) disposition {
	if current == next {
		return dispApplied
	}
	if parent == "" || current == parent {
		return dispApply
	}
	return dispConflict
}

// UpdateInit runs the init phase of the update wire (ADR-0027): verify the Edit
// Token, run the optimistic pre-check, diff the incoming manifest against the
// stored one by (hash, ext), and presign PUTs only for new blobs. It writes
// nothing to Postgres. token is the plaintext Edit Token from the header.
func (s *Service) UpdateInit(ctx context.Context, req api.UpdateInitRequest, token string) (UpdateInitResult, *httpx.Error) {
	incoming, herr := manifestFromBundle(req.Bundle)
	if herr != nil {
		return UpdateInitResult{}, herr
	}
	doc, stored, herr := s.loadEditable(ctx, req.TargetSlug, token)
	if herr != nil {
		return UpdateInitResult{}, herr
	}
	nextHash, herr := manifestHashHex(incoming)
	if herr != nil {
		return UpdateInitResult{}, herr
	}
	if classifyUpdate(doc.ManifestHashHex(), req.ParentManifestHash, nextHash) == dispConflict {
		return UpdateInitResult{}, conflictErr()
	}

	urls, herr := presignNewBlobs(ctx, s.Storage, doc.Slug, incoming, stored)
	if herr != nil {
		return UpdateInitResult{}, herr
	}
	return UpdateInitResult{PresignedURLs: urls}, nil
}

// UpdateCommit runs the commit phase: HEAD-verify blobs, then one atomic
// optimistic UPDATE on the live row. A retry after a successful commit
// (current == new hash) returns success without re-writing.
func (s *Service) UpdateCommit(ctx context.Context, req api.UpdateCommitRequest, token string) (CommitResult, *httpx.Error) {
	incoming, herr := manifestFromBundle(req.Bundle)
	if herr != nil {
		return CommitResult{}, herr
	}
	doc, _, herr := s.loadEditable(ctx, req.TargetSlug, token)
	if herr != nil {
		return CommitResult{}, herr
	}
	nextHash, herr := manifestHashHex(incoming)
	if herr != nil {
		return CommitResult{}, herr
	}

	switch classifyUpdate(doc.ManifestHashHex(), req.ParentManifestHash, nextHash) {
	case dispApplied:
		return s.commitResult(doc.Slug, doc.ManifestHashHex()), nil
	case dispConflict:
		return CommitResult{}, conflictErr()
	}

	if herr := verifyBlobs(ctx, s.Storage, doc.Slug, incoming); herr != nil {
		return CommitResult{}, herr
	}
	updated, herr := s.applyUpdate(ctx, doc.Slug, incoming, req.ParentManifestHash)
	if herr != nil {
		return CommitResult{}, herr
	}
	s.purgeSoon(ctx, updated.Slug)
	return s.commitResult(updated.Slug, updated.ManifestHashHex()), nil
}

// applyUpdate performs the guarded atomic overwrite and enqueues the slug's CDN
// purge in the same transaction (ADR-0031). A no-rows result means a concurrent
// writer moved manifest_hash off the parent between our read and the UPDATE,
// which is a lost optimistic-concurrency check → 409.
func (s *Service) applyUpdate(ctx context.Context, slug string, mfst manifest.Manifest, parentHex string) (*db.Document, *httpx.Error) {
	var parent []byte
	if parentHex != "" {
		p, err := hex.DecodeString(parentHex)
		if err != nil {
			return nil, httpx.BadRequest("parent_manifest_hash is not valid hex")
		}
		parent = p
	}
	updated, err := s.commitUpdate(ctx, db.UpdateManifestParams{
		Slug:               slug,
		Manifest:           mfst,
		ParentManifestHash: parent,
		ExpiresAt:          time.Now().Add(anonExpiresIn),
	})
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			return s.reconcileConcurrentUpdate(ctx, slug, mfst)
		}
		return nil, httpx.Internal("failed to update document")
	}
	return updated, nil
}

// commitUpdate overwrites the live row and enqueues its purge atomically.
func (s *Service) commitUpdate(ctx context.Context, p db.UpdateManifestParams) (*db.Document, error) {
	tx, err := s.Db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	updated, err := tx.UpdateManifest(ctx, p)
	if err != nil {
		return nil, err
	}
	if err := tx.EnqueuePurge(ctx, updated.Slug); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return updated, nil
}

// reconcileConcurrentUpdate handles a lost guarded UPDATE: a concurrent writer
// moved manifest_hash off the parent between our read and the write. If that
// writer landed the identical manifest we wanted, the desired state is already
// live, so re-read and return it as success; any other live hash is a genuine
// 409 conflict.
func (s *Service) reconcileConcurrentUpdate(ctx context.Context, slug string, mfst manifest.Manifest) (*db.Document, *httpx.Error) {
	nextHash, herr := manifestHashHex(mfst)
	if herr != nil {
		return nil, herr
	}
	doc, err := s.Db.GetBySlug(ctx, slug)
	if err != nil {
		return nil, conflictErr()
	}
	if doc.ManifestHashHex() == nextHash {
		return doc, nil
	}
	return nil, conflictErr()
}

// loadEditable loads the published row for slug, authenticates the Edit Token,
// and decodes its stored manifest. A gone/expired slug is 410; a missing one is
// 404 (both self-heal the CLI's local record).
func (s *Service) loadEditable(ctx context.Context, slug, token string) (*db.Document, manifest.Manifest, *httpx.Error) {
	doc, err := s.Db.GetBySlug(ctx, slug)
	if err != nil {
		switch {
		case errors.Is(err, db.ErrGone):
			return nil, manifest.Manifest{}, httpx.Gone("document has been deleted")
		case errors.Is(err, db.ErrNotFound):
			return nil, manifest.Manifest{}, httpx.NotFound("not found")
		default:
			return nil, manifest.Manifest{}, httpx.Internal("failed to load document")
		}
	}
	if herr := verifyEditToken(doc, token); herr != nil {
		return nil, manifest.Manifest{}, herr
	}
	var stored manifest.Manifest
	if err := json.Unmarshal(doc.ManifestJSON, &stored); err != nil {
		return nil, manifest.Manifest{}, httpx.Internal("corrupt manifest")
	}
	return doc, stored, nil
}

func (s *Service) commitResult(slug, manifestHash string) CommitResult {
	return CommitResult{
		URL:          documentURL(s.BaseURL, slug),
		Slug:         slug,
		ManifestHash: manifestHash,
	}
}

// presignNewBlobs presigns one PUT per incoming (hash, ext) that is absent from
// the stored manifest. A blob whose (hash, ext) already exists is content-
// addressed under the same live key, so it needs no re-upload (ADR-0027).
func presignNewBlobs(ctx context.Context, r2 *storage.Client, sl string, incoming, stored manifest.Manifest) (map[string]string, *httpx.Error) {
	storedSet := blobIdentities(stored)
	urls := make(map[string]string)
	seen := make(map[string]struct{})
	for path, f := range incoming.FilesByPath {
		ext := storage.ExtFromPath(path)
		if _, ok := storedSet[blobIdentity{f.Hash, ext}]; ok {
			continue
		}
		key := storage.BlobKey(sl, f.Hash, ext)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		url, err := r2.PresignPUT(ctx, key, f.Size, presignTTL)
		if err != nil {
			return nil, httpx.Internal("failed to generate presigned URLs")
		}
		urls[path] = url
	}
	return urls, nil
}

// blobIdentity is the (content hash, extension) pair that names a stored blob's
// R2 key, matching the render-time blobkey derivation.
type blobIdentity struct {
	hash string
	ext  string
}

func blobIdentities(mfst manifest.Manifest) map[blobIdentity]struct{} {
	set := make(map[blobIdentity]struct{}, len(mfst.FilesByPath))
	for path, f := range mfst.FilesByPath {
		set[blobIdentity{f.Hash, storage.ExtFromPath(path)}] = struct{}{}
	}
	return set
}

func manifestFromBundle(dto api.BundleDTO) (manifest.Manifest, *httpx.Error) {
	mfst, err := manifest.FromDTO(dto)
	if err != nil {
		return manifest.Manifest{}, httpx.BadRequest("bundle.root_path not present in bundle.files")
	}
	return mfst, nil
}

func manifestHashHex(mfst manifest.Manifest) (string, *httpx.Error) {
	h, err := db.ManifestHash(mfst)
	if err != nil {
		return "", httpx.Internal("manifest hashing failed")
	}
	return hex.EncodeToString(h), nil
}

func conflictErr() *httpx.Error {
	return httpx.Conflict(api.CodeUpdateConflict,
		"document changed elsewhere; re-run to pick up the change, or pass --force to overwrite")
}

// verifyEditToken checks the plaintext token against the row's stored hash. A
// missing token is 401; a wrong one (or a row with no token) is 403. Mirrors the
// document service's mapper; the constant-time compare lives in db.Document.
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
