package handlers_test

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Abir66/mdfly/internal/api"
)

func TestView_returnsRenderedMarkdown(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}

	dsn := startPostgres(t)
	env := startMinio(t)
	srv := newTestServer(t, dsn, env, "https://mdfly.dev")

	content := []byte("# Hello\n\nThis is mdfly.\n")
	const rootPath = "hello.md"
	bundle := singleFileBundle(rootPath, content)
	idempKey := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeee01"

	initR := postJSON(t, srv.URL+"/v1/publish/init", api.InitRequest{
		IdempotencyKey: idempKey, Bundle: bundle,
	})
	initBody := decodeInitResponse(t, initR)
	putBlob(t, initBody.PresignedURLs[rootPath], content)

	commitR := postJSON(t, srv.URL+"/v1/publish/commit", api.CommitRequest{IdempotencyKey: idempKey})
	commitBody := decodeCommitResponse(t, commitR)

	resp, err := http.Get(srv.URL + "/" + commitBody.Slug)
	if err != nil {
		t.Fatalf("GET /%s: %v", commitBody.Slug, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200", resp.StatusCode)
	}

	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "text/html") {
		t.Errorf("Content-Type=%q, want text/html", ct)
	}
	if !strings.Contains(ct, "charset=utf-8") {
		t.Errorf("Content-Type=%q, want charset=utf-8", ct)
	}

	if csp := resp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "cdn.jsdelivr.net") {
		t.Errorf("Content-Security-Policy=%q, want jsdelivr CDN allowed", csp)
	}

	if cc := resp.Header.Get("Cache-Control"); cc != "public, max-age=300, s-maxage=86400" {
		t.Errorf("Cache-Control=%q, want public, max-age=300, s-maxage=86400", cc)
	}
	if v := resp.Header.Get("Vary"); v != "Accept" {
		t.Errorf("Vary=%q, want Accept", v)
	}
	if rt := resp.Header.Get("X-Robots-Tag"); rt != "noindex, nofollow" {
		t.Errorf("X-Robots-Tag=%q, want noindex, nofollow", rt)
	}

	body, _ := io.ReadAll(resp.Body)
	bodyStr := string(body)
	if !strings.Contains(bodyStr, "<h1") {
		t.Errorf("body missing rendered <h1> heading: %s", bodyStr)
	}
	if !strings.Contains(bodyStr, "Hello") {
		t.Errorf("body missing heading text: %s", bodyStr)
	}
}

func TestView_titleFromFrontmatter(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}

	dsn := startPostgres(t)
	env := startMinio(t)
	srv := newTestServer(t, dsn, env, "https://mdfly.dev")

	content := []byte("---\ntitle: My Page Title\ndescription: A brief excerpt.\n---\n\n# Heading\n\nBody text.\n")
	const rootPath = "titled.md"
	bundle := singleFileBundle(rootPath, content)
	idempKey := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeee03"

	initR := postJSON(t, srv.URL+"/v1/publish/init", api.InitRequest{
		IdempotencyKey: idempKey, Bundle: bundle,
	})
	initBody := decodeInitResponse(t, initR)
	putBlob(t, initBody.PresignedURLs[rootPath], content)

	commitR := postJSON(t, srv.URL+"/v1/publish/commit", api.CommitRequest{IdempotencyKey: idempKey})
	commitBody := decodeCommitResponse(t, commitR)

	resp, err := http.Get(srv.URL + "/" + commitBody.Slug)
	if err != nil {
		t.Fatalf("GET /%s: %v", commitBody.Slug, err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	bodyStr := string(body)

	if !strings.Contains(bodyStr, "<title>My Page Title</title>") {
		t.Errorf("missing <title>My Page Title</title> in: %s", bodyStr)
	}
	if !strings.Contains(bodyStr, `name="description" content="A brief excerpt."`) {
		t.Errorf("missing description meta in: %s", bodyStr)
	}
	if !strings.Contains(bodyStr, `og:title" content="My Page Title"`) {
		t.Errorf("missing og:title in: %s", bodyStr)
	}
}

func TestView_titleFromH1WhenNoFrontmatter(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}

	dsn := startPostgres(t)
	env := startMinio(t)
	srv := newTestServer(t, dsn, env, "https://mdfly.dev")

	content := []byte("# Inferred Title\n\nFirst paragraph as excerpt.\n")
	const rootPath = "inferred.md"
	bundle := singleFileBundle(rootPath, content)
	idempKey := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeee04"

	initR := postJSON(t, srv.URL+"/v1/publish/init", api.InitRequest{
		IdempotencyKey: idempKey, Bundle: bundle,
	})
	initBody := decodeInitResponse(t, initR)
	putBlob(t, initBody.PresignedURLs[rootPath], content)

	commitR := postJSON(t, srv.URL+"/v1/publish/commit", api.CommitRequest{IdempotencyKey: idempKey})
	commitBody := decodeCommitResponse(t, commitR)

	resp, err := http.Get(srv.URL + "/" + commitBody.Slug)
	if err != nil {
		t.Fatalf("GET /%s: %v", commitBody.Slug, err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	bodyStr := string(body)

	if !strings.Contains(bodyStr, "<title>Inferred Title</title>") {
		t.Errorf("missing <title>Inferred Title</title> in: %s", bodyStr)
	}
}

func TestView_rewritesImageAssetURLs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}

	dsn := startPostgres(t)
	env := startMinio(t)
	srv := newTestServer(t, dsn, env, "https://mdfly.dev")
	publicBase := env.endpoint + "/" + env.bucket

	rootContent := []byte("# Pics\n\n![logo](./logo.png)\n\n![remote](https://example.com/x.png)\n")
	imgContent := []byte("\x89PNG\r\n\x1a\nfakepngbytes")
	const rootPath = "hello.md"
	const imgPath = "logo.png"

	bundle := api.BundleDTO{
		RootPath: rootPath,
		Files: []api.BundleFileDTO{
			{Path: rootPath, Hash: contentHash(rootContent), Size: int64(len(rootContent))},
			{Path: imgPath, Hash: contentHash(imgContent), Size: int64(len(imgContent))},
		},
	}
	idempKey := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeee05"

	initR := postJSON(t, srv.URL+"/v1/publish/init", api.InitRequest{
		IdempotencyKey: idempKey, Bundle: bundle,
	})
	initBody := decodeInitResponse(t, initR)
	putBlob(t, initBody.PresignedURLs[rootPath], rootContent)
	putBlob(t, initBody.PresignedURLs[imgPath], imgContent)

	commitR := postJSON(t, srv.URL+"/v1/publish/commit", api.CommitRequest{IdempotencyKey: idempKey})
	commitBody := decodeCommitResponse(t, commitR)

	resp, err := http.Get(srv.URL + "/" + commitBody.Slug)
	if err != nil {
		t.Fatalf("GET /%s: %v", commitBody.Slug, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	bodyStr := string(body)

	wantSrc := fmt.Sprintf(`src="%s/documents/%s/%s.png"`, publicBase, commitBody.Slug, contentHash(imgContent))
	if !strings.Contains(bodyStr, wantSrc) {
		t.Errorf("body missing rewritten asset URL %q in:\n%s", wantSrc, bodyStr)
	}
	if !strings.Contains(bodyStr, `src="https://example.com/x.png"`) {
		t.Errorf("external image must be preserved verbatim in:\n%s", bodyStr)
	}
	wantOG := fmt.Sprintf(`og:image" content="%s/documents/%s/%s.png"`, publicBase, commitBody.Slug, contentHash(imgContent))
	if !strings.Contains(bodyStr, wantOG) {
		t.Errorf("og:image must carry absolute asset URL in:\n%s", bodyStr)
	}
}

func TestView_externalOGImagePreserved(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}

	dsn := startPostgres(t)
	env := startMinio(t)
	srv := newTestServer(t, dsn, env, "https://mdfly.dev")

	rootContent := []byte("---\nimage: \"https://external.example/og.png\"\n---\n# Hello\n")
	const rootPath = "hello.md"
	bundle := singleFileBundle(rootPath, rootContent)
	idempKey := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeee06"

	initR := postJSON(t, srv.URL+"/v1/publish/init", api.InitRequest{
		IdempotencyKey: idempKey, Bundle: bundle,
	})
	initBody := decodeInitResponse(t, initR)
	putBlob(t, initBody.PresignedURLs[rootPath], rootContent)

	commitR := postJSON(t, srv.URL+"/v1/publish/commit", api.CommitRequest{IdempotencyKey: idempKey})
	commitBody := decodeCommitResponse(t, commitR)

	resp, err := http.Get(srv.URL + "/" + commitBody.Slug)
	if err != nil {
		t.Fatalf("GET /%s: %v", commitBody.Slug, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	bodyStr := string(body)
	if !strings.Contains(bodyStr, `og:image" content="https://external.example/og.png"`) {
		t.Errorf("external og:image must be preserved verbatim in:\n%s", bodyStr)
	}
}

func TestView_nestedRoutingAndCrossMdRewrite(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}

	dsn := startPostgres(t)
	env := startMinio(t)
	srv := newTestServer(t, dsn, env, "https://mdfly.dev")
	publicBase := env.endpoint + "/" + env.bucket

	const projectRoot = "/proj"
	logo := []byte("\x89PNG\r\n\x1a\nfakepngbytes")
	logoHash := contentHash(logo)
	files := map[string][]byte{
		"index.md":  []byte("# Index\n\n[y](./y.md)\n\n[deep](./sub2/a.md)\n\n![logo](./logo.png)\n"),
		"y.md":      []byte("# Y\n\n[back](./index.md)\n"),
		"sub2/a.md": []byte("# Sub2 A\n\n![rel](../logo.png)\n\n![abs](/proj/logo.png)\n"),
		"logo.png":  logo,
	}
	slug := publishFiles(t, srv, "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeee11", projectRoot, "index.md", files)

	logoSrc := fmt.Sprintf(`src="%s/documents/%s/%s.png"`, publicBase, slug, logoHash)

	// Root page: cross-md links and image rewritten.
	status, body := getString(t, srv.URL+"/"+slug)
	if status != http.StatusOK {
		t.Fatalf("root status=%d, want 200", status)
	}
	if !strings.Contains(body, fmt.Sprintf(`href="/%s/y.md"`, slug)) {
		t.Errorf("root: ./y.md not rewritten to /%s/y.md:\n%s", slug, body)
	}
	if !strings.Contains(body, fmt.Sprintf(`href="/%s/sub2/a.md"`, slug)) {
		t.Errorf("root: ./sub2/a.md not rewritten to /%s/sub2/a.md:\n%s", slug, body)
	}
	if !strings.Contains(body, logoSrc) {
		t.Errorf("root: logo not rewritten to CDN URL %q:\n%s", logoSrc, body)
	}

	// Nested page y.md: renders and back-link rewritten relative to root.
	status, body = getString(t, srv.URL+"/"+slug+"/y.md")
	if status != http.StatusOK {
		t.Fatalf("/%s/y.md status=%d, want 200", slug, status)
	}
	if !strings.Contains(body, "<h1") || !strings.Contains(body, "Y") {
		t.Errorf("/%s/y.md missing rendered heading:\n%s", slug, body)
	}
	if !strings.Contains(body, fmt.Sprintf(`href="/%s/index.md"`, slug)) {
		t.Errorf("/%s/y.md: ./index.md back-link not rewritten:\n%s", slug, body)
	}

	// Nested page sub2/a.md: relative AND absolute image refs resolve to root logo.
	status, body = getString(t, srv.URL+"/"+slug+"/sub2/a.md")
	if status != http.StatusOK {
		t.Fatalf("/%s/sub2/a.md status=%d, want 200", slug, status)
	}
	if !strings.Contains(body, "Sub2 A") {
		t.Errorf("/%s/sub2/a.md missing content:\n%s", slug, body)
	}
	if !strings.Contains(body, logoSrc) {
		t.Errorf("/%s/sub2/a.md: ../logo.png + /proj/logo.png must resolve to %q:\n%s", slug, logoSrc, body)
	}

	// Directory prefix renders a Directory Listing of its immediate children.
	status, body = getString(t, srv.URL+"/"+slug+"/sub2")
	if status != http.StatusOK {
		t.Fatalf("directory prefix /%s/sub2 status=%d, want 200", slug, status)
	}
	if !strings.Contains(body, `class="listing"`) {
		t.Errorf("/%s/sub2 missing Directory Listing:\n%s", slug, body)
	}
	if !strings.Contains(body, fmt.Sprintf(`href="/%s/sub2/a.md"`, slug)) {
		t.Errorf("/%s/sub2 listing missing child link to a.md:\n%s", slug, body)
	}

	// Unknown nested path → 404.
	status, _ = getString(t, srv.URL+"/"+slug+"/does-not-exist.md")
	if status != http.StatusNotFound {
		t.Errorf("unknown nested path status=%d, want 404", status)
	}

	// Trailing slash 301-canonicalizes to the slash-free form.
	resp, err := noRedirectClient().Get(srv.URL + "/" + slug + "/y.md/")
	if err != nil {
		t.Fatalf("GET /%s/y.md/: %v", slug, err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMovedPermanently {
		t.Errorf("trailing-slash status=%d, want 301", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/"+slug+"/y.md" {
		t.Errorf("trailing-slash Location=%q, want /%s/y.md", loc, slug)
	}
}

func TestView_directoryListingWithReadme(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}

	dsn := startPostgres(t)
	env := startMinio(t)
	srv := newTestServer(t, dsn, env, "https://mdfly.dev")

	const projectRoot = "/proj"
	files := map[string][]byte{
		"index.md":         []byte("# Index\n\n[docs](./docs)\n"),
		"docs/README.md":   []byte("# Docs Home\n\nWelcome.\n"),
		"docs/guide.md":    []byte("# Guide\n"),
		"docs/api/spec.md": []byte("# Spec\n"),
	}
	slug := publishFiles(t, srv, "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeee33", projectRoot, "index.md", files)

	// A link to the directory lands on its listing, not a dead end.
	status, body := getString(t, srv.URL+"/"+slug)
	if status != http.StatusOK {
		t.Fatalf("root status=%d, want 200", status)
	}
	if !strings.Contains(body, fmt.Sprintf(`href="/%s/docs"`, slug)) {
		t.Errorf("root: ./docs link not rewritten to directory listing URL:\n%s", body)
	}

	// The directory listing: immediate folders (api/) first, then files, each
	// clickable; the README renders below the listing.
	status, body = getString(t, srv.URL+"/"+slug+"/docs")
	if status != http.StatusOK {
		t.Fatalf("/%s/docs status=%d, want 200", slug, status)
	}
	if cc := getString2Header(t, srv.URL+"/"+slug+"/docs", "Cache-Control"); cc != "public, max-age=300, s-maxage=86400" {
		t.Errorf("directory Cache-Control=%q, want public, max-age=300, s-maxage=86400", cc)
	}
	if !strings.Contains(body, `class="listing"`) {
		t.Errorf("/%s/docs missing Directory Listing:\n%s", slug, body)
	}
	if !strings.Contains(body, fmt.Sprintf(`href="/%s/docs/api"`, slug)) {
		t.Errorf("listing missing folder child api/:\n%s", body)
	}
	if !strings.Contains(body, fmt.Sprintf(`href="/%s/docs/guide.md"`, slug)) {
		t.Errorf("listing missing file child guide.md:\n%s", body)
	}
	if strings.Index(body, "docs/api") > strings.Index(body, "docs/guide.md") {
		t.Errorf("folders must list before files:\n%s", body)
	}
	if !strings.Contains(body, "Docs Home") {
		t.Errorf("/%s/docs missing rendered README below listing:\n%s", slug, body)
	}
}

// getString2Header GETs url and returns one response header value.
func getString2Header(t *testing.T, url, header string) string {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	return resp.Header.Get(header)
}

func TestView_chromeAndStaticAssets(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}

	dsn := startPostgres(t)
	env := startMinio(t)
	srv := newTestServer(t, dsn, env, "https://mdfly.dev")

	const projectRoot = "/proj"
	files := map[string][]byte{
		"index.md":         []byte("# Index\n\n[auth](./docs/api/auth.md)\n"),
		"docs/api/auth.md": []byte("# Auth\n\nbody\n"),
	}
	slug := publishFiles(t, srv, "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeee22", projectRoot, "index.md", files)

	// Nested markdown page renders the full chrome.
	status, body := getString(t, srv.URL+"/"+slug+"/docs/api/auth.md")
	if status != http.StatusOK {
		t.Fatalf("status=%d, want 200", status)
	}
	// Sidebar tree: file links keep their extension; current node highlighted.
	if !strings.Contains(body, fmt.Sprintf(`href="/%s/index.md"`, slug)) {
		t.Errorf("sidebar missing root file link:\n%s", body)
	}
	if !strings.Contains(body, fmt.Sprintf(`href="/%s/docs/api/auth.md"`, slug)) {
		t.Errorf("sidebar missing nested file link:\n%s", body)
	}
	if !strings.Contains(body, "<details open") {
		t.Errorf("current file's ancestor dirs must render <details open>:\n%s", body)
	}
	if !strings.Contains(body, `aria-current="page"`) {
		t.Errorf("current node missing aria-current:\n%s", body)
	}
	// Breadcrumb with a home crumb → bundle root.
	if !strings.Contains(body, `class="breadcrumb"`) || !strings.Contains(body, fmt.Sprintf(`href="/%s"`, slug)) {
		t.Errorf("missing breadcrumb / home crumb:\n%s", body)
	}

	// The page references a hashed /_static stylesheet; it must be reachable with
	// an immutable cache header.
	cssURL := extractStaticURL(t, body, ".css")
	resp, err := http.Get(srv.URL + cssURL)
	if err != nil {
		t.Fatalf("GET %s: %v", cssURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%s status=%d, want 200", cssURL, resp.StatusCode)
	}
	if cc := resp.Header.Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("%s Cache-Control=%q, want immutable", cssURL, cc)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/css") {
		t.Errorf("%s Content-Type=%q, want text/css", cssURL, ct)
	}
}

// extractStaticURL pulls the first /_static/...<ext> URL out of an HTML page.
// Pages reference several asset types (.css, .js), so it scans every /_static/
// occurrence and returns the first whose URL token actually ends in ext, rather
// than blindly extending the first match to the next ext (which can span URLs).
func extractStaticURL(t *testing.T, html, ext string) string {
	t.Helper()
	const marker = "/_static/"
	found := false
	for start := 0; ; {
		rel := strings.Index(html[start:], marker)
		if rel < 0 {
			break
		}
		i := start + rel
		found = true
		start = i + len(marker)
		// The URL token runs until a quote, space, or angle bracket.
		end := strings.IndexAny(html[i:], "\"' \t\n><")
		if end < 0 {
			end = len(html) - i
		}
		if url := html[i : i+end]; strings.HasSuffix(url, ext) {
			return url
		}
	}
	if !found {
		t.Fatalf("no /_static/ URL in page:\n%s", html)
	}
	t.Fatalf("no %s asset in page:\n%s", ext, html)
	return ""
}

// noRedirectClient returns an http.Client that does not follow redirects, so a
// 301's status and Location can be asserted directly.
func noRedirectClient() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
}

func TestView_aboveRootUpParam(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}

	dsn := startPostgres(t)
	env := startMinio(t)
	srv := newTestServer(t, dsn, env, "https://mdfly.dev")

	// Seed an above-root key directly (the CLI no longer emits these; the
	// ?up=N path stays for forward-compatibility — S10 revised).
	files := map[string][]byte{
		"index.md":     []byte("# Index\n"),
		"../parent.md": []byte("# Parent Page\n"),
	}
	slug := publishFiles(t, srv, "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeee12", "/proj/sub", "index.md", files)

	status, body := getString(t, srv.URL+"/"+slug+"/parent.md?up=1")
	if status != http.StatusOK {
		t.Fatalf("/%s/parent.md?up=1 status=%d, want 200", slug, status)
	}
	if !strings.Contains(body, "Parent Page") {
		t.Errorf("?up=1 did not reconstruct ../parent.md:\n%s", body)
	}
}

func TestView_missingSlugReturns404(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}

	dsn := startPostgres(t)
	env := startMinio(t)
	srv := newTestServer(t, dsn, env, "https://mdfly.dev")

	resp, err := http.Get(srv.URL + "/doesnotexist99")
	if err != nil {
		t.Fatalf("GET /doesnotexist99: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status=%d, want 404", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Errorf("Content-Type=%q, want text/html", ct)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "public, max-age=60" {
		t.Errorf("Cache-Control=%q, want public, max-age=60", cc)
	}
	if rt := resp.Header.Get("X-Robots-Tag"); rt != "noindex" {
		t.Errorf("X-Robots-Tag=%q, want noindex", rt)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "404 — not found") {
		t.Errorf("body missing 404 marker: %q", body)
	}
}

func TestView_htmlEscaping(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}

	dsn := startPostgres(t)
	env := startMinio(t)
	srv := newTestServer(t, dsn, env, "https://mdfly.dev")

	content := []byte("<script>alert('xss')</script>\n")
	const rootPath = "xss.md"
	bundle := singleFileBundle(rootPath, content)
	idempKey := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeee02"

	initR := postJSON(t, srv.URL+"/v1/publish/init", api.InitRequest{
		IdempotencyKey: idempKey, Bundle: bundle,
	})
	initBody := decodeInitResponse(t, initR)
	putBlob(t, initBody.PresignedURLs[rootPath], content)

	commitR := postJSON(t, srv.URL+"/v1/publish/commit", api.CommitRequest{IdempotencyKey: idempKey})
	commitBody := decodeCommitResponse(t, commitR)

	resp, err := http.Get(srv.URL + "/" + commitBody.Slug)
	if err != nil {
		t.Fatalf("GET /%s: %v", commitBody.Slug, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /%s: status=%d, want 200", commitBody.Slug, resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	bodyStr := string(body)

	// The chrome ships its own <script> tags (app.js + the inline rail snippet),
	// so assert the user-authored payload specifically is stripped, not that the
	// page has zero scripts.
	if strings.Contains(bodyStr, "alert('xss')") {
		t.Errorf("user <script> payload not sanitized — XSS risk:\n%s", bodyStr)
	}
	if strings.Contains(bodyStr, "<script>alert") {
		t.Errorf("body contains live user <script> tag — XSS risk:\n%s", bodyStr)
	}
}
