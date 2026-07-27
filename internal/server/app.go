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

	"github.com/Abir66/mdfly/internal/server/db"
	"github.com/Abir66/mdfly/internal/server/jobs"
	"github.com/Abir66/mdfly/internal/server/service/document"
	"github.com/Abir66/mdfly/internal/server/service/publish"
	"github.com/Abir66/mdfly/internal/server/service/view"
	"github.com/Abir66/mdfly/internal/server/static"
	"github.com/Abir66/mdfly/internal/server/storage"
)

const (
	serverReadHeaderTimeout = 5 * time.Second
	serverReadTimeout       = 30 * time.Second
	serverWriteTimeout      = 60 * time.Second
	serverIdleTimeout       = 120 * time.Second

	dbConnectTimeout = 5 * time.Second
	dbPingTimeout    = 5 * time.Second
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

	return &App{
		cfg:     cfg,
		pool:    pool,
		db:      pg,
		storage: r2,
		static:  assets,
		publish: &publish.Service{
			Db:      pg,
			Storage: r2,
			BaseURL: cfg.BaseURL,
		},
		view:     &view.Service{Db: pg, Storage: r2, Static: assets},
		document: &document.Service{Db: pg},
		jobs:     jobs.New(jobs.SystemClock{}),
	}, nil
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
