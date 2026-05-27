package handlers_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Abir66/mdfly/internal/api"
	"github.com/Abir66/mdfly/internal/server/handlers"
	pgstore "github.com/Abir66/mdfly/internal/server/store/postgres"
	r2store "github.com/Abir66/mdfly/internal/server/store/r2"
)

func newFullTestServer(t *testing.T, dsn string, env minioEnv, baseURL string) *httptest.Server {
	t.Helper()

	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)

	pg := pgstore.New(pool)
	r2 := r2store.New(r2store.Config{
		Endpoint:        env.endpoint,
		AccessKeyID:     env.accessKey,
		SecretAccessKey: env.secretKey,
		Bucket:          env.bucket,
		PublicBaseURL:   env.endpoint + "/" + env.bucket,
	})

	publishDeps := handlers.PublishDeps{PG: pg, R2: r2, BaseURL: baseURL}
	viewDeps := handlers.ViewDeps{PG: pg, R2: r2}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/publish/init", handlers.Init(publishDeps))
	mux.HandleFunc("POST /v1/publish/commit", handlers.Commit(publishDeps))
	mux.HandleFunc("GET /{slug}", handlers.View(viewDeps))

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestView_returnsMarkdownInPre(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}

	dsn := startPostgres(t)
	env := startMinio(t)
	srv := newFullTestServer(t, dsn, env, "https://mdfly.dev")

	content := []byte("# Hello\n\nThis is mdfly.\n")
	hash := contentHash(content)
	manifest := api.Manifest{
		Root:  "hello.md",
		Files: []api.ManifestFile{{Path: "hello.md", Hash: hash, Size: int64(len(content))}},
	}
	idempKey := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeee01"

	initR := postJSON(t, srv.URL+"/v1/publish/init", api.InitRequest{
		IdempotencyKey: idempKey, Manifest: manifest,
	})
	initBody := decodeInitResponse(t, initR)
	putBlob(t, initBody.PresignedURLs[hash], content, hash)

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
	if !strings.Contains(bodyStr, "<pre>") {
		t.Errorf("body missing <pre>: %s", bodyStr)
	}
	if !strings.Contains(bodyStr, string(content)) {
		t.Errorf("body missing markdown content: %s", bodyStr)
	}
}

func TestView_missingSlugReturns404(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}

	dsn := startPostgres(t)
	env := startMinio(t)
	srv := newFullTestServer(t, dsn, env, "https://mdfly.dev")

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
	srv := newFullTestServer(t, dsn, env, "https://mdfly.dev")

	content := []byte("<script>alert('xss')</script>\n")
	hash := contentHash(content)
	manifest := api.Manifest{
		Root:  "xss.md",
		Files: []api.ManifestFile{{Path: "xss.md", Hash: hash, Size: int64(len(content))}},
	}
	idempKey := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeee02"

	initR := postJSON(t, srv.URL+"/v1/publish/init", api.InitRequest{
		IdempotencyKey: idempKey, Manifest: manifest,
	})
	initBody := decodeInitResponse(t, initR)
	putBlob(t, initBody.PresignedURLs[hash], content, hash)

	commitR := postJSON(t, srv.URL+"/v1/publish/commit", api.CommitRequest{IdempotencyKey: idempKey})
	commitBody := decodeCommitResponse(t, commitR)

	resp, err := http.Get(srv.URL + "/" + commitBody.Slug)
	if err != nil {
		t.Fatalf("GET /%s: %v", commitBody.Slug, err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	bodyStr := string(body)

	if strings.Contains(bodyStr, "<script>") {
		t.Error("body contains unescaped <script> tag — XSS risk")
	}
	if !strings.Contains(bodyStr, "&lt;script&gt;") {
		t.Errorf("body missing escaped script tag, got: %s", bodyStr)
	}
}

