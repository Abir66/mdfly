package publish_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscredentials "github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/Abir66/mdfly/internal/api"
	"github.com/Abir66/mdfly/internal/cli/localstate"
	"github.com/Abir66/mdfly/internal/server/db"
	"github.com/Abir66/mdfly/internal/server/handlers"
	"github.com/Abir66/mdfly/internal/server/service/document"
	"github.com/Abir66/mdfly/internal/server/service/gc"
	"github.com/Abir66/mdfly/internal/server/service/publish"
	"github.com/Abir66/mdfly/internal/server/service/view"
	"github.com/Abir66/mdfly/internal/server/static"
	"github.com/Abir66/mdfly/internal/server/storage"
)

func projectRoot() string {
	_, filename, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(filename), "../../..")
}

func e2eMigrationsDir() string {
	return filepath.Join(projectRoot(), "migrations")
}

func buildCLI(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "mdfly")
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/mdfly")
	cmd.Dir = projectRoot()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, out)
	}
	return bin
}

func startPostgres(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	req := testcontainers.ContainerRequest{
		Image:        "postgres:16-alpine",
		ExposedPorts: []string{"5432/tcp"},
		Env: map[string]string{
			"POSTGRES_DB":       "mdfly",
			"POSTGRES_USER":     "mdfly",
			"POSTGRES_PASSWORD": "secret",
		},
		WaitingFor: wait.ForLog("database system is ready to accept connections").
			WithOccurrence(2).WithStartupTimeout(60 * time.Second),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req, Started: true,
	})
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	t.Cleanup(func() { c.Terminate(ctx) }) //nolint:errcheck

	host, err := c.Host(ctx)
	if err != nil {
		t.Fatalf("postgres host: %v", err)
	}
	port, err := c.MappedPort(ctx, "5432/tcp")
	if err != nil {
		t.Fatalf("postgres port: %v", err)
	}
	dsn := fmt.Sprintf("postgres://mdfly:secret@%s:%s/mdfly?sslmode=disable", host, port.Port())

	migrateDSN := strings.Replace(dsn, "postgres://", "pgx5://", 1)
	m, err := migrate.New("file://"+e2eMigrationsDir(), migrateDSN)
	if err != nil {
		t.Fatalf("create migrator: %v", err)
	}
	if err := m.Up(); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	m.Close()
	return dsn
}

type minioEnv struct {
	endpoint  string
	accessKey string
	secretKey string
	bucket    string
}

func startMinio(t *testing.T) minioEnv {
	t.Helper()
	const (
		accessKey = "minioadmin"
		secretKey = "minioadmin"
		bucket    = "mdfly"
	)
	ctx := context.Background()
	req := testcontainers.ContainerRequest{
		Image:        "minio/minio:RELEASE.2025-09-07T16-13-09Z",
		ExposedPorts: []string{"9000/tcp"},
		Env: map[string]string{
			"MINIO_ROOT_USER":     accessKey,
			"MINIO_ROOT_PASSWORD": secretKey,
		},
		Cmd: []string{"server", "/data"},
		WaitingFor: wait.ForHTTP("/minio/health/live").
			WithPort("9000/tcp").WithStartupTimeout(60 * time.Second),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req, Started: true,
	})
	if err != nil {
		t.Fatalf("start minio: %v", err)
	}
	t.Cleanup(func() { c.Terminate(ctx) }) //nolint:errcheck

	host, err := c.Host(ctx)
	if err != nil {
		t.Fatalf("minio host: %v", err)
	}
	port, err := c.MappedPort(ctx, "9000/tcp")
	if err != nil {
		t.Fatalf("minio port: %v", err)
	}
	endpoint := fmt.Sprintf("http://%s:%s", host, port.Port())

	s3Client := s3.New(s3.Options{
		BaseEndpoint: aws.String(endpoint),
		Credentials:  awscredentials.NewStaticCredentialsProvider(accessKey, secretKey, ""),
		Region:       "auto",
		UsePathStyle: true,
	})
	if _, err := s3Client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String(bucket),
	}); err != nil {
		t.Fatalf("create bucket: %v", err)
	}
	return minioEnv{endpoint: endpoint, accessKey: accessKey, secretKey: secretKey, bucket: bucket}
}

func TestCLIPublish(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}

	dsn := startPostgres(t)
	env := startMinio(t)

	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)

	pg := db.New(pool)
	r2 := storage.New(storage.Config{
		Endpoint:        env.endpoint,
		AccessKeyID:     env.accessKey,
		SecretAccessKey: env.secretKey,
		Bucket:          env.bucket,
		PublicBaseURL:   env.endpoint + "/" + env.bucket,
	})

	// Use httptest.NewUnstartedServer so we know the address before starting.
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	pubSvc := &publish.Service{Db: pg, Storage: r2, BaseURL: srv.URL}
	viewSvc := &view.Service{Db: pg, Storage: r2, Static: static.New()}
	mux.HandleFunc("POST /v1/publish/init", handlers.Init(pubSvc))
	mux.HandleFunc("POST /v1/publish/commit", handlers.Commit(pubSvc))
	mux.HandleFunc("GET /{slug}", handlers.View(viewSvc))

	bin := buildCLI(t)

	dir := t.TempDir()
	mdFile := filepath.Join(dir, "hello.md")
	content := []byte("# Hello mdfly\n\nThis is a test document.\n")
	if err := os.WriteFile(mdFile, content, 0644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(bin, "publish", mdFile)
	cmd.Env = append(os.Environ(), "MDFLY_API="+srv.URL)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		t.Fatalf("mdfly publish failed: %v\nstderr: %s", err, stderr.String())
	}

	url := strings.TrimSpace(stdout.String())
	if !strings.HasPrefix(url, srv.URL+"/") {
		t.Errorf("stdout URL=%q, want prefix %s/", url, srv.URL)
	}
	if stderr.Len() != 0 {
		t.Errorf("unexpected stderr: %s", stderr.String())
	}

	// Verify the document is accessible.
	const verifyTimeout = 10 * time.Second
	verifyClient := &http.Client{Timeout: verifyTimeout}
	resp, err := verifyClient.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET %s: status=%d, want 200", url, resp.StatusCode)
	}
}

func TestCLIPublish_withImage(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}

	dsn := startPostgres(t)
	env := startMinio(t)

	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)

	pg := db.New(pool)
	publicBase := env.endpoint + "/" + env.bucket
	r2 := storage.New(storage.Config{
		Endpoint:        env.endpoint,
		AccessKeyID:     env.accessKey,
		SecretAccessKey: env.secretKey,
		Bucket:          env.bucket,
		PublicBaseURL:   publicBase,
	})

	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	pubSvc := &publish.Service{Db: pg, Storage: r2, BaseURL: srv.URL}
	viewSvc := &view.Service{Db: pg, Storage: r2, Static: static.New()}
	mux.HandleFunc("POST /v1/publish/init", handlers.Init(pubSvc))
	mux.HandleFunc("POST /v1/publish/commit", handlers.Commit(pubSvc))
	mux.HandleFunc("GET /{slug}", handlers.View(viewSvc))

	bin := buildCLI(t)

	dir := t.TempDir()
	imgContent := []byte("\x89PNG\r\n\x1a\nfakepngbytes")
	if err := os.WriteFile(filepath.Join(dir, "logo.png"), imgContent, 0644); err != nil {
		t.Fatal(err)
	}
	mdFile := filepath.Join(dir, "hello.md")
	mdContent := []byte("# Hello\n\n![logo](./logo.png)\n\n![remote](https://example.com/x.png)\n")
	if err := os.WriteFile(mdFile, mdContent, 0644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(bin, "publish", mdFile)
	cmd.Env = append(os.Environ(), "MDFLY_API="+srv.URL)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("mdfly publish failed: %v\nstderr: %s", err, stderr.String())
	}

	url := strings.TrimSpace(stdout.String())
	slug := strings.TrimPrefix(url, srv.URL+"/")

	// Both blobs landed in R2 (authenticated HEAD; minio denies anonymous reads).
	rootKey := storage.BlobKey(slug, hashOf(mdContent), ".md")
	assetKey := storage.BlobKey(slug, hashOf(imgContent), ".png")
	if err := r2.HeadBlob(context.Background(), rootKey, int64(len(mdContent))); err != nil {
		t.Errorf("root blob missing in R2: %v", err)
	}
	if err := r2.HeadBlob(context.Background(), assetKey, int64(len(imgContent))); err != nil {
		t.Errorf("asset blob missing in R2: %v", err)
	}

	// Rendered page rewrites the local image to the absolute CDN URL and leaves
	// the external image untouched.
	assetURL := fmt.Sprintf("%s/documents/%s/%s.png", publicBase, slug, hashOf(imgContent))
	verifyClient := &http.Client{Timeout: 10 * time.Second}
	pr, err := verifyClient.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer pr.Body.Close()
	if pr.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status=%d, want 200", url, pr.StatusCode)
	}
	body, _ := io.ReadAll(pr.Body)
	bodyStr := string(body)
	if !strings.Contains(bodyStr, fmt.Sprintf(`src="%s"`, assetURL)) {
		t.Errorf("rendered body missing rewritten asset URL %q in:\n%s", assetURL, bodyStr)
	}
	if !strings.Contains(bodyStr, `src="https://example.com/x.png"`) {
		t.Errorf("external image must be preserved verbatim in:\n%s", bodyStr)
	}
}

// keyRecorder collects the idempotency_key seen on each request, across retries.
type keyRecorder struct {
	mu   sync.Mutex
	keys []string
}

func (k *keyRecorder) add(key string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.keys = append(k.keys, key)
}

func (k *keyRecorder) all() []string {
	k.mu.Lock()
	defer k.mu.Unlock()
	return append([]string(nil), k.keys...)
}

// failFirstN wraps h so the first n calls return 503 before reaching h; retries
// after n run the real handler, exercising server-side idempotency (ADR-0006).
// Every call records the request's idempotency_key (body preserved for h).
func failFirstN(n int32, rec *keyRecorder, h http.HandlerFunc) (http.HandlerFunc, *int32) {
	var calls int32
	wrapped := func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		var probe struct {
			IdempotencyKey string `json:"idempotency_key"`
		}
		json.Unmarshal(body, &probe) //nolint:errcheck
		rec.add(probe.IdempotencyKey)
		if atomic.AddInt32(&calls, 1) <= n {
			http.Error(w, `{"error":{"code":"unavailable","message":"transient"}}`, http.StatusServiceUnavailable)
			return
		}
		h(w, r)
	}
	return wrapped, &calls
}

// assertSameKey checks every recorded key is non-empty and identical, proving
// the CLI reused one idempotency_key across all retries of a phase.
func assertSameKey(t *testing.T, phase string, keys []string) {
	t.Helper()
	if len(keys) == 0 {
		t.Fatalf("%s: no idempotency_key recorded", phase)
	}
	for i, k := range keys {
		if k == "" {
			t.Errorf("%s call %d: empty idempotency_key", phase, i)
		}
		if k != keys[0] {
			t.Errorf("%s call %d: key %q != first %q", phase, i, k, keys[0])
		}
	}
}

// TestCLIPublish_retriesOnTransient503 fails the first two init and commit
// attempts with 503; the CLI's third attempt of each must succeed with the same
// idempotency_key, yielding a viewable document.
func TestCLIPublish_retriesOnTransient503(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}

	dsn := startPostgres(t)
	env := startMinio(t)

	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)

	pg := db.New(pool)
	r2 := storage.New(storage.Config{
		Endpoint:        env.endpoint,
		AccessKeyID:     env.accessKey,
		SecretAccessKey: env.secretKey,
		Bucket:          env.bucket,
		PublicBaseURL:   env.endpoint + "/" + env.bucket,
	})

	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	pubSvc := &publish.Service{Db: pg, Storage: r2, BaseURL: srv.URL}
	viewSvc := &view.Service{Db: pg, Storage: r2, Static: static.New()}

	const failures = 2
	var initKeys, commitKeys keyRecorder
	initH, initCalls := failFirstN(failures, &initKeys, handlers.Init(pubSvc))
	commitH, commitCalls := failFirstN(failures, &commitKeys, handlers.Commit(pubSvc))
	mux.HandleFunc("POST /v1/publish/init", initH)
	mux.HandleFunc("POST /v1/publish/commit", commitH)
	mux.HandleFunc("GET /{slug}", handlers.View(viewSvc))

	bin := buildCLI(t)

	dir := t.TempDir()
	mdFile := filepath.Join(dir, "hello.md")
	content := []byte("# Retry test\n\nTransient 503s.\n")
	if err := os.WriteFile(mdFile, content, 0644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(bin, "publish", mdFile)
	cmd.Env = append(os.Environ(), "MDFLY_API="+srv.URL)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("mdfly publish failed despite retryable 503s: %v\nstderr: %s", err, stderr.String())
	}

	if got := atomic.LoadInt32(initCalls); got != failures+1 {
		t.Errorf("init calls=%d, want %d (2 failed + 1 success)", got, failures+1)
	}
	if got := atomic.LoadInt32(commitCalls); got != failures+1 {
		t.Errorf("commit calls=%d, want %d (2 failed + 1 success)", got, failures+1)
	}

	assertSameKey(t, "init", initKeys.all())
	assertSameKey(t, "commit", commitKeys.all())
	if initKeys.all()[0] != commitKeys.all()[0] {
		t.Errorf("init key %q != commit key %q; one key per publish expected",
			initKeys.all()[0], commitKeys.all()[0])
	}

	url := strings.TrimSpace(stdout.String())
	if !strings.HasPrefix(url, srv.URL+"/") {
		t.Fatalf("stdout URL=%q, want prefix %s/", url, srv.URL)
	}

	verifyClient := &http.Client{Timeout: 10 * time.Second}
	resp, err := verifyClient.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET %s: status=%d, want 200", url, resp.StatusCode)
	}
}

func TestCLIPublish_missingAssetFails(t *testing.T) {
	bin := buildCLI(t)
	dir := t.TempDir()
	mdFile := filepath.Join(dir, "hello.md")
	if err := os.WriteFile(mdFile, []byte("![x](./does-not-exist.png)\n"), 0644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(bin, "publish", mdFile)
	cmd.Env = append(os.Environ(), "MDFLY_API=http://127.0.0.1:0")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		t.Fatal("want non-zero exit for missing referenced asset, got success")
	}
	if !strings.Contains(stderr.String(), "does-not-exist.png") {
		t.Errorf("stderr should name the missing asset, got: %s", stderr.String())
	}
}

func TestCLIPublish_noArgs(t *testing.T) {
	bin := buildCLI(t)
	cmd := exec.Command(bin)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		t.Error("want non-zero exit when no args, got success")
	}
	if stderr.Len() == 0 {
		t.Error("want usage on stderr when no args")
	}
}

// TestCLIPublish_sourcesAndState drives publish from every content source
// against one server and asserts each returns a URL and persists an independent
// slug-keyed record: a file twice (two records), inline -m, and piped stdin.
func TestCLIPublish_sourcesAndState(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}

	dsn := startPostgres(t)
	env := startMinio(t)

	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)

	pg := db.New(pool)
	r2 := storage.New(storage.Config{
		Endpoint:        env.endpoint,
		AccessKeyID:     env.accessKey,
		SecretAccessKey: env.secretKey,
		Bucket:          env.bucket,
		PublicBaseURL:   env.endpoint + "/" + env.bucket,
	})

	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	pubSvc := &publish.Service{Db: pg, Storage: r2, BaseURL: srv.URL}
	mux.HandleFunc("POST /v1/publish/init", handlers.Init(pubSvc))
	mux.HandleFunc("POST /v1/publish/commit", handlers.Commit(pubSvc))

	bin := buildCLI(t)
	configDir := t.TempDir()
	workDir := t.TempDir()

	mdFile := filepath.Join(workDir, "notes.md")
	if err := os.WriteFile(mdFile, []byte("# Notes\n"), 0644); err != nil {
		t.Fatal(err)
	}

	run := func(stdin string, args ...string) string {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Dir = workDir
		cmd.Env = append(os.Environ(), "MDFLY_API="+srv.URL, "MDFLY_CONFIG_DIR="+configDir)
		if stdin != "" {
			cmd.Stdin = strings.NewReader(stdin)
		}
		var out, errOut bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errOut
		if err := cmd.Run(); err != nil {
			t.Fatalf("publish %v: %v\nstderr: %s", args, err, errOut.String())
		}
		url := strings.TrimSpace(out.String())
		if !strings.HasPrefix(url, srv.URL+"/") {
			t.Fatalf("publish %v: stdout=%q, want URL", args, url)
		}
		return url
	}

	url1 := run("", "publish", mdFile)
	url2 := run("", "publish", mdFile)
	if url1 == url2 {
		t.Errorf("repeated publish must mint independent URLs; both %s", url1)
	}
	run("", "publish", "-m", "# Inline note")
	run("# Piped note\n", "publish")

	led, err := localstate.New(configDir).Load()
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	recs := led.All()
	if len(recs) != 4 {
		t.Fatalf("state has %d records, want 4 (2 file + 1 -m + 1 stdin)", len(recs))
	}

	var fileRecs, textRecs int
	for _, rec := range recs {
		if !rec.HasToken {
			t.Errorf("record %s missing token marker", rec.Slug)
		}
		if tok, ok, _ := localstate.New(configDir).LoadToken(rec.Slug); !ok || tok == "" {
			t.Errorf("record %s: edit token not persisted", rec.Slug)
		}
		switch rec.Source {
		case localstate.SourceFile:
			fileRecs++
			if rec.Path == nil || *rec.Path != mdFile {
				t.Errorf("file record path=%v, want %q", rec.Path, mdFile)
			}
		case localstate.SourceText:
			textRecs++
			if rec.Path != nil {
				t.Errorf("text record path=%v, want nil", rec.Path)
			}
		}
	}
	if fileRecs != 2 || textRecs != 2 {
		t.Errorf("source split: file=%d text=%d, want 2/2", fileRecs, textRecs)
	}
}

// Ensure api.CommitResponse has a URL field — compile-time type check.
var _ = api.CommitResponse{URL: ""}

// TestCLIDelete_gcRemovesBlobs is the lifecycle end of the blackbox path: two
// documents are published, one is deleted through the CLI, its URL starts
// serving 410, and a GC pass past the blob-delete grace removes exactly that
// slug's blobs from R2 while the other document's blobs stay put (ADR-0005).
func TestCLIDelete_gcRemovesBlobs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}

	const (
		blobGrace  = 24 * time.Hour
		pastGrace  = blobGrace + time.Hour
		configPerm = 0644
	)

	dsn := startPostgres(t)
	env := startMinio(t)

	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)

	pg := db.New(pool)
	r2 := storage.New(storage.Config{
		Endpoint:        env.endpoint,
		AccessKeyID:     env.accessKey,
		SecretAccessKey: env.secretKey,
		Bucket:          env.bucket,
		PublicBaseURL:   env.endpoint + "/" + env.bucket,
	})

	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	pubSvc := &publish.Service{Db: pg, Storage: r2, BaseURL: srv.URL}
	viewSvc := &view.Service{Db: pg, Storage: r2, Static: static.New()}
	docSvc := &document.Service{Db: pg}
	mux.HandleFunc("POST /v1/publish/init", handlers.Init(pubSvc))
	mux.HandleFunc("POST /v1/publish/commit", handlers.Commit(pubSvc))
	mux.HandleFunc("DELETE /v1/documents/{slug}", handlers.DeleteDocument(docSvc))
	mux.HandleFunc("GET /{slug}", handlers.View(viewSvc))

	bin := buildCLI(t)
	configDir := t.TempDir()
	workDir := t.TempDir()

	imgContent := []byte("\x89PNG\r\n\x1a\ndoomedpng")
	if err := os.WriteFile(filepath.Join(workDir, "logo.png"), imgContent, configPerm); err != nil {
		t.Fatal(err)
	}
	doomedContent := []byte("# Doomed\n\n![logo](./logo.png)\n")
	if err := os.WriteFile(filepath.Join(workDir, "doomed.md"), doomedContent, configPerm); err != nil {
		t.Fatal(err)
	}
	keeperContent := []byte("# Keeper\n")
	if err := os.WriteFile(filepath.Join(workDir, "keeper.md"), keeperContent, configPerm); err != nil {
		t.Fatal(err)
	}

	runCLI := func(args ...string) string {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Dir = workDir
		cmd.Env = append(os.Environ(), "MDFLY_API="+srv.URL, "MDFLY_CONFIG_DIR="+configDir)
		var out, errOut bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errOut
		if err := cmd.Run(); err != nil {
			t.Fatalf("mdfly %v: %v\nstderr: %s", args, err, errOut.String())
		}
		return strings.TrimSpace(out.String())
	}

	doomedSlug := strings.TrimPrefix(runCLI("publish", "doomed.md"), srv.URL+"/")
	keeperSlug := strings.TrimPrefix(runCLI("publish", "keeper.md"), srv.URL+"/")

	ctx := context.Background()
	doomedRoot := storage.BlobKey(doomedSlug, hashOf(doomedContent), ".md")
	doomedAsset := storage.BlobKey(doomedSlug, hashOf(imgContent), ".png")
	keeperRoot := storage.BlobKey(keeperSlug, hashOf(keeperContent), ".md")
	for _, blob := range []struct {
		key  string
		size int
	}{
		{doomedRoot, len(doomedContent)},
		{doomedAsset, len(imgContent)},
		{keeperRoot, len(keeperContent)},
	} {
		if err := r2.HeadBlob(ctx, blob.key, int64(blob.size)); err != nil {
			t.Fatalf("blob %s missing before delete: %v", blob.key, err)
		}
	}

	if out := runCLI("delete", "-y", "--slug", doomedSlug); !strings.Contains(out, doomedSlug) {
		t.Fatalf("delete stdout = %q, want it to name %s", out, doomedSlug)
	}

	verifyClient := &http.Client{Timeout: 10 * time.Second}
	assertStatus := func(slug string, want int) {
		t.Helper()
		resp, err := verifyClient.Get(srv.URL + "/" + slug)
		if err != nil {
			t.Fatalf("GET /%s: %v", slug, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("GET /%s = %d, want %d", slug, resp.StatusCode, want)
		}
	}
	assertStatus(doomedSlug, http.StatusGone)
	assertStatus(keeperSlug, http.StatusOK)

	sweeper := &gc.Service{
		Db:              pg,
		Blobs:           r2,
		AbandonGrace:    time.Hour,
		BlobDeleteGrace: blobGrace,
		Now:             func() time.Time { return time.Now().Add(pastGrace) },
	}
	res, err := sweeper.Sweep(ctx)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if res.BlobsDeleted != 1 {
		t.Fatalf("sweep deleted blobs for %d rows, want 1", res.BlobsDeleted)
	}

	for _, key := range []string{doomedRoot, doomedAsset} {
		if err := r2.HeadBlob(ctx, key, 0); !errors.Is(err, storage.ErrBlobMissing) {
			t.Errorf("blob %s after GC: err = %v, want ErrBlobMissing", key, err)
		}
	}
	if err := r2.HeadBlob(ctx, keeperRoot, int64(len(keeperContent))); err != nil {
		t.Errorf("live document's blob was collateral damage: %v", err)
	}
	assertStatus(doomedSlug, http.StatusGone)

	// The stamped row leaves the work-list, so a second pass finds nothing.
	res, err = sweeper.Sweep(ctx)
	if err != nil {
		t.Fatalf("second Sweep: %v", err)
	}
	if res.BlobsDeleted != 0 {
		t.Errorf("second pass deleted blobs for %d rows, want 0", res.BlobsDeleted)
	}
}
