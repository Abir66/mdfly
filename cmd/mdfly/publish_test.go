package main_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
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
	"github.com/Abir66/mdfly/internal/server/storage"
)

func projectRoot() string {
	_, filename, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(filename), "../..")
}

func migrationsDir() string {
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

	publishDeps := handlers.PublishDeps{PG: pg, R2: r2, BaseURL: srv.URL}
	viewDeps := handlers.ViewDeps{PG: pg, R2: r2}
	mux.HandleFunc("POST /v1/publish/init", handlers.Init(publishDeps))
	mux.HandleFunc("POST /v1/publish/commit", handlers.Commit(publishDeps))
	mux.HandleFunc("GET /{slug}", handlers.View(viewDeps))

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

// Ensure api.CommitResponse has a URL field — compile-time type check.
var _ = api.CommitResponse{URL: ""}
