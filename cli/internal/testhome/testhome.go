// Package testhome points a test's home directory at a path on every platform.
package testhome

import "testing"

// Set makes dir the directory os.UserHomeDir reports for the rest of the test:
// it reads HOME on Unix and USERPROFILE on Windows, so Set sets both.
func Set(t testing.TB, dir string) {
	t.Helper()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
}
