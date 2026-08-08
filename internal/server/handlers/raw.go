package handlers

import (
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/Abir66/mdfly/internal/server/httpx"
	"github.com/Abir66/mdfly/internal/server/service/raw"
)

const (
	// rawCSP parks a raw response in a unique opaque origin. This is what makes
	// serving stranger-uploaded bytes from the apex acceptable; not cosmetic — see
	// ADR-0016's trade-off before changing it.
	rawCSP = "sandbox"
	// contentTypeOptionsNoSniff stops a browser from MIME-sniffing raw bytes into
	// an executable type.
	contentTypeOptionsNoSniff = "nosniff"
)

// Raw handles GET /raw/{slug}, streaming the bundle Root file's raw bytes.
func Raw(svc *raw.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f, herr := svc.Open(r.Context(), r.PathValue("slug"), "", "")
		writeRawResult(w, r, f, herr)
	}
}

// RawPath handles GET /raw/{slug}/{path...}. A trailing slash is 301-canonicalized
// to the slash-free form (ADR-0010) so every node has one URL.
func RawPath(svc *raw.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if redirectTrailingSlash(w, r) {
			return
		}
		f, herr := svc.Open(r.Context(), r.PathValue("slug"), r.PathValue("path"), r.URL.Query().Get("up"))
		writeRawResult(w, r, f, herr)
	}
}

// writeRawResult streams the file's bytes with the raw header set, or maps the
// error to a plain-text response. Raw never emits HTML on any route, and never
// negotiates, so there is no Vary: Accept. The ETag and Content-Length come from
// the manifest — set before the body — so serving them costs no blob fetch. A
// conditional request matching the ETag short-circuits to 304 with no body.
func writeRawResult(w http.ResponseWriter, r *http.Request, f *raw.File, herr *httpx.Error) {
	if herr != nil {
		http.Error(w, herr.Msg, herr.Status)
		return
	}
	defer f.Content.Close()

	etag := `"` + f.ETag + `"`
	h := w.Header()
	h.Set("Content-Type", f.ContentType)
	h.Set("Cache-Control", slugCacheControl())
	h.Set("X-Robots-Tag", robotsTagSlug)
	h.Set("X-Content-Type-Options", contentTypeOptionsNoSniff)
	h.Set("Content-Security-Policy", rawCSP)
	h.Set("ETag", etag)

	if ifNoneMatch(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	h.Set("Content-Length", strconv.FormatInt(f.Size, 10))
	io.Copy(w, f.Content) //nolint:errcheck
}

// ifNoneMatch reports whether an If-None-Match header matches etag, honoring the
// "*" wildcard, a comma-separated list, and the weak-validator "W/" prefix.
func ifNoneMatch(header, etag string) bool {
	if header == "" {
		return false
	}
	if strings.TrimSpace(header) == "*" {
		return true
	}
	for _, tok := range strings.Split(header, ",") {
		if strings.TrimPrefix(strings.TrimSpace(tok), "W/") == etag {
			return true
		}
	}
	return false
}
