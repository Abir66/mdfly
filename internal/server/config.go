package server

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/Abir66/mdfly/internal/server/storage"
)

const (
	defaultAddr            = ":8080"
	defaultShutdownTimeout = 15 * time.Second
	defaultLogLevel        = slog.LevelInfo
)

// Config is the server's runtime configuration, populated from environment
// variables by LoadConfig. Tests construct it directly.
type Config struct {
	Addr            string
	BaseURL         string
	LogLevel        slog.Level
	ShutdownTimeout time.Duration
	Database        DatabaseConfig
	R2              storage.Config
}

// DatabaseConfig holds Postgres connection parameters.
type DatabaseConfig struct {
	URL string
}

// LoadConfig reads configuration from the environment.
func LoadConfig() (Config, error) {
	dsn, err := requireEnv("DATABASE_URL")
	if err != nil {
		return Config{}, err
	}
	baseURL, err := requireEnv("BASE_URL")
	if err != nil {
		return Config{}, err
	}
	r2Endpoint, err := requireEnv("R2_ENDPOINT")
	if err != nil {
		return Config{}, err
	}
	r2Key, err := requireEnv("R2_ACCESS_KEY_ID")
	if err != nil {
		return Config{}, err
	}
	r2Secret, err := requireEnv("R2_SECRET_ACCESS_KEY")
	if err != nil {
		return Config{}, err
	}
	r2Bucket, err := requireEnv("R2_BUCKET")
	if err != nil {
		return Config{}, err
	}
	cdnBase, err := requireEnv("CDN_BASE_URL")
	if err != nil {
		return Config{}, err
	}

	level, err := parseLogLevel(os.Getenv("LOG_LEVEL"))
	if err != nil {
		return Config{}, err
	}

	shutdown, err := parseDuration(os.Getenv("SHUTDOWN_TIMEOUT"), defaultShutdownTimeout)
	if err != nil {
		return Config{}, fmt.Errorf("SHUTDOWN_TIMEOUT: %w", err)
	}

	return Config{
		Addr:            envOrDefault("ADDR", defaultAddr),
		BaseURL:         baseURL,
		LogLevel:        level,
		ShutdownTimeout: shutdown,
		Database:        DatabaseConfig{URL: dsn},
		R2: storage.Config{
			Endpoint:        r2Endpoint,
			AccessKeyID:     r2Key,
			SecretAccessKey: r2Secret,
			Bucket:          r2Bucket,
			PublicBaseURL:   cdnBase,
		},
	}, nil
}

func requireEnv(key string) (string, error) {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return "", fmt.Errorf("required env var not set: %s", key)
	}
	return v, nil
}

func envOrDefault(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func parseLogLevel(raw string) (slog.Level, error) {
	if raw == "" {
		return defaultLogLevel, nil
	}
	switch strings.ToLower(raw) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("invalid LOG_LEVEL %q", raw)
	}
}

func parseDuration(raw string, def time.Duration) (time.Duration, error) {
	if raw == "" {
		return def, nil
	}
	return time.ParseDuration(raw)
}
