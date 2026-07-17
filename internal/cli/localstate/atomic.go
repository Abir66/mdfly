package localstate

import (
	"os"
	"path/filepath"
	"syscall"
)

// writeAtomic writes data to a temp file in the same directory then renames it
// over path, so a crash mid-write never truncates the existing file.
func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+tempPattern)
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if err := tmp.Chmod(FileMode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// lock acquires an exclusive advisory lock on the named lock file and returns
// an unlock function. Concurrent mutators block until the holder unlocks.
func (s *Store) lock(name string) (func(), error) {
	f, err := os.OpenFile(filepath.Join(s.dir, name), os.O_CREATE|os.O_RDWR, FileMode)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}
