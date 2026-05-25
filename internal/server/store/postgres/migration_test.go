package postgres_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	_ "github.com/lib/pq"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func migrationsDir() string {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		panic("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(filename), "../../../../migrations")
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

	m, err := migrate.New("file://"+migrationsDir(), dsn)
	if err != nil {
		t.Fatalf("create migrator: %v", err)
	}
	defer m.Close()

	if err := m.Up(); err != nil {
		t.Fatalf("migrate up: %v", err)
	}

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	// All three partial indexes must exist after migrate-up.
	for _, idx := range []string{
		"documents_pending_gc_idx",
		"documents_anon_expiry_idx",
		"documents_owner_idx",
	} {
		var found bool
		if err := db.QueryRowContext(context.Background(),
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

	// pending-GC query must use documents_pending_gc_idx.
	checkExplainUsesIndex(t, db,
		"documents_pending_gc_idx",
		"SELECT id FROM documents WHERE status = 'pending' AND created_at < $1",
		"2099-01-01")

	// anon-expiry query must use documents_anon_expiry_idx.
	checkExplainUsesIndex(t, db,
		"documents_anon_expiry_idx",
		"SELECT id FROM documents WHERE expires_at < $1 AND deleted_at IS NULL",
		"2099-01-01")

	if err := m.Down(); err != nil {
		t.Fatalf("migrate down: %v", err)
	}

	var tableExists bool
	if err := db.QueryRowContext(context.Background(),
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

func checkExplainUsesIndex(t *testing.T, db *sql.DB, indexName, query string, args ...any) {
	t.Helper()
	ctx := context.Background()

	if _, err := db.ExecContext(ctx, "SET enable_seqscan = off"); err != nil {
		t.Fatalf("disable seqscan: %v", err)
	}
	defer db.ExecContext(ctx, "SET enable_seqscan = on") //nolint:errcheck

	rows, err := db.QueryContext(ctx, "EXPLAIN "+query, args...)
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
