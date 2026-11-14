//go:build windows

package checks

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// lockRegion is one byte past any holder record the file carries, so a locked
// region never stops a contender from reading who holds it.
func lockRegion() *windows.Overlapped { return &windows.Overlapped{OffsetHigh: 1} }

// tryLock takes an exclusive byte-range lock on f without waiting. It reports
// false when another handle holds it.
func tryLock(f *os.File) (bool, error) {
	err := windows.LockFileEx(windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, lockRegion())
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return false, nil
	}
	return err == nil, err
}

func unlock(f *os.File) { _ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, lockRegion()) }
