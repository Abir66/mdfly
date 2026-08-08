// Package raw runs the /raw/ read workflow (CONTEXT.md "Raw Path", ADR-0016): it
// loads a published bundle's manifest, resolves one file key, and opens a stream
// over that file's untransformed R2 bytes. Unlike the view and LLM surfaces it
// neither renders nor rewrites — the bytes come back exactly as published, so a
// raw response hashes to the manifest's stored hash for that key. HTTP handlers
// in internal/server/handlers are thin glue around Service: the service returns
// metadata plus an open reader, and the handler sets headers before the body.
package raw

import (
	"bufio"
	"context"
	"errors"
	"io"
	"log/slog"
	"mime"
	"path"
	"strings"

	"github.com/Abir66/mdfly/internal/markdown"
	"github.com/Abir66/mdfly/internal/server/db"
	"github.com/Abir66/mdfly/internal/server/httpx"
	"github.com/Abir66/mdfly/internal/server/manifest"
	"github.com/Abir66/mdfly/internal/server/storage"
)

const (
	// peekBytes is how many leading bytes the binary sniff inspects. It matches the
	// view path's whole-file check applied to a stream head — 512 is enough to see
	// a NUL or invalid UTF-8 without buffering the body.
	peekBytes = 512

	octetType = "application/octet-stream"
	pdfType   = "application/pdf"
	textType  = "text/plain; charset=utf-8"
	pdfExt    = ".pdf"
)

// inlineTypes maps a file extension to the real content type it is served as,
// inline. Everything here is passive when rendered at a top-level URL: images,
// audio, and video never execute embedded script. SVG is deliberately absent —
// it is an active document that runs script as image/svg+xml, so it falls
// through to text/plain (see classify). This map must NOT be merged with the
// view resolver's imageExts, which lists .svg because <img> never runs script.
var inlineTypes = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
	".mp4":  "video/mp4",
	".webm": "video/webm",
	".mp3":  "audio/mpeg",
	".wav":  "audio/wav",
}

// classify decides one raw file's content type and disposition from its key and
// peeked leading bytes. It is pure — no I/O, no HTTP. The four branches run in
// this order, and the order is load-bearing:
//
//  1. an extension on the inline whitelist → its real type, inline;
//  2. .pdf → application/pdf, attachment (its viewer carries a script runtime);
//  3. bytes that sniff binary → application/octet-stream, attachment;
//  4. everything else → text/plain, inline.
//
// The whitelist runs before the sniff on purpose: PNG bytes sniff binary, so
// reversing the two would turn every image into a download.
func classify(key string, peek []byte) (contentType string, attachment bool) {
	ext := storage.ExtFromPath(key)
	if ct, ok := inlineTypes[ext]; ok {
		return ct, false
	}
	if ext == pdfExt {
		return pdfType, true
	}
	if markdown.IsBinary(peek) {
		return octetType, true
	}
	return textType, false
}

// Service opens published files for the /raw/ surface.
type Service struct {
	Db      *db.Client
	Storage *storage.Client
}

// File is the metadata plus open body a raw response is written from. The caller
// owns Content and must Close it. ETag and Size come from the manifest, never a
// blob fetch; ContentType and Disposition come from classify. Disposition is the
// Content-Disposition header value, empty when the response is served inline.
type File struct {
	Content     io.ReadCloser
	ContentType string
	Disposition string
	ETag        string // manifest content hash (hex)
	Size        int64
}

// bufferedBody adapts a buffered read over the blob stream into a ReadCloser:
// Read serves the peeked bytes first (peeking does not consume them) and Close
// closes the underlying stream.
type bufferedBody struct {
	*bufio.Reader
	closer io.Closer
}

func (b *bufferedBody) Close() error { return b.closer.Close() }

// attachmentDisposition builds an RFC 6266 Content-Disposition value naming the
// file's basename. mime.FormatMediaType escapes a quoted filename and falls back
// to the filename*=utf-8” form for non-ASCII or control bytes, so a publisher-
// controlled name can never break the header. It returns "attachment" alone if
// FormatMediaType rejects the value.
func attachmentDisposition(key string) string {
	if d := mime.FormatMediaType("attachment", map[string]string{"filename": path.Base(key)}); d != "" {
		return d
	}
	return "attachment"
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

	br := bufio.NewReader(rc)
	peek, _ := br.Peek(peekBytes) // short files yield io.EOF; the bytes are still valid
	contentType, attachment := classify(key, peek)
	disposition := ""
	if attachment {
		disposition = attachmentDisposition(key)
	}
	return &File{
		Content:     &bufferedBody{Reader: br, closer: rc},
		ContentType: contentType,
		Disposition: disposition,
		ETag:        f.Hash,
		Size:        f.Size,
	}, nil
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
