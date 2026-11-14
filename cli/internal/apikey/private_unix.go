//go:build !windows

package apikey

import (
	"fmt"
	"io/fs"
	"os"
	"syscall"
)

// checkPrivate refuses a path that another user owns, or whose mode grants any bit
// in wide.
func checkPrivate(path string, info fs.FileInfo, wide fs.FileMode) error {
	if st, ok := info.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Getuid() {
		return fmt.Errorf("%s is owned by uid %d, not by this user; remove it and run %s", path, st.Uid, SetCommand)
	}
	if mode := info.Mode().Perm(); mode&wide != 0 {
		return fmt.Errorf("%s has mode %04o, which other users can read; run chmod go-rwx %s", path, mode, path)
	}
	return nil
}
