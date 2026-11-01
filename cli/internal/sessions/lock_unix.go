//go:build !windows

package sessions

import (
	"errors"
	"os"
	"syscall"
)

func lockShardFile(f *os.File) error {
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
		if !errors.Is(err, syscall.EINTR) {
			return err
		}
	}
}

func unlockShardFile(f *os.File) { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }
