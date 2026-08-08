package handlers

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/Abir66/mdfly/internal/server/httpx"
	"github.com/Abir66/mdfly/internal/server/service/view"
	"github.com/Abir66/mdfly/internal/server/ssr"
)

// canonicalQueryParam is the only query parameter the read paths honor; it
// addresses above-root manifest keys (browsers strip dot-segments from the path
// before the server sees them). Everything else is dropped by canonicalization.
const canonicalQueryParam = "up"

// View handles GET /{slug}, rendering the bundle's root document.
func View(svc *view.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if redirectCanonicalQuery(w, r) {
			return
		}
		html, herr := svc.RenderRoot(r.Context(), r.PathValue("slug"))
		writeViewResult(w, html, herr)
	}
}

// ViewPath handles GET /{slug}/{path...}, rendering a nested page. A trailing
// slash is 301-canonicalized to the slash-free form (ADR-0010) so every node has
// one URL.
func ViewPath(svc *view.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if redirectTrailingSlash(w, r) || redirectCanonicalQuery(w, r) {
			return
		}
		html, herr := svc.RenderPath(r.Context(), r.PathValue("slug"), r.PathValue("path"), r.URL.Query().Get(canonicalQueryParam))
		writeViewResult(w, html, herr)
	}
}

// redirectCanonicalQuery 302-redirects to the same path carrying only the "up"
// parameter when the request query holds anything else, and reports whether it
// did. It runs before any Postgres or object-storage access, so a junk-query
// request costs a query scan and a header write, not a render or a large stream
// (S54). 302 (not 301) keeps the stripping revocable if a real query parameter
// is ever introduced; the Cache-Control still lets the edge absorb repeats — the
// common case is a shared link carrying tracking parameters.
func redirectCanonicalQuery(w http.ResponseWriter, r *http.Request) bool {
	canonical, changed := canonicalizeQuery(r.URL.RawQuery)
	if !changed {
		return false
	}
	target := r.URL.EscapedPath()
	if canonical != "" {
		target += "?" + canonical
	}
	w.Header().Set("Cache-Control", slugCacheControl())
	http.Redirect(w, r, target, http.StatusFound)
	return true
}

// canonicalizeQuery returns the canonical query string retaining only the first
// "up" value, and whether raw differed from it. The comparison is against the
// raw string, not a re-encoded parse, so an alternate spelling of the same query
// ("%75p=1", "up=a%20b") is non-canonical and redirects — one URL per node means
// one cache key. A parse error yields whatever pairs survived, which is never
// byte-equal to raw, so malformed queries redirect too.
func canonicalizeQuery(raw string) (string, bool) {
	q, _ := url.ParseQuery(raw)
	canonical := url.Values{}
	if q.Has(canonicalQueryParam) {
		canonical.Set(canonicalQueryParam, q.Get(canonicalQueryParam))
	}
	encoded := canonical.Encode()
	return encoded, encoded != raw
}

// redirectTrailingSlash issues a 301 to the trailing-slash-free path (query
// preserved) when the request URL ends in "/", and reports whether it did.
func redirectTrailingSlash(w http.ResponseWriter, r *http.Request) bool {
	p := r.URL.EscapedPath()
	if len(p) <= 1 || !strings.HasSuffix(p, "/") {
		return false
	}
	target := strings.TrimRight(p, "/")
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	http.Redirect(w, r, target, http.StatusMovedPermanently)
	return true
}

// writeViewResult writes the rendered page, or maps the error to an HTTP
// response. View routes serve a human HTML viewer, so errors stay HTML/plain
// (not the JSON envelope) — the single seam for future SSR'd error pages.
func writeViewResult(w http.ResponseWriter, html string, herr *httpx.Error) {
	if herr != nil {
		writeViewError(w, herr)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Security-Policy", ssr.ContentSecurityPolicy)
	h.Set("Cache-Control", slugCacheControl())
	h.Set("Vary", "Accept")
	h.Set("X-Robots-Tag", robotsTagSlug)
	fmt.Fprint(w, html)
}

// writeViewError maps an httpx.Error to a view-path response: a 404 renders the
// minimal HTML page (CONTEXT.md "Document"), anything else stays plain text but
// still carries the noindex marker every slug response gets.
func writeViewError(w http.ResponseWriter, herr *httpx.Error) {
	if herr.Status == http.StatusNotFound {
		writeNotFound(w)
		return
	}
	w.Header().Set("X-Robots-Tag", robotsTagSlug)
	http.Error(w, herr.Msg, herr.Status)
}
