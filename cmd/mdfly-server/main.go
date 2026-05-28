package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Abir66/mdfly/internal/server/db"
	"github.com/Abir66/mdfly/internal/server/handlers"
	"github.com/Abir66/mdfly/internal/server/service/publish"
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

func main() {
	dsn := requireEnv("DATABASE_URL")
	baseURL := requireEnv("BASE_URL")

	connCtx, connCancel := context.WithTimeout(context.Background(), dbConnectTimeout)
	defer connCancel()
	pool, err := pgxpool.New(connCtx, dsn)
	if err != nil {
		slog.Error("open db", "err", err)
		os.Exit(1)
	}
	defer pool.Close()
	pingCtx, cancel := context.WithTimeout(context.Background(), dbPingTimeout)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		slog.Error("ping db", "err", err)
		os.Exit(1)
	}

	r2 := storage.New(storage.Config{
		Endpoint:        requireEnv("R2_ENDPOINT"),
		AccessKeyID:     requireEnv("R2_ACCESS_KEY_ID"),
		SecretAccessKey: requireEnv("R2_SECRET_ACCESS_KEY"),
		Bucket:          requireEnv("R2_BUCKET"),
		PublicBaseURL:   requireEnv("CDN_BASE_URL"),
	})

	pg := db.New(pool)
	pubSvc := &publish.Service{
		Db:      pg,
		Storage: r2,
		BaseURL: baseURL,
	}
	viewDeps := handlers.ViewDeps{PG: pg, R2: r2}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/publish/init", handlers.Init(pubSvc))
	mux.HandleFunc("POST /v1/publish/commit", handlers.Commit(pubSvc))
	mux.HandleFunc("GET /{slug}", handlers.View(viewDeps))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, "ok")
	})

	addr := envOrDefault("ADDR", ":8080")
	server := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: serverReadHeaderTimeout,
		ReadTimeout:       serverReadTimeout,
		WriteTimeout:      serverWriteTimeout,
		IdleTimeout:       serverIdleTimeout,
	}
	slog.Info("mdfly-server listening", "addr", addr)
	if err := server.ListenAndServe(); err != nil {
		slog.Error("listen", "err", err)
		os.Exit(1)
	}
}

func requireEnv(key string) string {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		slog.Error("required env var not set", "key", key)
		os.Exit(1)
	}
	return v
}

func envOrDefault(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}
