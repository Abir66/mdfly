package db_test

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func migrationsDir() string {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		panic("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(filename), "../../../migrations")
}

func startPostgres(t *testing.T) (dsn string, cleanup func()) {
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
			WithOccurrence(2).
			WithStartupTimeout(60 * time.Second),
	}
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}

	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("get container host: %v", err)
	}
	port, err := container.MappedPort(ctx, "5432/tcp")
	if err != nil {
		t.Fatalf("get container port: %v", err)
	}
	dsn = fmt.Sprintf("postgres://mdfly:secret@%s:%s/mdfly?sslmode=disable", host, port.Port())
	return dsn, func() { container.Terminate(ctx) }
}

// TestDocumentsMigrationRoundTrip verifies migrate-up creates the documents
// table with its three partial indexes, and migrate-down leaves an empty schema.
func TestDocumentsMigrationRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}

	dsn, cleanup := startPostgres(t)
	t.Cleanup(cleanup)

	migrateDSN := strings.Replace(dsn, "postgres://", "pgx5://", 1)
	m, err := migrate.New("file://"+migrationsDir(), migrateDSN)
	if err != nil {
		t.Fatalf("create migrator: %v", err)
	}
	defer m.Close()

	if err := m.Up(); err != nil {
		t.Fatalf("migrate up: %v", err)
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	for _, idx := range []string{
		"documents_pending_gc_idx",
		"documents_anon_expiry_idx",
		"documents_owner_idx",
	} {
		var found bool
		if err := pool.QueryRow(ctx,
			`SELECT EXISTS(
				SELECT 1 FROM pg_indexes
				WHERE tablename = 'documents' AND indexname = $1
			)`, idx).Scan(&found); err != nil {
			t.Fatalf("check index %s: %v", idx, err)
		}
		if !found {
			t.Errorf("index %s not found after migrate up", idx)
		}
	}

	checkExplainUsesIndex(t, pool,
		"documents_pending_gc_idx",
		"SELECT id FROM documents WHERE status = 'pending' AND created_at < $1",
		"2099-01-01")

	checkExplainUsesIndex(t, pool,
		"documents_anon_expiry_idx",
		"SELECT id FROM documents WHERE expires_at < $1 AND deleted_at IS NULL",
		"2099-01-01")

	if err := m.Down(); err != nil {
		t.Fatalf("migrate down: %v", err)
	}

	var tableExists bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS(
			SELECT 1 FROM information_schema.tables
			WHERE table_schema = 'public' AND table_name = 'documents'
		)`).Scan(&tableExists); err != nil {
		t.Fatalf("check table after down: %v", err)
	}
	if tableExists {
		t.Error("documents table still exists after migrate down")
	}
}

// TestLifecycleStatusMigration verifies migration 0003: the status CHECK admits
// all five lifecycle values, blobs_deleted_at and its partial blob-GC index
// exist, and stepping down reverts the CHECK after relocating the rows that the
// narrower vocabulary can no longer hold.
func TestLifecycleStatusMigration(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}

	dsn, cleanup := startPostgres(t)
	t.Cleanup(cleanup)

	migrateDSN := strings.Replace(dsn, "postgres://", "pgx5://", 1)
	m, err := migrate.New("file://"+migrationsDir(), migrateDSN)
	if err != nil {
		t.Fatalf("create migrator: %v", err)
	}
	defer m.Close()

	if err := m.Up(); err != nil {
		t.Fatalf("migrate up: %v", err)
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	for _, status := range []string{"pending", "published", "deleted", "expired", "abandoned"} {
		if err := insertDocument(ctx, pool, status+"-slug", status); err != nil {
			t.Fatalf("insert %s row: %v", status, err)
		}
	}

	var hasColumn bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS(
			SELECT 1 FROM information_schema.columns
			WHERE table_name = 'documents' AND column_name = 'blobs_deleted_at'
		)`).Scan(&hasColumn); err != nil {
		t.Fatalf("check blobs_deleted_at: %v", err)
	}
	if !hasColumn {
		t.Error("blobs_deleted_at column not found after migrate up")
	}

	checkExplainUsesIndex(t, pool,
		"documents_blob_gc_idx",
		`SELECT id FROM documents
		 WHERE blobs_deleted_at IS NULL
		   AND status IN ('deleted', 'expired', 'abandoned')
		   AND updated_at < $1`,
		"2099-01-01")

	if err := m.Steps(-1); err != nil {
		t.Fatalf("migrate down one step: %v", err)
	}

	var status string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM documents WHERE slug = 'expired-slug'`).Scan(&status); err != nil {
		t.Fatalf("read expired row after down: %v", err)
	}
	if status != "deleted" {
		t.Errorf("expired row status after down = %q, want deleted", status)
	}
	if err := pool.QueryRow(ctx,
		`SELECT status FROM documents WHERE slug = 'abandoned-slug'`).Scan(&status); err != nil {
		t.Fatalf("read abandoned row after down: %v", err)
	}
	if status != "pending" {
		t.Errorf("abandoned row status after down = %q, want pending", status)
	}

	if err := insertDocument(ctx, pool, "post-down-slug", "expired"); err == nil {
		t.Error("insert with status 'expired' succeeded after down; CHECK not reverted")
	}
}

// insertDocument inserts a minimal documents row with the given slug and status.
func insertDocument(ctx context.Context, pool *pgxpool.Pool, slug, status string) error {
	_, err := pool.Exec(ctx,
		`INSERT INTO documents (slug, idempotency_key, status, manifest, manifest_hash, bytes_total, file_count)
		 VALUES ($1, gen_random_uuid(), $2, '{}'::jsonb, '\x00'::bytea, 0, 0)`,
		slug, status)
	return err
}

func checkExplainUsesIndex(t *testing.T, pool *pgxpool.Pool, indexName, query string, args ...any) {
	t.Helper()
	ctx := context.Background()

	// Acquire a single connection so SET affects the subsequent EXPLAIN.
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire conn: %v", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, "SET enable_seqscan = off"); err != nil {
		t.Fatalf("disable seqscan: %v", err)
	}
	defer conn.Exec(ctx, "SET enable_seqscan = on") //nolint:errcheck

	rows, err := conn.Query(ctx, "EXPLAIN "+query, args...)
	if err != nil {
		t.Fatalf("EXPLAIN %s: %v", indexName, err)
	}
	defer rows.Close()

	var plan strings.Builder
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatalf("scan explain: %v", err)
		}
		plan.WriteString(line)
		plan.WriteString("\n")
	}

	if !strings.Contains(plan.String(), indexName) {
		t.Errorf("EXPLAIN does not mention %s:\n%s", indexName, plan.String())
	}
}
