package localstate

import (
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

const cacheFileName = "cache.json"

// UpdateCache is the volatile once-per-24h update-check record, kept in a
// separate file so a corrupt cache can never clobber the durable ledger.
type UpdateCache struct {
	LastCheck     time.Time `json:"last_check"`
	LatestVersion string    `json:"latest_version"`
}

func (s *Store) cachePath() string { return filepath.Join(s.dir, cacheFileName) }

// LoadCache reads cache.json. A missing or corrupt file yields the zero value
// without error; the cache is disposable and never blocks the CLI.
func (s *Store) LoadCache() (UpdateCache, error) {
	data, err := os.ReadFile(s.cachePath())
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return UpdateCache{}, nil
		}
		return UpdateCache{}, err
	}
	var c UpdateCache
	if err := json.Unmarshal(data, &c); err != nil {
		slog.Warn("localstate: corrupt cache.json ignored", "path", s.cachePath())
		return UpdateCache{}, nil
	}
	return c, nil
}

// SaveCache atomically persists the update-check record.
func (s *Store) SaveCache(c UpdateCache) error {
	if err := os.MkdirAll(s.dir, DirMode); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(s.cachePath(), data)
}
