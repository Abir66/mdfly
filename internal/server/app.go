// Package server assembles the mdfly-server runtime: configuration, infrastructure
// clients, domain services, HTTP routes, and graceful lifecycle. It assembles in
// one of two Roles — the process that serves HTTP or the one that ticks (ADR-0003)
// — and cmd/mdfly-server is a thin entrypoint over server.New + App.Run.
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
	"github.com/Abir66/mdfly/internal/server/redis"
	"github.com/Abir66/mdfly/internal/server/service/document"
	"github.com/Abir66/mdfly/internal/server/service/gc"
	"github.com/Abir66/mdfly/internal/server/service/publish"
	"github.com/Abir66/mdfly/internal/server/service/purge"
	"github.com/Abir66/mdfly/internal/server/service/raw"
	"github.com/Abir66/mdfly/internal/server/service/view"
	"github.com/Abir66/mdfly/internal/server/static"
	"github.com/Abir66/mdfly/internal/server/storage"
)

const (
	serverReadHeaderTimeout = 5 * time.Second
	serverReadTimeout       = 30 * time.Second
	// The longest a request may run, and the first link of the deploy timeout
	// chain (ADR-0015): request ceiling < SHUTDOWN_TIMEOUT < stop_grace_period,
	// the other two set per web slot in deploy/compose.yaml. Keeping it under the
	// drain budget is what makes a swap complete every request it accepted rather
	// than severing the slow ones.
	serverWriteTimeout = 30 * time.Second
	serverIdleTimeout  = 120 * time.Second

	dbConnectTimeout = 5 * time.Second
	dbPingTimeout    = 5 * time.Second

	jobLifecycleGC = "lifecycle-gc"
	jobPurgeDrain  = "purge-drain"
)

// Role selects which half of the binary an App is (ADR-0003): the process that
// serves HTTP, or the single process that ticks. One binary, two subcommands, so
// the two can never disagree about the schema or the query layer.
type Role string

const (
	RoleServe Role = "serve"
	RoleJobs  Role = "jobs"
)

// ParseRole maps a subcommand to its Role. There is deliberately no default: a
// mistyped or missing subcommand must fail loudly rather than silently starting
// the wrong process.
func ParseRole(name string) (Role, error) {
	switch role := Role(name); role {
	case RoleServe, RoleJobs:
		return role, nil
	default:
		return "", fmt.Errorf("unknown subcommand %q, want %q or %q", name, RoleServe, RoleJobs)
	}
}

// App holds the assembled server: infrastructure clients + domain services.
// Build with New, run with Run, release resources with Close. Which of its
// fields are populated depends on the Role it was built for.
type App struct {
	cfg      Config
	role     Role
	pool     *pgxpool.Pool
	db       *db.Client
	storage  *storage.Client
	static   *static.Assets
	publish  *publish.Service
	view     *view.Service
	raw      *raw.Service
	document *document.Service
	jobs     *jobs.Runner
	limiter  middleware.Limiter
	counter  *redis.Client
}

// New wires the App for role from cfg: opens the DB pool, pings it, builds the
// R2 client, and constructs whatever that role runs. Returns an error on any
// failure; the caller owns shutdown via App.Close.
func New(ctx context.Context, cfg Config, role Role) (*App, error) {
	pool, err := openPool(ctx, cfg.Database.URL)
	if err != nil {
		return nil, err
	}

	app, err := assemble(ctx, cfg, role, pool)
	if err != nil {
		pool.Close()
		return nil, err
	}
	return app, nil
}

// assemble wires the infrastructure both roles need — the DB pool and the R2
// client — then hands off to the role's own wiring. Split from New so assembly
// is exercisable without a reachable database.
func assemble(ctx context.Context, cfg Config, role Role, pool *pgxpool.Pool) (*App, error) {
	app := &App{
		cfg:     cfg,
		role:    role,
		pool:    pool,
		db:      db.New(pool),
		storage: storage.New(cfg.R2),
	}
	if role == RoleJobs {
		app.wireJobs()
		return app, nil
	}
	return app, app.wireWeb(ctx)
}

// wireWeb builds what the HTTP process serves with: the rate limiter, the static
// assets, and the domain services behind the routes. It registers no tickers —
// periodic work lives in the jobs process, because a deploy swap keeps two web
// processes alive at once and would otherwise double every job (ADR-0003).
func (a *App) wireWeb(ctx context.Context) error {
	limiter, counter, err := newLimiter(ctx, a.cfg.RateLimit)
	if err != nil {
		return err
	}
	a.limiter, a.counter = limiter, counter
	a.static = static.New()

	a.publish = &publish.Service{Db: a.db, Storage: a.storage, BaseURL: a.cfg.BaseURL}
	a.document = &document.Service{Db: a.db}
	a.view = &view.Service{Db: a.db, Storage: a.storage, Static: a.static}
	a.raw = &raw.Service{Db: a.db, Storage: a.storage}

	// The best-effort purge fired inline after a write (ADR-0012) belongs where
	// the write happens. It is deliberately outside the drain's wait group and is
	// killed at a deploy swap; that is harmless only because the purge_queue row
	// is the durable path and the jobs process drains it.
	if purger := newPurge(a.cfg, a.db); purger != nil {
		a.publish.Purge = purger
		a.document.Purge = purger
	}
	return nil
}

// wireJobs builds the ticking process: the lifecycle GC and purge services, and
// the runner they hang off, recording every pass to Postgres. No listener and no
// Redis — the jobs process answers nothing.
func (a *App) wireJobs() {
	a.jobs = jobs.New(jobs.SystemClock{}, a.db)
	registerJobs(a.jobs, a.cfg.Jobs, a.db, a.storage, newPurge(a.cfg, a.db))
}

// newPurge builds the CDN purge service (ADR-0012), or returns nil when
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

// newLimiter builds the write-path rate limiter over a pooled Redis connection
// (ADR-0013), returning both so the caller can close the pool. Both are nil when
// REDIS_URL is unset — local dev runs unthrottled rather than refusing to boot,
// and the Cloudflare edge limit still stands in production. A malformed URL is a
// config error and does fail the boot.
func newLimiter(ctx context.Context, cfg RateLimitConfig) (middleware.Limiter, *redis.Client, error) {
	if cfg.URL == "" {
		slog.Warn("redis not configured, write paths are unthrottled")
		return nil, nil, nil
	}
	client, err := redis.New(ctx, redis.Config{URL: cfg.URL})
	if err != nil {
		return nil, nil, err
	}
	return ratelimit.New(client), client, nil
}

// registerJobs wires the periodic jobs onto runner (ADR-0003). Intervals and
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

// Run runs the App's role until ctx is cancelled or a SIGINT/SIGTERM arrives,
// then drains within cfg.ShutdownTimeout and returns.
func (a *App) Run(ctx context.Context) error {
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if a.role == RoleJobs {
		return a.runJobs(ctx)
	}
	return a.runWeb(ctx)
}

// runJobs starts the tickers and blocks until shutdown, then lets a running pass
// finish within cfg.ShutdownTimeout. That budget is the jobs container's own and
// far larger than the web tier's (ADR-0015): nobody is waiting on a GC pass, but
// killing one mid-batch throws the batch away.
func (a *App) runJobs(ctx context.Context) error {
	slog.Info("mdfly-server jobs started", "jobs", a.jobs.Names())

	// Jobs get a context detached from the signal so a SIGTERM doesn't abort a
	// job mid-transaction; the shutdown timeout bounds the wait instead.
	a.jobs.Start(context.WithoutCancel(ctx))
	<-ctx.Done()
	slog.Info("shutdown signal received, draining jobs")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), a.cfg.ShutdownTimeout)
	defer cancel()
	if err := a.jobs.Stop(shutdownCtx); err != nil {
		return fmt.Errorf("stop jobs: %w", err)
	}
	return nil
}

// runWeb serves HTTP until shutdown, then drains in-flight requests within
// cfg.ShutdownTimeout. It ticks nothing.
func (a *App) runWeb(ctx context.Context) error {
	server := &http.Server{
		Addr:              a.cfg.Addr,
		Handler:           a.routes(),
		ReadHeaderTimeout: serverReadHeaderTimeout,
		ReadTimeout:       serverReadTimeout,
		WriteTimeout:      serverWriteTimeout,
		IdleTimeout:       serverIdleTimeout,
	}

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

	if err := a.drainRequests(server); err != nil && serveFailure == nil {
		return err
	}
	return serveFailure
}

// drainRequests lets in-flight requests finish, bounded by cfg.ShutdownTimeout.
func (a *App) drainRequests(server *http.Server) error {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), a.cfg.ShutdownTimeout)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	return nil
}

// Close releases infrastructure resources. Safe to call after Run returns.
func (a *App) Close() {
	if a.counter != nil {
		if err := a.counter.Close(); err != nil {
			slog.Warn("close redis", "err", err)
		}
	}
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
