//go:build !windows

package reviewers

import (
	"os/exec"
	"syscall"
)

// setProcessGroup puts the reviewer in its own process group and makes the timeout
// signal the GROUP.
//
// exec.CommandContext's default cancellation calls Process.Kill(), which signals the
// immediate child PID alone. A reviewer CLI spawns children, so the default leaves
// orphans holding a model call open past the gate's own deadline.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
