//go:build !windows

// Package procgroup runs a child in its own process group, so cancelling its
// context stops the whole tree the child started.
package procgroup

import (
	"os/exec"
	"syscall"
)

// Set puts cmd in its own process group and makes context cancellation signal
// the GROUP.
//
// exec.CommandContext's default cancellation calls Process.Kill(), which signals
// the immediate child PID alone. A shell or reviewer CLI spawns children, so the
// default leaves orphans running past the caller's deadline.
func Set(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
