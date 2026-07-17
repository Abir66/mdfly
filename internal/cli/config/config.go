// Package config resolves CLI settings by precedence: flag > env > config.toml > default.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

const (
	// DefaultAPIBase is the compiled-in fallback API base URL.
	DefaultAPIBase = "http://localhost:8080"
	// DefaultStateDirName is the state directory created under the user's home.
	DefaultStateDirName = ".mdfly"
	// ConfigFileName is the opt-in config file inside the state directory.
	ConfigFileName = "config.toml"
	// StateDirMode is the permission mode for the state directory.
	StateDirMode fs.FileMode = 0o700

	envConfigDir = "MDFLY_CONFIG_DIR"
	envAPI       = "MDFLY_API"
	envNoColor   = "NO_COLOR"
)

// Config is the resolved CLI configuration handed to every verb.
type Config struct {
	APIBase  string
	StateDir string
	NoColor  bool
}

// ConfigPath returns the path to the config.toml inside the state directory.
func (c Config) ConfigPath() string {
	return filepath.Join(c.StateDir, ConfigFileName)
}

// Resolve computes the configuration by precedence flag > env > config.toml >
// default. apiFlag is the raw --api value ("" when unset). It reads but never
// writes config.toml and never creates the state directory.
func Resolve(apiFlag string) (Config, error) {
	stateDir, err := resolveStateDir()
	if err != nil {
		return Config{}, err
	}

	fileAPIBase, err := readConfigFile(filepath.Join(stateDir, ConfigFileName))
	if err != nil {
		return Config{}, err
	}

	return Config{
		APIBase:  firstNonEmpty(apiFlag, os.Getenv(envAPI), fileAPIBase, DefaultAPIBase),
		StateDir: stateDir,
		NoColor:  os.Getenv(envNoColor) != "",
	}, nil
}

func resolveStateDir() (string, error) {
	if dir := os.Getenv(envConfigDir); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, DefaultStateDirName), nil
}

// readConfigFile returns the api_base from config.toml, or "" when the file is
// absent. A missing file is not an error (config is opt-in).
func readConfigFile(path string) (string, error) {
	var file struct {
		APIBase string `toml:"api_base"`
	}
	if _, err := toml.DecodeFile(path, &file); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	return file.APIBase, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
