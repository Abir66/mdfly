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

// TestPartialIndexesServeGCPredicates asserts the planner actually reaches every
// partial index through the predicate its owning job issues. A partial index
// whose WHERE clause drifts from that predicate still satisfies the functional
// tests — it just degrades to a seq scan — so nothing else catches the drift.
func TestPartialIndexesServeGCPredicates(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}

	dsn, cleanup := startPostgres(t)
	t.Cleanup(cleanup)

	m, err := migrate.New("file://"+migrationsDir(), strings.Replace(dsn, "postgres://", "pgx5://", 1))
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

	checkExplainUsesIndex(t, pool,
		"documents_pending_gc_idx",
		"SELECT id FROM documents WHERE status = 'pending' AND created_at < $1",
		"2099-01-01")

	checkExplainUsesIndex(t, pool,
		"documents_anon_expiry_idx",
		"SELECT id FROM documents WHERE expires_at < $1 AND deleted_at IS NULL",
		"2099-01-01")

	checkExplainUsesIndex(t, pool,
		"documents_owner_idx",
		`SELECT id FROM documents
		 WHERE owner_user_id = $1 AND deleted_at IS NULL
		 ORDER BY updated_at DESC`,
		1)

	checkExplainUsesIndex(t, pool,
		"documents_blob_gc_idx",
		`SELECT id FROM documents
		 WHERE blobs_deleted_at IS NULL
		   AND status IN ('deleted', 'expired', 'abandoned')
		   AND updated_at < $1`,
		"2099-01-01")

	checkExplainUsesIndex(t, pool,
		"purge_queue_due_idx",
		"SELECT slug FROM purge_queue WHERE next_attempt_at <= $1 ORDER BY next_attempt_at",
		"2099-01-01")
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
