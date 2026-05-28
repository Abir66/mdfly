package handlers_test

import (
	"bytes"
	"context"
	"crypto/sha256"
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
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/Abir66/mdfly/internal/api"
	"github.com/Abir66/mdfly/internal/server/db"
	"github.com/Abir66/mdfly/internal/server/handlers"
	"github.com/Abir66/mdfly/internal/server/publish"
	"github.com/Abir66/mdfly/internal/server/storage"
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

	migrateDSN := strings.Replace(dsn, "postgres://", "pgx5://", 1)
	m, err := migrate.New("file://"+migrationsDir(), migrateDSN)
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

// ── test server ───────────────────────────────────────────────────────────────

// newTestServer builds a full httptest.Server with all handlers registered.
func newTestServer(t *testing.T, dsn string, env minioEnv, baseURL string) *httptest.Server {
	t.Helper()

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

	pubSvc := &publish.Service{Db: pg, Storage: r2, BaseURL: baseURL}
	viewDeps := handlers.ViewDeps{PG: pg, R2: r2}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/publish/init", handlers.Init(pubSvc))
	mux.HandleFunc("POST /v1/publish/commit", handlers.Commit(pubSvc))
	mux.HandleFunc("GET /{slug}", handlers.View(viewDeps))

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

func contentHash(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// singleFileBundle builds a one-file wire BundleDTO whose root is that file.
func singleFileBundle(path string, content []byte) api.BundleDTO {
	hash := contentHash(content)
	return api.BundleDTO{
		RootPath: path,
		Files: []api.BundleFileDTO{
			{Path: path, Hash: hash, Size: int64(len(content))},
		},
	}
}

// putBlob uploads via a presigned URL the way the real CLI does: body and
// Content-Length only. The SHA256 checksum lives in the signed query of the URL,
// so no checksum header is sent.
func putBlob(t *testing.T, presignedURL string, content []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, presignedURL, bytes.NewReader(content))
	if err != nil {
		t.Fatalf("build PUT request: %v", err)
	}
	req.ContentLength = int64(len(content))

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("PUT blob: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("PUT blob status %d: %s", resp.StatusCode, body)
	}
}
