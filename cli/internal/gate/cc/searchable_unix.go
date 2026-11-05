//go:build !windows

package cc

import "syscall"

// searchable reports whether this process may enter the directory.
func searchable(dir string) bool {
	const searchBit = 1 // X_OK
	return syscall.Access(dir, searchBit) == nil
}
