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
