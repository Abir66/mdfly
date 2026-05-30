// Package view runs the SSR view workflow (CONTEXT.md "View"): it loads a
// published bundle's manifest, fetches the requested document blob, rewrites
// in-bundle references to their final URLs, and renders the full HTML page via
// the ssr package. HTTP handlers in internal/server/handlers are thin glue
// around Service; ssr stays a pure template renderer.
package view

import (
	"context"
	"encoding/json"
	"errors"
	"html/template"
	"log/slog"
	"path"
	"time"

	"github.com/Abir66/mdfly/internal/markdown"
	"github.com/Abir66/mdfly/internal/server/db"
	"github.com/Abir66/mdfly/internal/server/httpx"
	"github.com/Abir66/mdfly/internal/server/manifest"
	"github.com/Abir66/mdfly/internal/server/ssr"
	"github.com/Abir66/mdfly/internal/server/storage"
)

const markdownRenderTimeout = 2 * time.Second

// Service renders SSR view pages. RenderRoot and RenderPath return the page
// HTML, or *httpx.Error carrying the status the handler maps to a response.
type Service struct {
	Db      *db.Client
	Storage *storage.Client
}

// RenderRoot renders the bundle's root document for GET /{slug}.
func (s *Service) RenderRoot(ctx context.Context, slug string) (string, *httpx.Error) {
	if slug == "" {
		return "", httpx.NotFound("not found")
	}
	mfst, herr := s.loadManifest(ctx, slug)
	if herr != nil {
		return "", herr
	}
	return s.render(ctx, slug, mfst, mfst.RootPath)
}

// RenderPath renders a nested .md page for GET /{slug}/{path...}. rawPath is the
// project-root-relative key with the .md extension stripped; up is the optional
// ?up=N count reconstructing a key above the project root.
func (s *Service) RenderPath(ctx context.Context, slug, rawPath, up string) (string, *httpx.Error) {
	if slug == "" {
		return "", httpx.NotFound("not found")
	}
	key, ok := nestedKey(rawPath, up)
	if !ok {
		return "", httpx.NotFound("not found")
	}
	mfst, herr := s.loadManifest(ctx, slug)
	if herr != nil {
		return "", herr
	}
	return s.render(ctx, slug, mfst, key)
}

// loadManifest fetches and decodes the document's manifest for slug.
func (s *Service) loadManifest(ctx context.Context, slug string) (manifest.Manifest, *httpx.Error) {
	doc, err := s.Db.GetBySlug(ctx, slug)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			return manifest.Manifest{}, httpx.NotFound("not found")
		}
		slog.Error("db.GetBySlug failed", "slug", slug, "err", err)
		return manifest.Manifest{}, httpx.Internal("internal error")
	}
	var mfst manifest.Manifest
	if err := json.Unmarshal(doc.ManifestJSON, &mfst); err != nil {
		slog.Error("manifest unmarshal failed", "slug", slug, "err", err)
		return manifest.Manifest{}, httpx.Internal("internal error")
	}
	return mfst, nil
}

// render renders the file at key within the bundle and returns the page HTML.
// An unknown key is a 404.
func (s *Service) render(ctx context.Context, slug string, mfst manifest.Manifest, key string) (string, *httpx.Error) {
	f, ok := mfst.FilesByPath[key]
	if !ok {
		return "", httpx.NotFound("not found")
	}

	content, err := s.Storage.GetBlob(ctx, storage.BlobKey(slug, f.Hash, storage.ExtFromPath(key)))
	if err != nil {
		slog.Error("storage.GetBlob failed", "slug", slug, "key", key, "hash", f.Hash, "err", err)
		return "", httpx.Internal("internal error")
	}

	resolve := pageResolver(s.Storage, slug, mfst, path.Dir(key))
	rendered, meta, err := markdown.RenderRefsWithTimeout(content, resolve, markdownRenderTimeout)
	if err != nil {
		slog.Error("markdown render failed", "slug", slug, "key", key, "err", err)
		return "", httpx.Internal("render error")
	}

	pageHTML, err := ssr.RenderPage(ssr.PageData{
		Title:         meta.Title,
		Excerpt:       meta.Excerpt,
		OGImageURL:    resolveOGImage(meta.OGImagePath, resolve),
		Body:          template.HTML(rendered),
		EnrichMermaid: meta.HasMermaid,
		EnrichMath:    meta.HasMath,
	})
	if err != nil {
		slog.Error("ssr.RenderPage failed", "slug", slug, "key", key, "err", err)
		return "", httpx.Internal("render error")
	}
	return pageHTML, nil
}
