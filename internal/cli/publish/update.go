package publish

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"path/filepath"
	"time"

	"github.com/Abir66/mdfly/internal/api"
	"github.com/Abir66/mdfly/internal/cli/input"
	"github.com/Abir66/mdfly/internal/cli/localstate"
)

const (
	pathUpdateInit   = "/v1/update/init"
	pathUpdateCommit = "/v1/update/commit"
)

// UpdateOptions is the full input to an update. Slug names the live document;
// Token is its Edit Token; ParentManifestHash is the record's last-known hash
// for the optimistic check ("" when --force). Source/Recursive/Progress mirror
// Options.
type UpdateOptions struct {
	APIBase            string
	StateDir           string
	Slug               string
	Token              string
	ParentManifestHash string
	Source             input.Source
	Recursive          bool
	Progress           io.Writer
}

// RunUpdate orchestrates the update workflow (ADR-0027): build Bundle → limits
// preflight → update/init (presign changed blobs) → parallel upload → update/
// commit → re-persist the Local State record. The URL and slug are unchanged.
func RunUpdate(opts UpdateOptions) (Result, error) {
	bundle, err := bundleForSource(opts.Source, opts.Recursive)
	if err != nil {
		return Result{}, fmt.Errorf("build bundle: %w", err)
	}
	if err := checkBundleLimits(bundle); err != nil {
		return Result{}, err
	}

	ctx := context.Background()
	client := &http.Client{Timeout: httpClientTimeout}

	initResp, err := doUpdateInit(ctx, client, opts, bundle)
	if err != nil {
		return Result{}, fmt.Errorf("update/init: %w", err)
	}
	if err := uploadBlobs(ctx, client, bundle, initResp.PresignedURLs, opts.Progress); err != nil {
		return Result{}, err
	}
	commitResp, err := doUpdateCommit(ctx, client, opts, bundle)
	if err != nil {
		return Result{}, fmt.Errorf("update/commit: %w", err)
	}

	result := Result{
		URL:          commitResp.URL,
		Slug:         commitResp.Slug,
		ManifestHash: commitResp.ManifestHash,
		Tier:         tierAnon,
	}
	if err := persistUpdate(opts.StateDir, opts.Source, opts.Token, bundle, commitResp); err != nil {
		slog.Warn("persist local state failed", "slug", commitResp.Slug, "err", err)
	}
	return result, nil
}

func doUpdateInit(ctx context.Context, client *http.Client, opts UpdateOptions, bundle Bundle) (api.UpdateInitResponse, error) {
	initURL, err := url.JoinPath(opts.APIBase, pathUpdateInit)
	if err != nil {
		return api.UpdateInitResponse{}, fmt.Errorf("build init URL: %w", err)
	}
	return withRetry(ctx, func() (api.UpdateInitResponse, error) {
		return postJSONAuth[api.UpdateInitResponse](ctx, client, initURL, opts.Token, api.UpdateInitRequest{
			TargetSlug:         opts.Slug,
			ParentManifestHash: opts.ParentManifestHash,
			Bundle:             bundle.ToDTO(),
		})
	})
}

func doUpdateCommit(ctx context.Context, client *http.Client, opts UpdateOptions, bundle Bundle) (api.UpdateCommitResponse, error) {
	commitURL, err := url.JoinPath(opts.APIBase, pathUpdateCommit)
	if err != nil {
		return api.UpdateCommitResponse{}, fmt.Errorf("build commit URL: %w", err)
	}
	return withRetry(ctx, func() (api.UpdateCommitResponse, error) {
		return postJSONAuth[api.UpdateCommitResponse](ctx, client, commitURL, opts.Token, api.UpdateCommitRequest{
			TargetSlug:         opts.Slug,
			ParentManifestHash: opts.ParentManifestHash,
			Bundle:             bundle.ToDTO(),
		})
	})
}

// persistUpdate rewrites the Local State record after a successful update,
// preserving CreatedAt and the stored Edit Token while refreshing the manifest
// hash, size, counters, and updated_at. A file update re-attaches the current
// source path (rename resilience, ADR-0026); a text update clears it.
func persistUpdate(stateDir string, src input.Source, token string, bundle Bundle, commit api.UpdateCommitResponse) error {
	store := localstate.New(stateDir)
	ledger, err := store.Load()
	if err != nil {
		return err
	}
	rec, existed := ledger.Get(commit.Slug)
	now := time.Now().UTC()
	if !existed {
		rec = localstate.Record{Slug: commit.Slug, CreatedAt: now, Tier: tierAnon, HasToken: token != ""}
	}

	rec.URL = commit.URL
	rec.ManifestHash = commit.ManifestHash
	rec.Size = bundle.TotalBytes()
	rec.FileCount = len(bundle.FilesByPath)
	rec.UpdatedAt = now
	if src.Kind == input.KindFile {
		rec.Source = localstate.SourceFile
		if abs, err := filepath.Abs(src.Path); err == nil {
			rec.Path = &abs
		} else {
			rec.Path = nil
		}
	} else {
		rec.Source = localstate.SourceText
		rec.Path = nil
	}
	return store.Upsert(rec)
}
