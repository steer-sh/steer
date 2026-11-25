//go:build windows

package cmd

import "syscall"

// detachedProcess (DETACHED_PROCESS) frees the child from the parent's console;
// CREATE_NEW_PROCESS_GROUP puts it in its own group. Together they keep the
// background refresh alive after the hook process exits.
const detachedProcess = 0x00000008

// detachAttr detaches the spawned refresh from the hook's console and process
// group, so it outlives the session that started it on Windows too.
func detachAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		CreationFlags: detachedProcess | syscall.CREATE_NEW_PROCESS_GROUP,
	}
}
