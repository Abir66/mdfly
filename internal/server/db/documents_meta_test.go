package db_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Abir66/mdfly/internal/server/db"
	"github.com/Abir66/mdfly/internal/server/manifest"
)

func TestPublish_StoresMetadata(t *testing.T) {
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

	client := db.New(pool)

	mfst := manifest.Manifest{
		RootPath: "doc.md",
		FilesByPath: map[string]manifest.ManifestFile{
			"doc.md": {Hash: "abc123", Size: 100},
		},
	}
	doc, err := client.InsertPending(ctx, db.InsertPendingParams{
		Slug:           "test-slug",
		IdempotencyKey: "11111111-2222-3333-4444-555555555555",
		Manifest:       mfst,
	})
	if err != nil {
		t.Fatalf("InsertPending: %v", err)
	}

	ogHash := []byte{0xde, 0xad, 0xbe, 0xef}
	published, err := client.Publish(ctx, doc.IdempotencyKey, time.Now().Add(time.Hour), db.PublishMetaParams{
		Title:       "Test Title",
		Excerpt:     "Test excerpt.",
		OGImageHash: ogHash,
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	if published.Title != "Test Title" {
		t.Errorf("Title: got %q, want %q", published.Title, "Test Title")
	}
	if published.Excerpt != "Test excerpt." {
		t.Errorf("Excerpt: got %q, want %q", published.Excerpt, "Test excerpt.")
	}
	if string(published.OGImageHash) != string(ogHash) {
		t.Errorf("OGImageHash: got %x, want %x", published.OGImageHash, ogHash)
	}

	// GetBySlug should also return metadata.
	fetched, err := client.GetBySlug(ctx, "test-slug")
	if err != nil {
		t.Fatalf("GetBySlug: %v", err)
	}
	if fetched.Title != "Test Title" {
		t.Errorf("fetched Title: got %q, want %q", fetched.Title, "Test Title")
	}
}
