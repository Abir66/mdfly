// Package server assembles the mdfly-server runtime: configuration, infrastructure
// clients, domain services, HTTP routes, and graceful lifecycle. cmd/mdfly-server
// is a thin entrypoint over server.New + App.Run.
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Abir66/mdfly/internal/server/cloudflare"
	"github.com/Abir66/mdfly/internal/server/db"
	"github.com/Abir66/mdfly/internal/server/jobs"
	"github.com/Abir66/mdfly/internal/server/middleware"
	"github.com/Abir66/mdfly/internal/server/ratelimit"
	"github.com/Abir66/mdfly/internal/server/service/document"
	"github.com/Abir66/mdfly/internal/server/service/gc"
	"github.com/Abir66/mdfly/internal/server/service/publish"
	"github.com/Abir66/mdfly/internal/server/service/purge"
	"github.com/Abir66/mdfly/internal/server/service/view"
	"github.com/Abir66/mdfly/internal/server/static"
	"github.com/Abir66/mdfly/internal/server/storage"
	"github.com/Abir66/mdfly/internal/server/upstash"
)

const (
	serverReadHeaderTimeout = 5 * time.Second
	serverReadTimeout       = 30 * time.Second
	serverWriteTimeout      = 60 * time.Second
	serverIdleTimeout       = 120 * time.Second

	dbConnectTimeout = 5 * time.Second
	dbPingTimeout    = 5 * time.Second

	jobLifecycleGC = "lifecycle-gc"
	jobPurgeDrain  = "purge-drain"
)

// App holds the assembled server: infrastructure clients + domain services.
// Build with New, run with Run, release resources with Close.
type App struct {
	cfg      Config
	pool     *pgxpool.Pool
	db       *db.Client
	storage  *storage.Client
	static   *static.Assets
	publish  *publish.Service
	view     *view.Service
	document *document.Service
	jobs     *jobs.Runner
	limiter  middleware.Limiter
}

// New wires the App from cfg: opens the DB pool, pings it, builds the R2 client,
// and constructs domain services. Returns an error on any failure; the caller
// owns shutdown via App.Close.
func New(ctx context.Context, cfg Config) (*App, error) {
	pool, err := openPool(ctx, cfg.Database.URL)
	if err != nil {
		return nil, err
	}

	r2 := storage.New(cfg.R2)
	pg := db.New(pool)
	assets := static.New()

	purger := newPurge(cfg, pg)
	pubSvc := &publish.Service{Db: pg, Storage: r2, BaseURL: cfg.BaseURL}
	docSvc := &document.Service{Db: pg}
	if purger != nil {
		pubSvc.Purge = purger
		docSvc.Purge = purger
	}

	runner := jobs.New(jobs.SystemClock{})
	registerJobs(runner, cfg.Jobs, pg, r2, purger)

	return &App{
		cfg:      cfg,
		pool:     pool,
		db:       pg,
		storage:  r2,
		static:   assets,
		publish:  pubSvc,
		view:     &view.Service{Db: pg, Storage: r2, Static: assets},
		document: docSvc,
		jobs:     runner,
		limiter:  newLimiter(cfg.RateLimit),
	}, nil
}

// newPurge builds the CDN purge service (ADR-0031), or returns nil when
// Cloudflare is unconfigured — local dev still enqueues purges durably, it just
// never contacts the edge, rather than refusing to boot.
func newPurge(cfg Config, pg *db.Client) *purge.Service {
	if cfg.Cloudflare.ZoneID == "" || cfg.Cloudflare.Token == "" {
		slog.Warn("cloudflare not configured, cdn purges will stay queued")
		return nil
	}
	return &purge.Service{
		Db: pg,
		CDN: cloudflare.New(cloudflare.Config{
			ZoneID: cfg.Cloudflare.ZoneID,
			Token:  cfg.Cloudflare.Token,
		}),
		Host: purge.HostFromBaseURL(cfg.BaseURL),
	}
}

// newLimiter builds the write-path rate limiter (ADR-0028), or returns nil when
// Upstash is unconfigured — local dev runs unthrottled rather than refusing to
// boot, and the Cloudflare edge limit still stands in production.
func newLimiter(cfg RateLimitConfig) middleware.Limiter {
	if cfg.URL == "" || cfg.Token == "" {
		slog.Warn("upstash not configured, write paths are unthrottled")
		return nil
	}
	return ratelimit.New(upstash.New(upstash.Config{URL: cfg.URL, Token: cfg.Token}))
}

// registerJobs wires the periodic jobs onto runner (ADR-0029). Intervals and
// grace windows come from cfg, so nothing about the schedule is hardcoded here.
// A nil purger leaves the drain unregistered: with no Cloudflare credentials
// every pass would fail, so queued rows simply wait for a configured process.
func registerJobs(runner *jobs.Runner, cfg JobsConfig, store gc.Store, blobs gc.Blobs, purger *purge.Service) {
	lifecycleGC := &gc.Service{
		Db:              store,
		Blobs:           blobs,
		AbandonGrace:    cfg.AbandonGrace,
		BlobDeleteGrace: cfg.BlobDeleteGrace,
	}
	runner.Register(jobLifecycleGC, cfg.LifecycleGCInterval, lifecycleGC.Job)

	if purger != nil {
		runner.Register(jobPurgeDrain, cfg.PurgeDrainInterval, purger.Job)
	}
}

// Run starts the periodic jobs and the HTTP server, then blocks until ctx is
// cancelled or a SIGINT/SIGTERM arrives. On shutdown signal, drains in-flight
// requests and running jobs within cfg.ShutdownTimeout, then returns.
func (a *App) Run(ctx context.Context) error {
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	server := &http.Server{
		Addr:              a.cfg.Addr,
		Handler:           a.routes(),
		ReadHeaderTimeout: serverReadHeaderTimeout,
		ReadTimeout:       serverReadTimeout,
		WriteTimeout:      serverWriteTimeout,
		IdleTimeout:       serverIdleTimeout,
	}

	// Jobs get a context detached from the signal so a SIGTERM doesn't abort a
	// job mid-transaction; the shutdown timeout bounds the wait instead.
	a.jobs.Start(context.WithoutCancel(ctx))

	serveErr := make(chan error, 1)
	go func() {
		slog.Info("mdfly-server listening", "addr", a.cfg.Addr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
			return
		}
		serveErr <- nil
	}()

	var serveFailure error
	select {
	case serveFailure = <-serveErr:
	case <-ctx.Done():
		slog.Info("shutdown signal received, draining")
	}

	if err := a.shutdown(server); err != nil && serveFailure == nil {
		return err
	}
	return serveFailure
}

// shutdown drains in-flight requests, then stops the job runner, both bounded
// by cfg.ShutdownTimeout. The runner is always stopped, even if draining
// requests fails; the HTTP error wins when both fail.
func (a *App) shutdown(server *http.Server) error {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), a.cfg.ShutdownTimeout)
	defer cancel()

	var serverErr error
	if err := server.Shutdown(shutdownCtx); err != nil {
		serverErr = fmt.Errorf("shutdown: %w", err)
	}
	if err := a.jobs.Stop(shutdownCtx); err != nil && serverErr == nil {
		return fmt.Errorf("stop jobs: %w", err)
	}
	return serverErr
}

// Close releases infrastructure resources. Safe to call after Run returns.
func (a *App) Close() {
	if a.pool != nil {
		a.pool.Close()
	}
}

func openPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	connCtx, cancel := context.WithTimeout(ctx, dbConnectTimeout)
	defer cancel()
	pool, err := pgxpool.New(connCtx, dsn)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	pingCtx, cancelPing := context.WithTimeout(ctx, dbPingTimeout)
	defer cancelPing()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping db: %w", err)
	}
	return pool, nil
}
