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

const (
	// FileMode is the permission mode for every file the store writes.
	FileMode fs.FileMode = 0o600
	// DirMode is the permission mode for the state directory.
	DirMode fs.FileMode = 0o700

	stateFileName = "state.json"
	lockFileName  = "state.lock"
	bakSuffix     = ".bak"
	tempPattern   = ".tmp-*"
)

// Source distinguishes a file-backed publish from a text (stdin/-m) publish.
type Source string

const (
	SourceFile Source = "file"
	SourceText Source = "text"
)

// Record is one publish ledger entry, keyed by the server-authoritative slug.
// Path is the absolute source path, or nil for a Text Publish.
type Record struct {
	Slug         string    `json:"slug"`
	URL          string    `json:"url"`
	ManifestHash string    `json:"manifest_hash"`
	Tier         string    `json:"tier"`
	Size         int64     `json:"size"`
	FileCount    int       `json:"file_count"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	Source       Source    `json:"source"`
	Path         *string   `json:"path"`
	HasToken     bool      `json:"has_token"`
}

// stateFile is the on-disk shape of state.json.
type stateFile struct {
	Records map[string]Record `json:"records"`
}

// Store is the durable publish ledger rooted at a state directory.
type Store struct {
	dir string
}

// New returns a Store rooted at the given state directory. The directory is
// created lazily on first write.
func New(dir string) *Store {
	return &Store{dir: dir}
}

func (s *Store) statePath() string { return filepath.Join(s.dir, stateFileName) }

// Load reads the ledger. A corrupt state.json is backed up to state.json.bak,
// warned about, and replaced with an empty ledger so publish still works.
func (s *Store) Load() (*Ledger, error) {
	sf, err := s.readState()
	if err != nil {
		return nil, err
	}
	return newLedger(sf.Records), nil
}

// readState parses state.json. A missing file yields an empty state; a corrupt
// file is recovered by backing it up and returning an empty state.
func (s *Store) readState() (stateFile, error) {
	empty := stateFile{Records: map[string]Record{}}
	data, err := os.ReadFile(s.statePath())
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return empty, nil
		}
		return empty, err
	}

	var sf stateFile
	if err := json.Unmarshal(data, &sf); err != nil {
		s.recoverCorrupt(data)
		return empty, nil
	}
	if sf.Records == nil {
		sf.Records = map[string]Record{}
	}
	return sf, nil
}

// recoverCorrupt backs up unparseable state to state.json.bak and warns.
func (s *Store) recoverCorrupt(data []byte) {
	bak := s.statePath() + bakSuffix
	if err := os.WriteFile(bak, data, FileMode); err != nil {
		slog.Warn("localstate: back up corrupt state failed", "path", bak, "err", err)
		return
	}
	slog.Warn("localstate: corrupt state.json recovered", "backup", bak)
}

// Upsert atomically inserts or replaces a record under an exclusive lock.
func (s *Store) Upsert(rec Record) error {
	return s.mutate(func(sf *stateFile) {
		sf.Records[rec.Slug] = rec
	})
}

// Prune atomically removes the record for slug (self-heal on server 404/410).
func (s *Store) Prune(slug string) error {
	return s.mutate(func(sf *stateFile) {
		delete(sf.Records, slug)
	})
}

// mutate runs a read-modify-write cycle while holding the exclusive file lock,
// so concurrent writers cannot lose each other's records.
func (s *Store) mutate(fn func(*stateFile)) error {
	if err := os.MkdirAll(s.dir, DirMode); err != nil {
		return err
	}
	unlock, err := s.lock(lockFileName)
	if err != nil {
		return err
	}
	defer unlock()

	sf, err := s.readState()
	if err != nil {
		return err
	}
	fn(&sf)
	return s.writeState(sf)
}

// writeState serializes the ledger with a write-temp-then-rename so an
// interrupted write leaves the prior good state.json intact.
func (s *Store) writeState(sf stateFile) error {
	data, err := json.MarshalIndent(sf, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(s.statePath(), data)
}
