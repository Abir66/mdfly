package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolve_precedence(t *testing.T) {
	tests := []struct {
		name        string
		apiFlag     string
		envAPI      string
		fileAPIBase string // "" means no config.toml written
		want        string
	}{
		{name: "default when nothing set", want: DefaultAPIBase},
		{name: "config file over default", fileAPIBase: "https://file.example", want: "https://file.example"},
		{name: "env over config file", envAPI: "https://env.example", fileAPIBase: "https://file.example", want: "https://env.example"},
		{name: "flag over env", apiFlag: "https://flag.example", envAPI: "https://env.example", fileAPIBase: "https://file.example", want: "https://flag.example"},
		{name: "flag over default", apiFlag: "https://flag.example", want: "https://flag.example"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv(envConfigDir, dir)
			t.Setenv(envAPI, tt.envAPI)
			if tt.fileAPIBase != "" {
				writeConfigFile(t, dir, tt.fileAPIBase)
			}

			cfg, err := Resolve(tt.apiFlag)
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if cfg.APIBase != tt.want {
				t.Errorf("APIBase=%q, want %q", cfg.APIBase, tt.want)
			}
		})
	}
}

func TestResolve_configDirRelocation(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(envConfigDir, dir)

	cfg, err := Resolve("")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if cfg.StateDir != dir {
		t.Errorf("StateDir=%q, want %q", cfg.StateDir, dir)
	}
	if cfg.ConfigPath() != filepath.Join(dir, ConfigFileName) {
		t.Errorf("ConfigPath=%q, want under %q", cfg.ConfigPath(), dir)
	}
}

func TestResolve_defaultStateDirUnderHome(t *testing.T) {
	t.Setenv(envConfigDir, "")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home dir: %v", err)
	}
	cfg, err := Resolve("")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	want := filepath.Join(home, DefaultStateDirName)
	if cfg.StateDir != want {
		t.Errorf("StateDir=%q, want %q", cfg.StateDir, want)
	}
}

func TestResolve_noColor(t *testing.T) {
	t.Setenv(envConfigDir, t.TempDir())
	t.Setenv(envNoColor, "1")
	cfg, err := Resolve("")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !cfg.NoColor {
		t.Error("NoColor=false, want true when NO_COLOR set")
	}
}

func TestResolve_missingConfigFileIsNotError(t *testing.T) {
	t.Setenv(envConfigDir, t.TempDir())
	if _, err := Resolve(""); err != nil {
		t.Errorf("Resolve with no config.toml should succeed, got %v", err)
	}
}

func writeConfigFile(t *testing.T, dir, apiBase string) {
	t.Helper()
	body := "api_base = \"" + apiBase + "\"\n"
	if err := os.WriteFile(filepath.Join(dir, ConfigFileName), []byte(body), 0o600); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}
}
