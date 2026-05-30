package handlers

import (
	"fmt"
	"net/http"

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

// ViewPath handles GET /{slug}/{path...}, rendering a nested .md page.
func ViewPath(svc *view.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		html, herr := svc.RenderPath(r.Context(), r.PathValue("slug"), r.PathValue("path"), r.URL.Query().Get("up"))
		writeViewResult(w, html, herr)
	}
}

// writeViewResult writes the rendered page, or maps the error to a plain HTTP
// response. View routes serve a human HTML viewer, so errors stay plain text
// (not the JSON envelope) — the single seam for future SSR'd error pages.
func writeViewResult(w http.ResponseWriter, html string, herr *httpx.Error) {
	if herr != nil {
		http.Error(w, herr.Msg, herr.Status)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", ssr.ContentSecurityPolicy)
	fmt.Fprint(w, html)
}
