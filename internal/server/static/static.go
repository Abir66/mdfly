// Package static embeds the Viewer chrome's CSS and JS and serves them as
// content-hashed, immutable assets under the reserved /_static/ path (ADR-0010).
// The hash in each URL lets the CDN cache the bytes forever; a content change
// mints a new URL. ssr references the hashed URLs so the template never inlines
// the chrome (supersedes S19's inline <style>).
package static

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"net/http"
)

//go:embed assets/app.css assets/app.js
var files embed.FS

const (
	// CacheControl is sent with every /_static asset: forever-cacheable because
	// the content hash in the URL changes whenever the bytes do.
	CacheControl = "public, max-age=31536000, immutable"

	urlPrefix = "/_static/"
	hashLen   = 10
)

// asset is one embedded file resolved to its hashed URL, bytes, and MIME type.
type asset struct {
	url         string
	body        []byte
	contentType string
}

// Assets holds the built Viewer chrome assets and serves them.
type Assets struct {
	css   asset
	js    asset
	byURL map[string]asset
}

// New reads the embedded files, computes content-hashed URLs, and indexes them
// for serving. It panics on a missing embed (a build-time guarantee, never a
// runtime condition).
func New() *Assets {
	css := build("assets/app.css", "app", "css", "text/css; charset=utf-8")
	js := build("assets/app.js", "app", "js", "text/javascript; charset=utf-8")
	return &Assets{
		css:   css,
		js:    js,
		byURL: map[string]asset{css.url: css, js.url: js},
	}
}

// build loads an embedded file and derives its /_static/<name>.<hash>.<ext> URL.
func build(embedPath, name, ext, contentType string) asset {
	body, err := files.ReadFile(embedPath)
	if err != nil {
		panic("static: embedded asset missing: " + embedPath)
	}
	sum := sha256.Sum256(body)
	hash := hex.EncodeToString(sum[:])[:hashLen]
	return asset{
		url:         urlPrefix + name + "." + hash + "." + ext,
		body:        body,
		contentType: contentType,
	}
}

// CSSURL returns the content-hashed URL of the Viewer stylesheet.
func (a *Assets) CSSURL() string { return a.css.url }

// JSURL returns the content-hashed URL of the Viewer script.
func (a *Assets) JSURL() string { return a.js.url }

// Handler serves the embedded assets at their hashed URLs with an immutable
// cache header. Any other path is a 404 — a stale hash must not 200.
func (a *Assets) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		as, ok := a.byURL[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		h := w.Header()
		h.Set("Content-Type", as.contentType)
		h.Set("Cache-Control", CacheControl)
		w.Write(as.body)
	})
}
