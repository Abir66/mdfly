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
	if wantsHelp(os.Args[1:]) {
		fmt.Println(usage)
		return
	}

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

// wantsHelp reports whether the invocation asks for usage rather than a role. It
// is answered before any config is loaded, so `mdfly-server --help` succeeds in a
// bare image with no .env — that is how the published image is smoke-tested.
func wantsHelp(args []string) bool {
	if len(args) != 1 {
		return false
	}
	switch args[0] {
	case "-h", "--help", "help":
		return true
	}
	return false
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
