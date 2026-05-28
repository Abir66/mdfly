package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/Abir66/mdfly/internal/server"
)

func main() {
	cfg, err := server.LoadConfig()
	if err != nil {
		slog.Error("load config", "err", err)
		os.Exit(1)
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.LogLevel})))

	ctx := context.Background()
	app, err := server.New(ctx, cfg)
	if err != nil {
		slog.Error("init server", "err", err)
		os.Exit(1)
	}
	defer app.Close()

	if err := app.Run(ctx); err != nil {
		slog.Error("run server", "err", err)
		os.Exit(1)
	}
}
