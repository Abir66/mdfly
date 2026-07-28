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

	defaultPurgeDrainInterval  = 15 * time.Minute
	defaultLifecycleGCInterval = time.Hour
	defaultAbandonGrace        = time.Hour
	defaultBlobDeleteGrace     = 24 * time.Hour
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
	Jobs            JobsConfig
	RateLimit       RateLimitConfig
	Cloudflare      CloudflareConfig
}

// RateLimitConfig holds the Upstash REST credentials backing the write-path rate
// limiter (ADR-0028). Env vars: UPSTASH_REDIS_REST_URL, UPSTASH_REDIS_REST_TOKEN.
// Both empty means unconfigured — the server boots with the limiter off.
type RateLimitConfig struct {
	URL   string
	Token string
}

// CloudflareConfig holds the zone and API token the CDN purge uses (ADR-0031).
// Env vars: CLOUDFLARE_ZONE_ID, CLOUDFLARE_API_TOKEN. Either empty means
// unconfigured — purges are still enqueued durably, but nothing drains them.
type CloudflareConfig struct {
	ZoneID string
	Token  string
}

// DatabaseConfig holds Postgres connection parameters.
type DatabaseConfig struct {
	URL string
}

// JobsConfig holds the periodic-job intervals and grace windows (ADR-0029).
// Env vars, with defaults: PURGE_DRAIN_INTERVAL (15m), LIFECYCLE_GC_INTERVAL
// (1h), ABANDON_GRACE (1h), BLOB_DELETE_GRACE (24h).
type JobsConfig struct {
	PurgeDrainInterval  time.Duration
	LifecycleGCInterval time.Duration
	AbandonGrace        time.Duration
	BlobDeleteGrace     time.Duration
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

	jobs, err := loadJobsConfig()
	if err != nil {
		return Config{}, err
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
		Jobs: jobs,
		RateLimit: RateLimitConfig{
			URL:   os.Getenv("UPSTASH_REDIS_REST_URL"),
			Token: os.Getenv("UPSTASH_REDIS_REST_TOKEN"),
		},
		Cloudflare: CloudflareConfig{
			ZoneID: os.Getenv("CLOUDFLARE_ZONE_ID"),
			Token:  os.Getenv("CLOUDFLARE_API_TOKEN"),
		},
	}, nil
}

func loadJobsConfig() (JobsConfig, error) {
	var cfg JobsConfig
	for _, spec := range []struct {
		key string
		def time.Duration
		dst *time.Duration
	}{
		{"PURGE_DRAIN_INTERVAL", defaultPurgeDrainInterval, &cfg.PurgeDrainInterval},
		{"LIFECYCLE_GC_INTERVAL", defaultLifecycleGCInterval, &cfg.LifecycleGCInterval},
		{"ABANDON_GRACE", defaultAbandonGrace, &cfg.AbandonGrace},
		{"BLOB_DELETE_GRACE", defaultBlobDeleteGrace, &cfg.BlobDeleteGrace},
	} {
		d, err := parseDuration(os.Getenv(spec.key), spec.def)
		if err != nil {
			return JobsConfig{}, fmt.Errorf("%s: %w", spec.key, err)
		}
		*spec.dst = d
	}
	return cfg, nil
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
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, err
	}
	if d <= 0 {
		return 0, fmt.Errorf("duration must be positive, got %q", raw)
	}
	return d, nil
}
