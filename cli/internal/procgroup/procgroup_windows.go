//go:build windows

// Package procgroup runs a child in its own process group, so cancelling its
// context stops the whole tree the child started.
package procgroup

import "os/exec"

// Set is a no-op on Windows, which has no process groups to signal. The stdlib
// default still kills the immediate child on cancellation; its own children may
// outlive it.
func Set(cmd *exec.Cmd) {}
