package handlers_test

import (
	"net/http"
	"strconv"
	"testing"
)

// getRaw GETs a /raw/ URL with optional request headers and returns the response
// (body still open) so a test can assert headers and bytes.
func getRaw(t *testing.T, url string, headers map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("build GET %s: %v", url, err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	return resp
}

func TestRaw_bytesIdenticalAndHeaders(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}

	dsn := startPostgres(t)
	env := startMinio(t)
	srv := newTestServer(t, dsn, env, "https://mdfly.dev")

	const rootPath = "notes.md"
	content := []byte("# Notes\n\n<script>not sanitized here</script>\n\nRaw bytes: \x00\x01\xff done.\n")
	files := map[string][]byte{rootPath: content}
	slug := publishFiles(t, srv, "aaaaaaaa-bbbb-cccc-dddd-ffffffffff01", "/proj", rootPath, files)

	resp := getRaw(t, srv.URL+"/raw/"+slug, nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200", resp.StatusCode)
	}

	body := readAll(t, resp.Body)
	if !bytesEqual(body, content) {
		t.Errorf("raw bytes differ from published content:\n got %q\nwant %q", body, content)
	}
	// The bytes hash to the manifest's stored hash for the key (the ETag).
	if got, want := contentHash(body), contentHash(content); got != want {
		t.Errorf("body hash=%s, want %s", got, want)
	}

	wantHeaders := map[string]string{
		"Content-Type":            "text/plain; charset=utf-8",
		"Cache-Control":           "public, max-age=300, s-maxage=86400",
		"X-Robots-Tag":            "noindex, nofollow",
		"X-Content-Type-Options":  "nosniff",
		"Content-Security-Policy": "sandbox",
		"ETag":                    `"` + contentHash(content) + `"`,
		"Content-Length":          itoa(len(content)),
	}
	for k, want := range wantHeaders {
		if got := resp.Header.Get(k); got != want {
			t.Errorf("header %s=%q, want %q", k, got, want)
		}
	}
	// Raw does not negotiate, so it carries no Vary: Accept.
	if v := resp.Header.Get("Vary"); v != "" {
		t.Errorf("Vary=%q, want empty (raw does not negotiate)", v)
	}
}

func TestRaw_nestedKeyBytes(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}

	dsn := startPostgres(t)
	env := startMinio(t)
	srv := newTestServer(t, dsn, env, "https://mdfly.dev")

	nested := []byte("package main\n\nfunc main() {}\n")
	files := map[string][]byte{
		"index.md":   []byte("# Index\n"),
		"src/app.go": nested,
	}
	slug := publishFiles(t, srv, "aaaaaaaa-bbbb-cccc-dddd-ffffffffff02", "/proj", "index.md", files)

	resp := getRaw(t, srv.URL+"/raw/"+slug+"/src/app.go", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200", resp.StatusCode)
	}
	if body := readAll(t, resp.Body); !bytesEqual(body, nested) {
		t.Errorf("nested raw bytes differ:\n got %q\nwant %q", body, nested)
	}
}

func TestRaw_conditionalRequestReturns304(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}

	dsn := startPostgres(t)
	env := startMinio(t)
	srv := newTestServer(t, dsn, env, "https://mdfly.dev")

	const rootPath = "cond.md"
	content := []byte("# Conditional\n")
	slug := publishFiles(t, srv, "aaaaaaaa-bbbb-cccc-dddd-ffffffffff03", "/proj", rootPath, map[string][]byte{rootPath: content})

	first := getRaw(t, srv.URL+"/raw/"+slug, nil)
	etag := first.Header.Get("ETag")
	first.Body.Close()
	if etag == "" {
		t.Fatal("first response carried no ETag")
	}

	resp := getRaw(t, srv.URL+"/raw/"+slug, map[string]string{"If-None-Match": etag})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotModified {
		t.Fatalf("conditional status=%d, want 304", resp.StatusCode)
	}
	if body := readAll(t, resp.Body); len(body) != 0 {
		t.Errorf("304 carried a body of %d bytes, want none", len(body))
	}
}

func TestRaw_lifecycleAndUnknownStatuses(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}

	dsn := startPostgres(t)
	env := startMinio(t)
	srv := newTestServer(t, dsn, env, "https://mdfly.dev")

	const rootPath = "life.md"
	slug := publishFiles(t, srv, "aaaaaaaa-bbbb-cccc-dddd-ffffffffff04", "/proj", rootPath, map[string][]byte{rootPath: []byte("# Life\n")})

	// A delete (soft-deleted row) serves 410 with a plain-text body, never HTML.
	setDocumentStatus(t, dsn, slug, "deleted")
	code, body := getString(t, srv.URL+"/raw/"+slug)
	if code != http.StatusGone {
		t.Errorf("deleted raw status=%d, want 410", code)
	}
	assertPlainNotHTML(t, srv.URL+"/raw/"+slug, body)

	// A never-existed slug serves 404, also plain text.
	code, body = getString(t, srv.URL+"/raw/doesnotexist99")
	if code != http.StatusNotFound {
		t.Errorf("unknown raw status=%d, want 404", code)
	}
	assertPlainNotHTML(t, srv.URL+"/raw/doesnotexist99", body)

	// A directory prefix 404s (raw is files-only).
	dirSlug := publishFiles(t, srv, "aaaaaaaa-bbbb-cccc-dddd-ffffffffff05", "/proj", "index.md", map[string][]byte{
		"index.md":  []byte("# Index\n"),
		"docs/a.md": []byte("# A\n"),
	})
	if code, _ := getString(t, srv.URL+"/raw/"+dirSlug+"/docs"); code != http.StatusNotFound {
		t.Errorf("directory-prefix raw status=%d, want 404", code)
	}
}

func TestRaw_trailingSlashRedirects(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}

	dsn := startPostgres(t)
	env := startMinio(t)
	srv := newTestServer(t, dsn, env, "https://mdfly.dev")

	files := map[string][]byte{
		"index.md": []byte("# Index\n"),
		"a.md":     []byte("# A\n"),
	}
	slug := publishFiles(t, srv, "aaaaaaaa-bbbb-cccc-dddd-ffffffffff06", "/proj", "index.md", files)

	resp, err := noRedirectClient().Get(srv.URL + "/raw/" + slug + "/a.md/")
	if err != nil {
		t.Fatalf("GET trailing slash: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMovedPermanently {
		t.Fatalf("trailing-slash status=%d, want 301", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/raw/"+slug+"/a.md" {
		t.Errorf("Location=%q, want /raw/%s/a.md", loc, slug)
	}
}

// assertPlainNotHTML re-GETs url to check its error body carries a text/plain
// content type and no HTML — raw never emits the viewer's HTML 404 body.
func assertPlainNotHTML(t *testing.T, url, body string) {
	t.Helper()
	ct := getString2Header(t, url, "Content-Type")
	if !containsStr(ct, "text/plain") {
		t.Errorf("error Content-Type=%q, want text/plain", ct)
	}
	if containsStr(body, "<html") || containsStr(body, "<!doctype") {
		t.Errorf("raw error body is HTML, want plain text: %q", body)
	}
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func itoa(n int) string {
	return strconv.Itoa(n)
}
