package mine

import (
	"os"
	"testing"
)

// TestMain points the home directory at an empty temporary one, so no test reads
// the operator's ~/.steer, ~/.claude or ~/.codex.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "mine-home-")
	if err != nil {
		panic(err)
	}
	os.Setenv("HOME", home)
	os.Setenv("USERPROFILE", home)
	code := m.Run()
	os.RemoveAll(home)
	os.Exit(code)
}
