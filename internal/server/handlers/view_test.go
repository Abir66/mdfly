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
	if !strings.Contains(body, fmt.Sprintf(`href="/%s/y"`, slug)) {
		t.Errorf("root: ./y.md not rewritten to /%s/y:\n%s", slug, body)
	}
	if !strings.Contains(body, fmt.Sprintf(`href="/%s/sub2/a"`, slug)) {
		t.Errorf("root: ./sub2/a.md not rewritten to /%s/sub2/a:\n%s", slug, body)
	}
	if !strings.Contains(body, logoSrc) {
		t.Errorf("root: logo not rewritten to CDN URL %q:\n%s", logoSrc, body)
	}

	// Nested page y: renders and back-link rewritten relative to root.
	status, body = getString(t, srv.URL+"/"+slug+"/y")
	if status != http.StatusOK {
		t.Fatalf("/%s/y status=%d, want 200", slug, status)
	}
	if !strings.Contains(body, "<h1") || !strings.Contains(body, "Y") {
		t.Errorf("/%s/y missing rendered heading:\n%s", slug, body)
	}
	if !strings.Contains(body, fmt.Sprintf(`href="/%s/index"`, slug)) {
		t.Errorf("/%s/y: ./index.md back-link not rewritten:\n%s", slug, body)
	}

	// Nested page sub2/a: relative AND absolute image refs resolve to root logo.
	status, body = getString(t, srv.URL+"/"+slug+"/sub2/a")
	if status != http.StatusOK {
		t.Fatalf("/%s/sub2/a status=%d, want 200", slug, status)
	}
	if !strings.Contains(body, "Sub2 A") {
		t.Errorf("/%s/sub2/a missing content:\n%s", slug, body)
	}
	if !strings.Contains(body, logoSrc) {
		t.Errorf("/%s/sub2/a: ../logo.png + /proj/logo.png must resolve to %q:\n%s", slug, logoSrc, body)
	}

	// Unknown nested path → 404.
	status, _ = getString(t, srv.URL+"/"+slug+"/does-not-exist")
	if status != http.StatusNotFound {
		t.Errorf("unknown nested path status=%d, want 404", status)
	}
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

	status, body := getString(t, srv.URL+"/"+slug+"/parent?up=1")
	if status != http.StatusOK {
		t.Fatalf("/%s/parent?up=1 status=%d, want 200", slug, status)
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

	if strings.Contains(bodyStr, "<script") {
		t.Error("body contains <script> tag — XSS risk")
	}
}
