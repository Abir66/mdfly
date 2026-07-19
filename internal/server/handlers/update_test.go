package handlers_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Abir66/mdfly/internal/api"
)

// publishForUpdate publishes a single-file bundle bound to editToken and returns
// its slug and the committed manifest hash (the update parent).
func publishForUpdate(t *testing.T, srv *httptest.Server, idempKey, rootPath string, content []byte, editToken string) (slug, manifestHash string) {
	t.Helper()
	bundle := singleFileBundle(rootPath, content)
	initR := postJSON(t, srv.URL+"/v1/publish/init", api.InitRequest{
		IdempotencyKey: idempKey, Bundle: bundle, EditToken: editToken,
	})
	initBody := decodeInitResponse(t, initR)
	putBlob(t, initBody.PresignedURLs[rootPath], content)
	commitR := postJSON(t, srv.URL+"/v1/publish/commit", api.CommitRequest{IdempotencyKey: idempKey})
	commit := decodeCommitResponse(t, commitR)
	return commit.Slug, commit.ManifestHash
}

// bundleFromFiles builds a wire BundleDTO from a path→content map.
func bundleFromFiles(rootPath string, files map[string][]byte) api.BundleDTO {
	dto := api.BundleDTO{RootPath: rootPath}
	for p, c := range files {
		dto.Files = append(dto.Files, api.BundleFileDTO{Path: p, Hash: contentHash(c), Size: int64(len(c))})
	}
	return dto
}

// updateInit POSTs /v1/update/init with the bearer token and returns the response.
func updateInit(t *testing.T, srv *httptest.Server, slug, parent, token string, bundle api.BundleDTO) *http.Response {
	t.Helper()
	return postJSONAuth(t, srv.URL+"/v1/update/init", token, api.UpdateInitRequest{
		TargetSlug: slug, ParentManifestHash: parent, Bundle: bundle,
	})
}

// updateCommit POSTs /v1/update/commit with the bearer token and returns the response.
func updateCommit(t *testing.T, srv *httptest.Server, slug, parent, token string, bundle api.BundleDTO) *http.Response {
	t.Helper()
	return postJSONAuth(t, srv.URL+"/v1/update/commit", token, api.UpdateCommitRequest{
		TargetSlug: slug, ParentManifestHash: parent, Bundle: bundle,
	})
}

func TestUpdate_overwritesInPlaceSameURL(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	dsn := startPostgres(t)
	env := startMinio(t)
	srv := newTestServer(t, dsn, env, "https://mdfly.dev")

	const token = "mftk_secrettoken"
	slug, parent := publishForUpdate(t, srv, "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeef001", "doc.md", []byte("# One\n"), token)
	oldURL := "https://mdfly.dev/" + slug

	newContent := []byte("# Two\n")
	bundle := bundleFromFiles("doc.md", map[string][]byte{"doc.md": newContent})

	initR := updateInit(t, srv, slug, parent, token, bundle)
	initBody := decodeUpdateInit(t, initR)
	if _, ok := initBody.PresignedURLs["doc.md"]; !ok {
		t.Fatalf("changed root should be presigned; got %v", initBody.PresignedURLs)
	}
	putBlob(t, initBody.PresignedURLs["doc.md"], newContent)

	commitR := updateCommit(t, srv, slug, parent, token, bundle)
	commit := decodeUpdateCommit(t, commitR)
	if commit.Slug != slug || commit.URL != oldURL {
		t.Errorf("slug/url changed: slug=%q url=%q, want %q/%q", commit.Slug, commit.URL, slug, oldURL)
	}
	if commit.ManifestHash == parent {
		t.Errorf("manifest hash should change after content change")
	}

	status, body := getString(t, srv.URL+"/"+slug)
	if status != http.StatusOK {
		t.Fatalf("post-update view status=%d, want 200", status)
	}
	if !containsStr(body, "Two") {
		t.Errorf("view should serve new content; body=%q", body)
	}
}

func TestUpdate_initPresignsOnlyChangedBlobs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	dsn := startPostgres(t)
	env := startMinio(t)
	srv := newTestServer(t, dsn, env, "https://mdfly.dev")

	const token = "mftk_secrettoken"
	slug, parent := publishForUpdate(t, srv, "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeef002", "doc.md", []byte("# Same\n"), token)

	// Re-submit the identical manifest: nothing changed, so nothing is presigned.
	same := bundleFromFiles("doc.md", map[string][]byte{"doc.md": []byte("# Same\n")})
	initR := updateInit(t, srv, slug, parent, token, same)
	initBody := decodeUpdateInit(t, initR)
	if len(initBody.PresignedURLs) != 0 {
		t.Errorf("unchanged bundle should presign nothing; got %v", initBody.PresignedURLs)
	}
}

func TestUpdate_wrongOrMissingTokenRejected(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	dsn := startPostgres(t)
	env := startMinio(t)
	srv := newTestServer(t, dsn, env, "https://mdfly.dev")

	const token = "mftk_secrettoken"
	slug, parent := publishForUpdate(t, srv, "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeef003", "doc.md", []byte("# Hi\n"), token)
	bundle := bundleFromFiles("doc.md", map[string][]byte{"doc.md": []byte("# Bye\n")})

	for _, tc := range []struct {
		name, tok string
		want      int
	}{
		{"missing", "", http.StatusUnauthorized},
		{"wrong", "mftk_wrong", http.StatusForbidden},
	} {
		initR := updateInit(t, srv, slug, parent, tc.tok, bundle)
		initR.Body.Close()
		if initR.StatusCode != tc.want {
			t.Errorf("init %s-token status=%d, want %d", tc.name, initR.StatusCode, tc.want)
		}
		commitR := updateCommit(t, srv, slug, parent, tc.tok, bundle)
		commitR.Body.Close()
		if commitR.StatusCode != tc.want {
			t.Errorf("commit %s-token status=%d, want %d", tc.name, commitR.StatusCode, tc.want)
		}
	}
}

func TestUpdate_staleParentConflictsForceOverrides(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	dsn := startPostgres(t)
	env := startMinio(t)
	srv := newTestServer(t, dsn, env, "https://mdfly.dev")

	const token = "mftk_secrettoken"
	slug, parent := publishForUpdate(t, srv, "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeef004", "doc.md", []byte("# V1\n"), token)

	// First update lands, moving manifest_hash off `parent`.
	v2 := []byte("# V2\n")
	b2 := bundleFromFiles("doc.md", map[string][]byte{"doc.md": v2})
	putBlob(t, decodeUpdateInit(t, updateInit(t, srv, slug, parent, token, b2)).PresignedURLs["doc.md"], v2)
	updateCommit(t, srv, slug, parent, token, b2).Body.Close()

	// A second update still sending the original stale parent → 409 on both.
	v3 := []byte("# V3\n")
	b3 := bundleFromFiles("doc.md", map[string][]byte{"doc.md": v3})
	initR := updateInit(t, srv, slug, parent, token, b3)
	initR.Body.Close()
	if initR.StatusCode != http.StatusConflict {
		t.Errorf("stale-parent init status=%d, want 409", initR.StatusCode)
	}
	commitR := updateCommit(t, srv, slug, parent, token, b3)
	commitR.Body.Close()
	if commitR.StatusCode != http.StatusConflict {
		t.Errorf("stale-parent commit status=%d, want 409", commitR.StatusCode)
	}

	// --force (empty parent) skips the check and overwrites.
	putBlob(t, decodeUpdateInit(t, updateInit(t, srv, slug, "", token, b3)).PresignedURLs["doc.md"], v3)
	forced := updateCommit(t, srv, slug, "", token, b3)
	if forced.StatusCode != http.StatusOK {
		t.Fatalf("--force commit status=%d, want 200", forced.StatusCode)
	}
	forced.Body.Close()
	if _, body := getString(t, srv.URL+"/"+slug); !containsStr(body, "V3") {
		t.Errorf("forced update should serve V3; body=%q", body)
	}
}

func TestUpdate_commitRetryIsIdempotent(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	dsn := startPostgres(t)
	env := startMinio(t)
	srv := newTestServer(t, dsn, env, "https://mdfly.dev")

	const token = "mftk_secrettoken"
	slug, parent := publishForUpdate(t, srv, "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeef005", "doc.md", []byte("# A\n"), token)

	newC := []byte("# B\n")
	bundle := bundleFromFiles("doc.md", map[string][]byte{"doc.md": newC})
	putBlob(t, decodeUpdateInit(t, updateInit(t, srv, slug, parent, token, bundle)).PresignedURLs["doc.md"], newC)

	first := decodeUpdateCommit(t, updateCommit(t, srv, slug, parent, token, bundle))
	// Re-run the same commit with the same stale parent: current == new hash, so
	// it is already-applied success, not a conflict.
	retryR := updateCommit(t, srv, slug, parent, token, bundle)
	if retryR.StatusCode != http.StatusOK {
		t.Fatalf("commit retry status=%d, want 200", retryR.StatusCode)
	}
	retry := decodeUpdateCommit(t, retryR)
	if retry.ManifestHash != first.ManifestHash {
		t.Errorf("retry hash=%q, want %q", retry.ManifestHash, first.ManifestHash)
	}
}

func TestUpdate_missingAndGoneSlug(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	dsn := startPostgres(t)
	env := startMinio(t)
	srv := newTestServer(t, dsn, env, "https://mdfly.dev")

	bundle := bundleFromFiles("doc.md", map[string][]byte{"doc.md": []byte("# X\n")})

	// Never existed → 404.
	initR := updateInit(t, srv, "neverexisted99", "", "mftk_whatever", bundle)
	initR.Body.Close()
	if initR.StatusCode != http.StatusNotFound {
		t.Errorf("missing-slug init status=%d, want 404", initR.StatusCode)
	}

	// Deleted → 410.
	const token = "mftk_secrettoken"
	slug, parent := publishForUpdate(t, srv, "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeef006", "doc.md", []byte("# Y\n"), token)
	deleteDoc(t, srv.URL, slug, token).Body.Close()
	goneR := updateInit(t, srv, slug, parent, token, bundle)
	goneR.Body.Close()
	if goneR.StatusCode != http.StatusGone {
		t.Errorf("deleted-slug init status=%d, want 410", goneR.StatusCode)
	}
}
