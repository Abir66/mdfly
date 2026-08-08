package localstate

import (
	"os"

	"golang.org/x/sys/windows"
)

// lockByteRange is the region LockFileEx claims. Windows has no whole-file lock
// verb, so one byte past the start stands in for the file: the range need only
// be the same in every process, not cover real content.
const lockByteRange = 1

// lockFile takes a blocking exclusive lock via LockFileEx. Omitting
// LOCKFILE_FAIL_IMMEDIATELY is what makes it wait rather than error, matching
// flock(2) with LOCK_EX on the Unix side.
func lockFile(f *os.File) error {
	return windows.LockFileEx(
		windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK,
		0,
		lockByteRange,
		0,
		&windows.Overlapped{},
	)
}

func unlockFile(f *os.File) error {
	return windows.UnlockFileEx(
		windows.Handle(f.Fd()),
		0,
		lockByteRange,
		0,
		&windows.Overlapped{},
	)
}
