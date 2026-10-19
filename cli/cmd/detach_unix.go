//go:build !windows

package cmd

import "syscall"

// detachAttr puts the spawned refresh in its own process group, so it outlives the
// session that started it and is never a foreground child of the hook.
func detachAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}
