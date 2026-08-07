package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/Abir66/mdfly/internal/server"
)

const usage = "usage: mdfly-server <serve|jobs>"

func main() {
	role, err := parseArgs(os.Args[1:])
	if err != nil {
		slog.Error("bad invocation", "err", err, "usage", usage)
		os.Exit(1)
	}

	cfg, err := server.LoadConfig()
	if err != nil {
		slog.Error("load config", "err", err)
		os.Exit(1)
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.LogLevel})))

	ctx := context.Background()
	app, err := server.New(ctx, cfg, role)
	if err != nil {
		slog.Error("init server", "err", err, "role", role)
		os.Exit(1)
	}
	defer app.Close()

	if err := app.Run(ctx); err != nil {
		slog.Error("run server", "err", err, "role", role)
		os.Exit(1)
	}
}

// parseArgs resolves the subcommand to a role. It requires exactly one and never
// defaults, so a mistyped compose command fails at boot rather than quietly
// running the wrong process.
func parseArgs(args []string) (server.Role, error) {
	if len(args) != 1 {
		return "", fmt.Errorf("expected exactly one subcommand, got %d", len(args))
	}
	return server.ParseRole(args[0])
}
