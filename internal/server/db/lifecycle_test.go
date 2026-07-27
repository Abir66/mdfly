package db_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Abir66/mdfly/internal/server/db"
)

// lifecycleDB starts Postgres, migrates up, and returns a db.Client plus the
// raw pool for seeding rows the public API cannot create (owned documents,
// backdated timestamps, terminal statuses).
func lifecycleDB(t *testing.T) (*db.Client, *pgxpool.Pool) {
	t.Helper()

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

	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)

	return db.New(pool), pool
}

type seedRow struct {
	slug      string
	status    string
	ownerID   *int64
	createdAt *time.Time
	expiresAt *time.Time
	deletedAt *time.Time
}

func seedDocument(t *testing.T, pool *pgxpool.Pool, r seedRow) {
	t.Helper()
	const q = `
INSERT INTO documents (
	slug, idempotency_key, status, owner_user_id,
	manifest, manifest_hash, bytes_total, file_count,
	created_at, expires_at, deleted_at
) VALUES ($1, gen_random_uuid(), $2, $3, '{}'::jsonb, '\x00'::bytea, 0, 0,
          COALESCE($4::timestamptz, now()), $5, $6)`

	if _, err := pool.Exec(context.Background(), q,
		r.slug, r.status, r.ownerID, r.createdAt, r.expiresAt, r.deletedAt); err != nil {
		t.Fatalf("seed %s: %v", r.slug, err)
	}
}

func statusOf(t *testing.T, pool *pgxpool.Pool, slug string) string {
	t.Helper()
	var status string
	if err := pool.QueryRow(context.Background(),
		`SELECT status FROM documents WHERE slug = $1`, slug).Scan(&status); err != nil {
		t.Fatalf("read status of %s: %v", slug, err)
	}
	return status
}

// TestMarkExpired covers the anon-expiry transition: only published rows whose
// expires_at has passed move, Owned Documents (no expires_at) never do, the
// batch is bounded, and a repeat pass is a no-op.
func TestMarkExpired(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}

	client, pool := lifecycleDB(t)
	ctx := context.Background()
	now := time.Now()
	past := now.Add(-time.Hour)

	seedDocument(t, pool, seedRow{slug: "lapsed-a", status: "published", expiresAt: &past})
	seedDocument(t, pool, seedRow{slug: "lapsed-b", status: "published", expiresAt: new(now.Add(-2 * time.Hour))})
	seedDocument(t, pool, seedRow{slug: "live", status: "published", expiresAt: new(now.Add(time.Hour))})
	seedDocument(t, pool, seedRow{slug: "owned", status: "published", ownerID: new(int64(1))})
	seedDocument(t, pool, seedRow{slug: "already-deleted", status: "deleted", expiresAt: &past, deletedAt: &past})

	moved, err := client.MarkExpired(ctx, now, 1)
	if err != nil {
		t.Fatalf("MarkExpired: %v", err)
	}
	if moved != 1 {
		t.Fatalf("first pass moved %d rows, want 1 (batch bound)", moved)
	}

	moved, err = client.MarkExpired(ctx, now, 10)
	if err != nil {
		t.Fatalf("MarkExpired: %v", err)
	}
	if moved != 1 {
		t.Fatalf("second pass moved %d rows, want the 1 remaining lapsed row", moved)
	}

	for _, slug := range []string{"lapsed-a", "lapsed-b"} {
		if got := statusOf(t, pool, slug); got != "expired" {
			t.Errorf("%s status = %q, want expired", slug, got)
		}
	}
	if got := statusOf(t, pool, "live"); got != "published" {
		t.Errorf("unexpired row status = %q, want published", got)
	}
	if got := statusOf(t, pool, "owned"); got != "published" {
		t.Errorf("owned row status = %q, want published", got)
	}
	if got := statusOf(t, pool, "already-deleted"); got != "deleted" {
		t.Errorf("deleted row status = %q, want deleted", got)
	}

	moved, err = client.MarkExpired(ctx, now, 10)
	if err != nil {
		t.Fatalf("MarkExpired: %v", err)
	}
	if moved != 0 {
		t.Errorf("idempotent pass moved %d rows, want 0", moved)
	}
}

// TestMarkAbandoned covers the pending-abandon transition: pending rows older
// than the cutoff move, younger ones stay, and a repeat pass is a no-op.
func TestMarkAbandoned(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}

	client, pool := lifecycleDB(t)
	ctx := context.Background()
	now := time.Now()
	cutoff := now.Add(-time.Hour)

	seedDocument(t, pool, seedRow{slug: "stale-a", status: "pending", createdAt: new(now.Add(-3 * time.Hour))})
	seedDocument(t, pool, seedRow{slug: "stale-b", status: "pending", createdAt: new(now.Add(-2 * time.Hour))})
	seedDocument(t, pool, seedRow{slug: "fresh", status: "pending", createdAt: new(now.Add(-time.Minute))})
	seedDocument(t, pool, seedRow{slug: "live-doc", status: "published", createdAt: new(now.Add(-3 * time.Hour))})

	moved, err := client.MarkAbandoned(ctx, cutoff, 1)
	if err != nil {
		t.Fatalf("MarkAbandoned: %v", err)
	}
	if moved != 1 {
		t.Fatalf("first pass moved %d rows, want 1 (batch bound)", moved)
	}

	moved, err = client.MarkAbandoned(ctx, cutoff, 10)
	if err != nil {
		t.Fatalf("MarkAbandoned: %v", err)
	}
	if moved != 1 {
		t.Fatalf("second pass moved %d rows, want the 1 remaining stale row", moved)
	}

	for _, slug := range []string{"stale-a", "stale-b"} {
		if got := statusOf(t, pool, slug); got != "abandoned" {
			t.Errorf("%s status = %q, want abandoned", slug, got)
		}
	}
	if got := statusOf(t, pool, "fresh"); got != "pending" {
		t.Errorf("within-grace row status = %q, want pending", got)
	}
	if got := statusOf(t, pool, "live-doc"); got != "published" {
		t.Errorf("published row status = %q, want published", got)
	}

	moved, err = client.MarkAbandoned(ctx, cutoff, 10)
	if err != nil {
		t.Fatalf("MarkAbandoned: %v", err)
	}
	if moved != 0 {
		t.Errorf("idempotent pass moved %d rows, want 0", moved)
	}
}
