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
	"github.com/Abir66/mdfly/internal/server/filetree"
	"github.com/Abir66/mdfly/internal/server/httpx"
	"github.com/Abir66/mdfly/internal/server/manifest"
	"github.com/Abir66/mdfly/internal/server/ssr"
	"github.com/Abir66/mdfly/internal/server/static"
	"github.com/Abir66/mdfly/internal/server/storage"
)

const markdownRenderTimeout = 2 * time.Second

// Service renders SSR view pages. RenderRoot and RenderPath return the page
// HTML, or *httpx.Error carrying the status the handler maps to a response.
type Service struct {
	Db      *db.Client
	Storage *storage.Client
	Static  *static.Assets
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

// RenderPath renders a nested page for GET /{slug}/{path...}. rawPath is the
// project-root-relative key with its extension intact (ADR-0010); up is the
// optional ?up=N count reconstructing a key above the project root.
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
		if errors.Is(err, db.ErrGone) {
			return manifest.Manifest{}, httpx.Gone("gone")
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

// render resolves key against the bundle manifest and dispatches on node type
// (ADR-0010). A markdown key renders to HTML; an image renders inline; another
// file renders a text preview or download card (S24); a directory prefix renders
// a Directory Listing; anything else is a 404.
func (s *Service) render(ctx context.Context, slug string, mfst manifest.Manifest, key string) (string, *httpx.Error) {
	res := filetree.Classify(manifestKeys(mfst), key)
	switch res.Kind {
	case filetree.File:
		switch {
		case isMarkdownKey(res.Key):
			return s.renderMarkdown(ctx, slug, mfst, res.Key)
		case isImageKey(res.Key):
			return s.renderImage(slug, mfst, res.Key)
		default:
			return s.renderTextPreview(ctx, slug, mfst, res.Key)
		}
	case filetree.Dir:
		return s.renderDirectory(ctx, slug, mfst, res.Key)
	default:
		return "", httpx.NotFound("not found")
	}
}

// PreviewMaxBytes is the largest text/code file rendered inline as highlighted
// source; larger files fall back to a download card without a blob fetch.
const PreviewMaxBytes = 1 << 20

// renderImage renders an image key as an inline <img> pointing at its CDN blob.
// No blob is fetched — the browser loads it directly from the CDN.
func (s *Service) renderImage(slug string, mfst manifest.Manifest, key string) (string, *httpx.Error) {
	return s.renderPage(slug, mfst, key, ssr.PageData{
		Title: path.Base(key),
		Image: &ssr.Asset{Name: path.Base(key), URL: s.blobURL(slug, mfst, key)},
	})
}

// renderTextPreview renders a non-markdown, non-image file. Guards run in order:
// a file larger than PreviewMaxBytes yields a download card with no fetch; else
// the blob is fetched and, if it sniffs binary (a lying extension), a download
// card; otherwise the source is highlighted inline.
func (s *Service) renderTextPreview(ctx context.Context, slug string, mfst manifest.Manifest, key string) (string, *httpx.Error) {
	f := mfst.FilesByPath[key]
	if f.Size > PreviewMaxBytes {
		return s.renderDownload(slug, mfst, key)
	}
	content, err := s.Storage.GetBlob(ctx, storage.BlobKey(slug, f.Hash, storage.ExtFromPath(key)))
	if err != nil {
		slog.Error("storage.GetBlob failed", "slug", slug, "key", key, "hash", f.Hash, "err", err)
		return "", httpx.Internal("internal error")
	}
	if markdown.IsBinary(content) {
		return s.renderDownload(slug, mfst, key)
	}
	highlighted, err := markdown.HighlightFile(path.Base(key), content)
	if err != nil {
		slog.Error("markdown highlight failed", "slug", slug, "key", key, "err", err)
		return "", httpx.Internal("render error")
	}
	return s.renderPage(slug, mfst, key, ssr.PageData{Title: path.Base(key), Body: highlighted, CodeFile: true})
}

// renderDownload renders a metadata card (name, size, CDN Download link) for a
// file that can't be previewed inline. Size comes from the manifest — no fetch.
func (s *Service) renderDownload(slug string, mfst manifest.Manifest, key string) (string, *httpx.Error) {
	f := mfst.FilesByPath[key]
	return s.renderPage(slug, mfst, key, ssr.PageData{
		Title:    path.Base(key),
		Download: &ssr.Asset{Name: path.Base(key), URL: s.blobURL(slug, mfst, key), Size: f.Size},
	})
}

// blobURL returns the public CDN URL for a file key's blob.
func (s *Service) blobURL(slug string, mfst manifest.Manifest, key string) string {
	f := mfst.FilesByPath[key]
	return s.Storage.BlobPublicURL(storage.BlobKey(slug, f.Hash, storage.ExtFromPath(key)))
}

// manifestKeys returns the bundle's manifest keys as a slice for filetree.
func manifestKeys(mfst manifest.Manifest) []string {
	keys := make([]string, 0, len(mfst.FilesByPath))
	for k := range mfst.FilesByPath {
		keys = append(keys, k)
	}
	return keys
}

// renderMarkdown fetches the markdown blob at key, rewrites in-bundle references,
// and renders the full HTML page with the document's chrome.
func (s *Service) renderMarkdown(ctx context.Context, slug string, mfst manifest.Manifest, key string) (string, *httpx.Error) {
	rendered, meta, resolve, herr := s.renderBlob(ctx, slug, mfst, key)
	if herr != nil {
		return "", herr
	}
	return s.renderPage(slug, mfst, key, ssr.PageData{
		Title:         meta.Title,
		Excerpt:       meta.Excerpt,
		OGImageURL:    resolveOGImage(meta.OGImagePath, resolve),
		Body:          rendered,
		EnrichMermaid: meta.HasMermaid,
		EnrichMath:    meta.HasMath,
	})
}

// renderDirectory renders a Directory Listing for prefix: its immediate folders
// (first) and files (with sizes), each clickable. When the directory carries an
// index document (README.md/index.md) it is rendered below the listing by
// reusing the markdown render path.
func (s *Service) renderDirectory(ctx context.Context, slug string, mfst manifest.Manifest, prefix string) (string, *httpx.Error) {
	data := ssr.PageData{Listing: filetree.ListDir(sizesByKey(mfst), prefix)}
	if idxKey, ok := filetree.IndexFile(manifestKeys(mfst), prefix); ok {
		rendered, meta, resolve, herr := s.renderBlob(ctx, slug, mfst, idxKey)
		if herr != nil {
			return "", herr
		}
		data.Body = rendered
		data.Title = meta.Title
		data.Excerpt = meta.Excerpt
		data.OGImageURL = resolveOGImage(meta.OGImagePath, resolve)
		data.EnrichMermaid = meta.HasMermaid
		data.EnrichMath = meta.HasMath
	}
	return s.renderPage(slug, mfst, prefix, data)
}

// renderBlob fetches the markdown blob at key and renders it to HTML, returning
// the body, its metadata, and the resolver used (for OG-image resolution).
func (s *Service) renderBlob(ctx context.Context, slug string, mfst manifest.Manifest, key string) (template.HTML, markdown.Meta, markdown.RefResolver, *httpx.Error) {
	f := mfst.FilesByPath[key]
	content, err := s.Storage.GetBlob(ctx, storage.BlobKey(slug, f.Hash, storage.ExtFromPath(key)))
	if err != nil {
		slog.Error("storage.GetBlob failed", "slug", slug, "key", key, "hash", f.Hash, "err", err)
		return "", markdown.Meta{}, nil, httpx.Internal("internal error")
	}
	resolve := pageResolver(s.Storage, slug, mfst, path.Dir(key))
	rendered, meta, err := markdown.RenderRefsWithTimeout(content, resolve, markdownRenderTimeout)
	if err != nil {
		slog.Error("markdown render failed", "slug", slug, "key", key, "err", err)
		return "", markdown.Meta{}, nil, httpx.Internal("render error")
	}
	return template.HTML(rendered), meta, resolve, nil
}

// renderPage fills in the chrome fields (slug, tree, breadcrumb, static URLs) for
// key and executes the page template.
func (s *Service) renderPage(slug string, mfst manifest.Manifest, key string, data ssr.PageData) (string, *httpx.Error) {
	data.Slug = slug
	data.Sidebar = len(mfst.FilesByPath) > 1
	data.Tree = filetree.BuildTree(manifestKeys(mfst), key)
	data.Breadcrumb = filetree.Breadcrumb(key)
	data.CSSURL = s.Static.CSSURL()
	data.JSURL = s.Static.JSURL()
	pageHTML, err := ssr.RenderPage(data)
	if err != nil {
		slog.Error("ssr.RenderPage failed", "slug", slug, "key", key, "err", err)
		return "", httpx.Internal("render error")
	}
	return pageHTML, nil
}

// sizesByKey maps every manifest key to its byte size for filetree.ListDir.
func sizesByKey(mfst manifest.Manifest) map[string]int64 {
	sizes := make(map[string]int64, len(mfst.FilesByPath))
	for k, f := range mfst.FilesByPath {
		sizes[k] = f.Size
	}
	return sizes
}
