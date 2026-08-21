//go:build windows

package reviewers

import "os/exec"

// setProcessGroup is a no-op on Windows, which has no process groups to signal. The
// stdlib default still kills the immediate child on timeout; a reviewer CLI's own
// children may outlive it.
func setProcessGroup(cmd *exec.Cmd) {}
