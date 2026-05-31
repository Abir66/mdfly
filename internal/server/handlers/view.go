package handlers

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/Abir66/mdfly/internal/server/httpx"
	"github.com/Abir66/mdfly/internal/server/service/view"
	"github.com/Abir66/mdfly/internal/server/ssr"
)

// View handles GET /{slug}, rendering the bundle's root document.
func View(svc *view.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		html, herr := svc.RenderRoot(r.Context(), r.PathValue("slug"))
		writeViewResult(w, html, herr)
	}
}

// ViewPath handles GET /{slug}/{path...}, rendering a nested page. A trailing
// slash is 301-canonicalized to the slash-free form (ADR-0024) so every node has
// one URL.
func ViewPath(svc *view.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if redirectTrailingSlash(w, r) {
			return
		}
		html, herr := svc.RenderPath(r.Context(), r.PathValue("slug"), r.PathValue("path"), r.URL.Query().Get("up"))
		writeViewResult(w, html, herr)
	}
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
