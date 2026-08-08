// Package raw runs the /raw/ read workflow (CONTEXT.md "Raw Path", ADR-0016): it
// loads a published bundle's manifest, resolves one file key, and opens a stream
// over that file's untransformed R2 bytes. Unlike the view and LLM surfaces it
// neither renders nor rewrites — the bytes come back exactly as published, so a
// raw response hashes to the manifest's stored hash for that key. HTTP handlers
// in internal/server/handlers are thin glue around Service: the service returns
// metadata plus an open reader, and the handler sets headers before the body.
package raw

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"

	"github.com/Abir66/mdfly/internal/server/db"
	"github.com/Abir66/mdfly/internal/server/httpx"
	"github.com/Abir66/mdfly/internal/server/manifest"
	"github.com/Abir66/mdfly/internal/server/storage"
)

// rawContentType is the single content type S52 answers for every raw file. S53
// replaces this stub with the content-type table (ADR-0016).
const rawContentType = "text/plain; charset=utf-8"

// Service opens published files for the /raw/ surface.
type Service struct {
	Db      *db.Client
	Storage *storage.Client
}

// File is the metadata plus open body a raw response is written from. The caller
// owns Content and must Close it. ETag and Size come from the manifest, never a
// blob fetch; ContentType is the service's classification (a single value in
// S52).
type File struct {
	Content     io.ReadCloser
	ContentType string
	ETag        string // manifest content hash (hex)
	Size        int64
}

// Open resolves rawPath/up against slug's bundle and opens a stream over that
// file's raw bytes. rawPath is the project-root-relative key with its extension
// intact (empty for the bare /raw/<slug>, which addresses the Root); up is the
// optional ?up=N count reconstructing a key above the project root. It returns
// *httpx.Error carrying the status the handler maps to a plain-text response.
func (s *Service) Open(ctx context.Context, slug, rawPath, up string) (*File, *httpx.Error) {
	m, herr := s.loadManifest(ctx, slug)
	if herr != nil {
		return nil, herr
	}
	return s.open(ctx, slug, m, rawPath, up)
}

// open resolves the file key against m and opens its blob stream. Resolution is
// files-only: an exact file key streams, and a directory prefix, an unknown key,
// or a bare request against an empty Root all 404 — there is no third outcome.
// Split from Open so resolution is exercisable without a database. The 404 paths
// return before any blob fetch.
func (s *Service) open(ctx context.Context, slug string, m manifest.Manifest, rawPath, up string) (*File, *httpx.Error) {
	key, ok := rawKey(m, rawPath, up)
	if !ok {
		return nil, httpx.NotFound("not found")
	}
	f, ok := m.FilesByPath[key]
	if !ok {
		return nil, httpx.NotFound("not found")
	}
	rc, err := s.Storage.GetBlobStream(ctx, storage.BlobKey(slug, f.Hash, storage.ExtFromPath(key)))
	if err != nil {
		slog.Error("storage.GetBlobStream failed", "slug", slug, "key", key, "hash", f.Hash, "err", err)
		return nil, httpx.Internal("internal error")
	}
	return &File{Content: rc, ContentType: rawContentType, ETag: f.Hash, Size: f.Size}, nil
}

// rawKey resolves the addressed file key. An empty rawPath addresses the Root —
// 404 when the Root is empty (a folder-Document has no entry file). A non-empty
// rawPath reuses the view path's ?up=N reconstruction (manifest.NestedKey).
func rawKey(m manifest.Manifest, rawPath, up string) (string, bool) {
	if strings.Trim(rawPath, "/") == "" {
		if m.RootPath == "" {
			return "", false
		}
		return m.RootPath, true
	}
	return manifest.NestedKey(rawPath, up)
}

// loadManifest fetches slug's decoded manifest via the shared db loader, mapping
// its error vocabulary to the raw path's HTTP errors.
func (s *Service) loadManifest(ctx context.Context, slug string) (manifest.Manifest, *httpx.Error) {
	if slug == "" {
		return manifest.Manifest{}, httpx.NotFound("not found")
	}
	_, m, err := s.Db.GetManifestBySlug(ctx, slug)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			return manifest.Manifest{}, httpx.NotFound("not found")
		}
		if errors.Is(err, db.ErrGone) {
			return manifest.Manifest{}, httpx.Gone("gone")
		}
		slog.Error("load manifest failed", "slug", slug, "err", err)
		return manifest.Manifest{}, httpx.Internal("internal error")
	}
	return m, nil
}
