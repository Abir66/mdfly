package handlers_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscredentials "github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	_ "github.com/lib/pq"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/Abir66/mdfly/internal/api"
	"github.com/Abir66/mdfly/internal/server/handlers"
	pgstore "github.com/Abir66/mdfly/internal/server/store/postgres"
	r2store "github.com/Abir66/mdfly/internal/server/store/r2"
)

// ── container helpers ─────────────────────────────────────────────────────────

func migrationsDir() string {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		panic("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(filename), "../../../migrations")
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

	m, err := migrate.New("file://"+migrationsDir(), dsn)
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
		Image:        "minio/minio:latest",
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

	// Create bucket.
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

// ── test server ───────────────────────────────────────────────────────────────

func newTestServer(t *testing.T, dsn string, env minioEnv, baseURL string) *httptest.Server {
	t.Helper()

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	pg := pgstore.New(db)
	r2 := r2store.New(r2store.Config{
		Endpoint:        env.endpoint,
		AccessKeyID:     env.accessKey,
		SecretAccessKey: env.secretKey,
		Bucket:          env.bucket,
		PublicBaseURL:   env.endpoint + "/" + env.bucket,
	})
	deps := handlers.PublishDeps{PG: pg, R2: r2, BaseURL: baseURL}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/publish/init", handlers.Init(deps))
	mux.HandleFunc("POST /v1/publish/commit", handlers.Commit(deps))

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// ── request helpers ───────────────────────────────────────────────────────────

func postJSON(t *testing.T, url string, body any) *http.Response {
	t.Helper()
	b, _ := json.Marshal(body)
	resp, err := http.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	return resp
}

func readAll(t *testing.T, r io.Reader) []byte {
	t.Helper()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return b
}

func decodeInitResponse(t *testing.T, resp *http.Response) api.InitResponse {
	t.Helper()
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("init: status=%d body=%s", resp.StatusCode, body)
	}
	var v api.InitResponse
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("decode InitResponse: %v", err)
	}
	return v
}

func decodeCommitResponse(t *testing.T, resp *http.Response) api.CommitResponse {
	t.Helper()
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("commit: status=%d body=%s", resp.StatusCode, body)
	}
	var v api.CommitResponse
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("decode CommitResponse: %v", err)
	}
	return v
}

// contentHash returns the hex-encoded SHA256 of b.
func contentHash(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// hashBase64 converts a hex SHA256 to the base64 form used in x-amz-checksum-sha256.
func hashBase64(hexHash string) string {
	raw, _ := hex.DecodeString(hexHash)
	return base64.StdEncoding.EncodeToString(raw)
}

// putBlob sends the presigned PUT with the correct checksum header.
func putBlob(t *testing.T, presignedURL string, content []byte, hexHash string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, presignedURL, bytes.NewReader(content))
	if err != nil {
		t.Fatalf("build PUT request: %v", err)
	}
	req.ContentLength = int64(len(content))
	req.Header.Set("x-amz-checksum-sha256", hashBase64(hexHash))
	req.Header.Set("x-amz-sdk-checksum-algorithm", "SHA256")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT blob: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("PUT blob status %d: %s", resp.StatusCode, body)
	}
}

// ── integration tests ─────────────────────────────────────────────────────────

// TestPublishInitAndCommit is the tracer-bullet: init → upload → commit → published.
func TestPublishInitAndCommit(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}

	dsn := startPostgres(t)
	env := startMinio(t)
	srv := newTestServer(t, dsn, env, "https://mdfly.dev")

	content := []byte("# Hello mdfly\n\nThis is a test document.\n")
	hash := contentHash(content)

	manifest := api.Manifest{
		Root:  "hello.md",
		Files: []api.ManifestFile{{Path: "hello.md", Hash: hash, Size: int64(len(content))}},
	}
	idempotencyKey := "550e8400-e29b-41d4-a716-446655440000"

	// Phase 1: init.
	initResp := postJSON(t, srv.URL+"/v1/publish/init", api.InitRequest{
		IdempotencyKey: idempotencyKey,
		Manifest:       manifest,
		EditToken:      "mftk_testtoken",
	})
	if initResp.StatusCode != http.StatusOK {
		body := readAll(t, initResp.Body)
		initResp.Body.Close()
		t.Fatalf("init status %d: %s", initResp.StatusCode, body)
	}
	initBody := decodeInitResponse(t, initResp)

	if initBody.Slug == "" {
		t.Fatal("init response: empty slug")
	}
	if initBody.InlineAccept {
		t.Error("init response: inline_accept should be false")
	}
	if len(initBody.MissingHashes) != 1 || initBody.MissingHashes[0] != hash {
		t.Errorf("init response: missing_hashes=%v, want [%s]", initBody.MissingHashes, hash)
	}
	presignedURL, ok := initBody.PresignedURLs[hash]
	if !ok {
		t.Fatalf("init response: no presigned URL for hash %s", hash)
	}

	// Phase 2: upload.
	putBlob(t, presignedURL, content, hash)

	// Phase 3: commit.
	commitResp := postJSON(t, srv.URL+"/v1/publish/commit", api.CommitRequest{
		IdempotencyKey: idempotencyKey,
	})
	if commitResp.StatusCode != http.StatusOK {
		body := readAll(t, commitResp.Body)
		commitResp.Body.Close()
		t.Fatalf("commit status %d: %s", commitResp.StatusCode, body)
	}
	commitBody := decodeCommitResponse(t, commitResp)

	if commitBody.Slug != initBody.Slug {
		t.Errorf("commit slug=%q, want %q", commitBody.Slug, initBody.Slug)
	}
	if !strings.HasPrefix(commitBody.URL, "https://mdfly.dev/") {
		t.Errorf("commit URL=%q, want prefix https://mdfly.dev/", commitBody.URL)
	}
	if commitBody.ManifestHash == "" {
		t.Error("commit response: empty manifest_hash")
	}
}

// TestPublishInit_idempotent verifies that re-posting the same idempotency_key returns the same slug.
func TestPublishInit_idempotent(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}

	dsn := startPostgres(t)
	env := startMinio(t)
	srv := newTestServer(t, dsn, env, "https://mdfly.dev")

	manifest := api.Manifest{
		Root:  "doc.md",
		Files: []api.ManifestFile{{Path: "doc.md", Hash: contentHash([]byte("hello")), Size: 5}},
	}
	req := api.InitRequest{IdempotencyKey: "6ba7b810-9dad-11d1-80b4-00c04fd430c8", Manifest: manifest}

	r1 := postJSON(t, srv.URL+"/v1/publish/init", req)
	b1 := decodeInitResponse(t, r1)

	r2 := postJSON(t, srv.URL+"/v1/publish/init", req)
	b2 := decodeInitResponse(t, r2)

	if b1.Slug == "" {
		t.Error("idempotent init: first response has empty slug")
	}
	if b1.Slug != b2.Slug {
		t.Errorf("idempotent init: slug changed from %q to %q", b1.Slug, b2.Slug)
	}
}

// TestPublishCommit_missingBlob verifies that commit without uploading blobs returns 409.
func TestPublishCommit_missingBlob(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}

	dsn := startPostgres(t)
	env := startMinio(t)
	srv := newTestServer(t, dsn, env, "https://mdfly.dev")

	content := []byte("missing blob content")
	hash := contentHash(content)
	manifest := api.Manifest{
		Root:  "m.md",
		Files: []api.ManifestFile{{Path: "m.md", Hash: hash, Size: int64(len(content))}},
	}
	idempotencyKey := "6ba7b811-9dad-11d1-80b4-00c04fd430c8"

	r := postJSON(t, srv.URL+"/v1/publish/init", api.InitRequest{
		IdempotencyKey: idempotencyKey, Manifest: manifest,
	})
	r.Body.Close()
	if r.StatusCode != http.StatusOK {
		t.Fatalf("init status %d", r.StatusCode)
	}

	// Commit without uploading — expect 409.
	commitResp := postJSON(t, srv.URL+"/v1/publish/commit", api.CommitRequest{
		IdempotencyKey: idempotencyKey,
	})
	commitResp.Body.Close()
	if commitResp.StatusCode != http.StatusConflict {
		t.Errorf("commit with missing blob: status=%d, want 409", commitResp.StatusCode)
	}
}

// TestPublishCommit_alreadyPublished verifies that committing an already-published doc returns success.
func TestPublishCommit_alreadyPublished(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}

	dsn := startPostgres(t)
	env := startMinio(t)
	srv := newTestServer(t, dsn, env, "https://mdfly.dev")

	content := []byte("published again")
	hash := contentHash(content)
	manifest := api.Manifest{
		Root:  "pub.md",
		Files: []api.ManifestFile{{Path: "pub.md", Hash: hash, Size: int64(len(content))}},
	}
	idempotencyKey := "6ba7b812-9dad-11d1-80b4-00c04fd430c8"

	initR := postJSON(t, srv.URL+"/v1/publish/init", api.InitRequest{
		IdempotencyKey: idempotencyKey, Manifest: manifest,
	})
	initBody := decodeInitResponse(t, initR)
	putBlob(t, initBody.PresignedURLs[hash], content, hash)

	// First commit.
	r1 := postJSON(t, srv.URL+"/v1/publish/commit", api.CommitRequest{IdempotencyKey: idempotencyKey})
	b1 := decodeCommitResponse(t, r1)
	if r1.StatusCode != http.StatusOK {
		t.Fatalf("first commit status %d", r1.StatusCode)
	}

	// Second commit (idempotent).
	r2 := postJSON(t, srv.URL+"/v1/publish/commit", api.CommitRequest{IdempotencyKey: idempotencyKey})
	b2 := decodeCommitResponse(t, r2)
	if r2.StatusCode != http.StatusOK {
		t.Fatalf("second commit status %d", r2.StatusCode)
	}

	if b1.URL != b2.URL {
		t.Errorf("idempotent commit: URL changed from %q to %q", b1.URL, b2.URL)
	}
	if b1.Slug != b2.Slug {
		t.Errorf("idempotent commit: slug changed from %q to %q", b1.Slug, b2.Slug)
	}
}

// TestPublishInit_differentManifestSameKey verifies that re-posting a different manifest
// with the same idempotency_key returns the original slug (no duplicate row).
func TestPublishInit_differentManifestSameKey(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}

	dsn := startPostgres(t)
	env := startMinio(t)
	srv := newTestServer(t, dsn, env, "https://mdfly.dev")

	idempotencyKey := "6ba7b813-9dad-11d1-80b4-00c04fd430c8"
	m1 := api.Manifest{
		Root:  "a.md",
		Files: []api.ManifestFile{{Path: "a.md", Hash: contentHash([]byte("content a")), Size: 9}},
	}
	m2 := api.Manifest{
		Root:  "b.md",
		Files: []api.ManifestFile{{Path: "b.md", Hash: contentHash([]byte("content b")), Size: 9}},
	}

	r1 := postJSON(t, srv.URL+"/v1/publish/init", api.InitRequest{
		IdempotencyKey: idempotencyKey, Manifest: m1,
	})
	b1 := decodeInitResponse(t, r1)

	r2 := postJSON(t, srv.URL+"/v1/publish/init", api.InitRequest{
		IdempotencyKey: idempotencyKey, Manifest: m2,
	})
	b2 := decodeInitResponse(t, r2)

	if b1.Slug != b2.Slug {
		t.Errorf("different manifest, same key: slug changed from %q to %q (must be same)", b1.Slug, b2.Slug)
	}
}

// TestPresignPUT_checksumEnforcement verifies that minio rejects a PUT with the wrong
// x-amz-checksum-sha256 (the presign pin works).
func TestPresignPUT_checksumEnforcement(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}

	env := startMinio(t)

	content := []byte("correct content")
	hash := contentHash(content)

	r2 := r2store.New(r2store.Config{
		Endpoint:        env.endpoint,
		AccessKeyID:     env.accessKey,
		SecretAccessKey: env.secretKey,
		Bucket:          env.bucket,
		PublicBaseURL:   env.endpoint + "/" + env.bucket,
	})

	ctx := context.Background()
	key := r2store.BlobKey(hash, ".md")
	presignedURL, err := r2.PresignPUT(ctx, key, int64(len(content)), hash, 10*time.Minute)
	if err != nil {
		t.Fatalf("PresignPUT: %v", err)
	}

	// PUT with wrong checksum → must be rejected.
	wrongHash := contentHash([]byte("wrong content"))
	req, _ := http.NewRequest(http.MethodPut, presignedURL, bytes.NewReader(content))
	req.ContentLength = int64(len(content))
	req.Header.Set("x-amz-checksum-sha256", hashBase64(wrongHash))
	req.Header.Set("x-amz-sdk-checksum-algorithm", "SHA256")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT with wrong checksum: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode < 400 {
		t.Errorf("PUT with wrong checksum: expected 4xx, got %d", resp.StatusCode)
	}
}
