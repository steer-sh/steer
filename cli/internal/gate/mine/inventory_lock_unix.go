//go:build !windows

package mine

import (
	"errors"
	"os"
	"syscall"
)

func lockInventoryFile(f *os.File) error {
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
		if !errors.Is(err, syscall.EINTR) {
			return err
		}
	}
}

func unlockInventoryFile(f *os.File) { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }
