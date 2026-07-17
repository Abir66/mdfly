package publish

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/Abir66/mdfly/internal/api"
	"github.com/Abir66/mdfly/internal/cli/input"
	"github.com/google/uuid"
)

const (
	pathInit   = "/v1/publish/init"
	pathCommit = "/v1/publish/commit"

	httpClientTimeout = 30 * time.Second

	// tierAnon is the tier recorded for every anonymous publish.
	tierAnon = "anon"
)

// Options is the full input to a publish. Source is the resolved content origin
// (S30 input); Recursive follows linked .md files transitively (S31 walk).
// Progress, when non-nil, receives an "uploaded N/M" counter during the upload
// phase (the caller passes nil to suppress it, e.g. under -q or a non-TTY).
type Options struct {
	APIBase   string
	StateDir  string
	Source    input.Source
	Recursive bool
	Progress  io.Writer
}

// Result is the outcome of a successful publish, carrying every field the
// caller needs for stdout, --json, and the persisted Local State record.
type Result struct {
	URL          string
	Slug         string
	ManifestHash string
	Tier         string
}

// Run orchestrates the publish workflow: build Bundle → limits preflight →
// init → parallel blob upload → commit → persist Local State + credentials.
func Run(opts Options) (Result, error) {
	bundle, err := bundleForSource(opts.Source, opts.Recursive)
	if err != nil {
		return Result{}, fmt.Errorf("build bundle: %w", err)
	}
	if err := checkBundleLimits(bundle); err != nil {
		return Result{}, err
	}

	idempotencyKey, err := uuid.NewV7()
	if err != nil {
		return Result{}, fmt.Errorf("generate idempotency key: %w", err)
	}
	editToken, err := newEditToken()
	if err != nil {
		return Result{}, fmt.Errorf("generate edit token: %w", err)
	}

	ctx := context.Background()
	client := &http.Client{Timeout: httpClientTimeout}
	key := idempotencyKey.String()

	initResp, err := doInit(ctx, client, opts.APIBase, key, bundle, editToken)
	if err != nil {
		return Result{}, fmt.Errorf("publish/init: %w", err)
	}
	if err := uploadBlobs(ctx, client, bundle, initResp.PresignedURLs, opts.Progress); err != nil {
		return Result{}, err
	}
	commitResp, err := doCommit(ctx, client, opts.APIBase, key)
	if err != nil {
		return Result{}, fmt.Errorf("publish/commit: %w", err)
	}

	result := Result{
		URL:          commitResp.URL,
		Slug:         commitResp.Slug,
		ManifestHash: commitResp.ManifestHash,
		Tier:         tierAnon,
	}
	// The document is already published; a local-state write failure is a
	// recoverable anomaly (list/update lose this entry) — warn, do not fail.
	if err := persistPublish(opts.StateDir, opts.Source, bundle, commitResp, editToken); err != nil {
		slog.Warn("persist local state failed", "slug", commitResp.Slug, "err", err)
	}
	return result, nil
}

// doInit posts the init request under a retry, reusing key across attempts.
func doInit(ctx context.Context, client *http.Client, apiBase, key string, bundle Bundle, editToken string) (api.InitResponse, error) {
	initURL, err := url.JoinPath(apiBase, pathInit)
	if err != nil {
		return api.InitResponse{}, fmt.Errorf("build init URL: %w", err)
	}
	return withRetry(ctx, func() (api.InitResponse, error) {
		return postJSON[api.InitResponse](ctx, client, initURL, api.InitRequest{
			IdempotencyKey: key,
			Bundle:         bundle.ToDTO(),
			EditToken:      editToken,
		})
	})
}

// doCommit posts the commit request under a retry, reusing key across attempts.
func doCommit(ctx context.Context, client *http.Client, apiBase, key string) (api.CommitResponse, error) {
	commitURL, err := url.JoinPath(apiBase, pathCommit)
	if err != nil {
		return api.CommitResponse{}, fmt.Errorf("build commit URL: %w", err)
	}
	return withRetry(ctx, func() (api.CommitResponse, error) {
		return postJSON[api.CommitResponse](ctx, client, commitURL, api.CommitRequest{
			IdempotencyKey: key,
		})
	})
}
