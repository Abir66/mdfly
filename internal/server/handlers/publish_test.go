package handlers_test

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Abir66/mdfly/internal/api"
	"github.com/Abir66/mdfly/internal/server/storage"
)

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

	putBlob(t, presignedURL, content, hash)

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

	commitResp := postJSON(t, srv.URL+"/v1/publish/commit", api.CommitRequest{
		IdempotencyKey: idempotencyKey,
	})
	commitResp.Body.Close()
	if commitResp.StatusCode != http.StatusConflict {
		t.Errorf("commit with missing blob: status=%d, want 409", commitResp.StatusCode)
	}
}

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

	r1 := postJSON(t, srv.URL+"/v1/publish/commit", api.CommitRequest{IdempotencyKey: idempotencyKey})
	b1 := decodeCommitResponse(t, r1)
	if r1.StatusCode != http.StatusOK {
		t.Fatalf("first commit status %d", r1.StatusCode)
	}

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

func TestPresignPUT_checksumEnforcement(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}

	env := startMinio(t)

	content := []byte("correct content")
	hash := contentHash(content)

	r2 := storage.New(storage.Config{
		Endpoint:        env.endpoint,
		AccessKeyID:     env.accessKey,
		SecretAccessKey: env.secretKey,
		Bucket:          env.bucket,
		PublicBaseURL:   env.endpoint + "/" + env.bucket,
	})

	ctx := context.Background()
	key := storage.BlobKey(hash, ".md")
	presignedURL, err := r2.PresignPUT(ctx, key, int64(len(content)), hash, 10*time.Minute)
	if err != nil {
		t.Fatalf("PresignPUT: %v", err)
	}

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
