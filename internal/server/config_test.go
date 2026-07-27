package server_test

import (
	"testing"
	"time"

	"github.com/Abir66/mdfly/internal/server"
)

// setRequiredEnv sets the env vars LoadConfig demands, so each test only varies
// the ones it cares about.
func setRequiredEnv(t *testing.T) {
	t.Helper()
	for k, v := range map[string]string{
		"DATABASE_URL":         "postgres://localhost/mdfly",
		"BASE_URL":             "https://mdfly.dev",
		"R2_ENDPOINT":          "https://r2.example.com",
		"R2_ACCESS_KEY_ID":     "key",
		"R2_SECRET_ACCESS_KEY": "secret",
		"R2_BUCKET":            "mdfly",
		"CDN_BASE_URL":         "https://cdn.mdfly.dev",
	} {
		t.Setenv(k, v)
	}
}

func TestLoadConfigJobDefaults(t *testing.T) {
	setRequiredEnv(t)

	cfg, err := server.LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	want := server.JobsConfig{
		PurgeDrainInterval:  15 * time.Minute,
		LifecycleGCInterval: time.Hour,
		AbandonGrace:        time.Hour,
		BlobDeleteGrace:     24 * time.Hour,
	}
	if cfg.Jobs != want {
		t.Fatalf("job defaults = %+v, want %+v", cfg.Jobs, want)
	}
}

func TestLoadConfigJobOverrides(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("PURGE_DRAIN_INTERVAL", "30s")
	t.Setenv("LIFECYCLE_GC_INTERVAL", "5m")
	t.Setenv("ABANDON_GRACE", "10m")
	t.Setenv("BLOB_DELETE_GRACE", "48h")

	cfg, err := server.LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	want := server.JobsConfig{
		PurgeDrainInterval:  30 * time.Second,
		LifecycleGCInterval: 5 * time.Minute,
		AbandonGrace:        10 * time.Minute,
		BlobDeleteGrace:     48 * time.Hour,
	}
	if cfg.Jobs != want {
		t.Fatalf("job overrides = %+v, want %+v", cfg.Jobs, want)
	}
}

func TestLoadConfigRejectsInvalidJobDuration(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("LIFECYCLE_GC_INTERVAL", "-1h")

	if _, err := server.LoadConfig(); err == nil {
		t.Fatal("LoadConfig accepted a negative LIFECYCLE_GC_INTERVAL")
	}
}
