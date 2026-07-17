package localstate

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

const (
	credentialsFileName = "credentials"
	credsLockFileName   = "credentials.lock"
)

func (s *Store) credentialsPath() string {
	return filepath.Join(s.dir, credentialsFileName)
}

// LoadToken returns the Edit Token for slug from the credentials file.
func (s *Store) LoadToken(slug string) (string, bool, error) {
	creds, err := s.readCredentials()
	if err != nil {
		return "", false, err
	}
	tok, ok := creds[slug]
	return tok, ok, nil
}

// SaveToken stores the Edit Token for slug in the separate credentials file;
// state records never hold the plaintext token, only a has_token marker.
func (s *Store) SaveToken(slug, token string) error {
	return s.mutateCredentials(func(c map[string]string) {
		c[slug] = token
	})
}

// DeleteToken removes the Edit Token for slug from the credentials file.
func (s *Store) DeleteToken(slug string) error {
	return s.mutateCredentials(func(c map[string]string) {
		delete(c, slug)
	})
}

func (s *Store) readCredentials() (map[string]string, error) {
	data, err := os.ReadFile(s.credentialsPath())
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	creds := map[string]string{}
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, err
	}
	if creds == nil { // JSON null nils the map
		creds = map[string]string{}
	}
	return creds, nil
}

func (s *Store) mutateCredentials(fn func(map[string]string)) error {
	if err := os.MkdirAll(s.dir, DirMode); err != nil {
		return err
	}
	unlock, err := s.lock(credsLockFileName)
	if err != nil {
		return err
	}
	defer unlock()

	creds, err := s.readCredentials()
	if err != nil {
		return err
	}
	fn(creds)
	data, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(s.credentialsPath(), data)
}
