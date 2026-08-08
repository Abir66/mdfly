//go:build !windows

package localstate

import (
	"os"
	"syscall"
)

// lockFile takes a blocking exclusive advisory lock via flock(2).
func lockFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
}

func unlockFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
