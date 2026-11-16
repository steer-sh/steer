//go:build windows

package mine

import (
	"os"

	"golang.org/x/sys/windows"
)

func lockInventoryFile(f *os.File) error {
	return windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK,
		0, 1, 0, &windows.Overlapped{})
}

func unlockInventoryFile(f *os.File) {
	_ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &windows.Overlapped{})
}
