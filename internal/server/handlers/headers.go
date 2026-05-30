package handlers

import (
	"fmt"
	"net/http"
)

// Cache lifetimes (seconds) for slug responses. Per ADR-0018, slug HTML is a pure
// function of (slug, manifest_hash): a short browser max-age plus a long edge
// s-maxage lets Cloudflare absorb the read fan-out. A 404 caches briefly only —
// the slug might be created tomorrow.
const (
	slugMaxAgeSeconds  = 300
	slugSMaxAgeSeconds = 86400
	notFoundMaxAge     = 60
)

const (
	// robotsTagSlug marks every slug response unindexable (CONTEXT.md / ADR-0021).
	robotsTagSlug = "noindex, nofollow"
	// robotsTagNotFound omits nofollow — a 404 has no links to follow.
	robotsTagNotFound = "noindex"

	notFoundHTML = `<!doctype html><html><body><h1>404 — not found</h1></body></html>`
)

func slugCacheControl() string {
	return fmt.Sprintf("public, max-age=%d, s-maxage=%d", slugMaxAgeSeconds, slugSMaxAgeSeconds)
}

func notFoundCacheControl() string {
	return fmt.Sprintf("public, max-age=%d", notFoundMaxAge)
}

// writeNotFound writes the minimal 404 HTML page with its cache and robots
// headers. Used for both unknown slugs and unknown paths within a bundle.
func writeNotFound(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", notFoundCacheControl())
	h.Set("X-Robots-Tag", robotsTagNotFound)
	w.WriteHeader(http.StatusNotFound)
	fmt.Fprint(w, notFoundHTML)
}
