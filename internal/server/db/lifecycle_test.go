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
	slug           string
	status         string
	ownerID        *int64
	createdAt      *time.Time
	updatedAt      *time.Time
	expiresAt      *time.Time
	deletedAt      *time.Time
	blobsDeletedAt *time.Time
}

func seedDocument(t *testing.T, pool *pgxpool.Pool, r seedRow) {
	t.Helper()
	const q = `
INSERT INTO documents (
	slug, idempotency_key, status, owner_user_id,
	manifest, manifest_hash, bytes_total, file_count,
	created_at, updated_at, expires_at, deleted_at, blobs_deleted_at
) VALUES ($1, gen_random_uuid(), $2, $3, '{}'::jsonb, '\x00'::bytea, 0, 0,
          COALESCE($4::timestamptz, now()), COALESCE($5::timestamptz, now()), $6, $7, $8)`

	if _, err := pool.Exec(context.Background(), q,
		r.slug, r.status, r.ownerID, r.createdAt, r.updatedAt,
		r.expiresAt, r.deletedAt, r.blobsDeletedAt); err != nil {
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

// TestListBlobGCCandidates covers the blob-deletion work-list: deleted/expired
// rows appear only once past the grace, abandoned rows appear at once, live rows
// and already-stamped rows never appear, and the batch is bounded.
func TestListBlobGCCandidates(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}

	client, pool := lifecycleDB(t)
	ctx := context.Background()
	now := time.Now()
	graced := now.Add(-24 * time.Hour)

	seedDocument(t, pool, seedRow{slug: "old-deleted", status: "deleted",
		updatedAt: new(now.Add(-48 * time.Hour)), deletedAt: new(now.Add(-48 * time.Hour))})
	seedDocument(t, pool, seedRow{slug: "old-expired", status: "expired",
		updatedAt: new(now.Add(-36 * time.Hour))})
	seedDocument(t, pool, seedRow{slug: "fresh-deleted", status: "deleted",
		updatedAt: new(now.Add(-time.Hour)), deletedAt: new(now.Add(-time.Hour))})
	seedDocument(t, pool, seedRow{slug: "abandoned-now", status: "abandoned", updatedAt: new(now)})
	seedDocument(t, pool, seedRow{slug: "live", status: "published"})
	seedDocument(t, pool, seedRow{slug: "already-swept", status: "deleted",
		updatedAt: new(now.Add(-48 * time.Hour)), blobsDeletedAt: new(now.Add(-47 * time.Hour))})

	got, err := client.ListBlobGCCandidates(ctx, graced, 10)
	if err != nil {
		t.Fatalf("ListBlobGCCandidates: %v", err)
	}
	wantSlugs := map[string]db.DocumentStatus{
		"old-deleted":   db.StatusDeleted,
		"old-expired":   db.StatusExpired,
		"abandoned-now": db.StatusAbandoned,
	}
	if len(got) != len(wantSlugs) {
		t.Fatalf("candidates = %+v, want %d rows %v", got, len(wantSlugs), wantSlugs)
	}
	for _, c := range got {
		status, ok := wantSlugs[c.Slug]
		if !ok {
			t.Errorf("unexpected candidate %s", c.Slug)
			continue
		}
		if c.Status != status {
			t.Errorf("%s status = %q, want %q", c.Slug, c.Status, status)
		}
		if c.ID == 0 {
			t.Errorf("%s has zero ID", c.Slug)
		}
	}

	// Oldest transition first, so a backlog drains in age order.
	if got[0].Slug != "old-deleted" {
		t.Errorf("first candidate = %s, want old-deleted (oldest updated_at)", got[0].Slug)
	}

	bounded, err := client.ListBlobGCCandidates(ctx, graced, 2)
	if err != nil {
		t.Fatalf("ListBlobGCCandidates bounded: %v", err)
	}
	if len(bounded) != 2 {
		t.Errorf("bounded list returned %d rows, want 2", len(bounded))
	}
}

// TestSetBlobsDeletedAt stamps a row out of the work-list and stays idempotent
// on a repeat call, without disturbing updated_at (the grace ordering column).
func TestSetBlobsDeletedAt(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}

	client, pool := lifecycleDB(t)
	ctx := context.Background()
	now := time.Now()
	updated := now.Add(-48 * time.Hour)

	seedDocument(t, pool, seedRow{slug: "swept", status: "deleted",
		updatedAt: new(updated), deletedAt: new(updated)})

	candidates, err := client.ListBlobGCCandidates(ctx, now.Add(-24*time.Hour), 10)
	if err != nil || len(candidates) != 1 {
		t.Fatalf("list before stamp: %+v, err %v", candidates, err)
	}

	if err := client.SetBlobsDeletedAt(ctx, candidates[0].ID); err != nil {
		t.Fatalf("SetBlobsDeletedAt: %v", err)
	}
	if err := client.SetBlobsDeletedAt(ctx, candidates[0].ID); err != nil {
		t.Fatalf("repeat SetBlobsDeletedAt: %v", err)
	}

	after, err := client.ListBlobGCCandidates(ctx, now.Add(-24*time.Hour), 10)
	if err != nil {
		t.Fatalf("list after stamp: %v", err)
	}
	if len(after) != 0 {
		t.Errorf("stamped row still listed: %+v", after)
	}

	var stamp, updatedAt time.Time
	if err := pool.QueryRow(ctx,
		`SELECT blobs_deleted_at, updated_at FROM documents WHERE slug = 'swept'`).
		Scan(&stamp, &updatedAt); err != nil {
		t.Fatalf("read stamps: %v", err)
	}
	if stamp.IsZero() {
		t.Error("blobs_deleted_at not stamped")
	}
	if updatedAt.Sub(updated).Abs() > time.Second {
		t.Errorf("updated_at = %s, want it untouched at %s", updatedAt, updated)
	}
}
