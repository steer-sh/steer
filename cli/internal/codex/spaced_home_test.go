package codex

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/hookstore"
)

// TestASpacedHomeRegistersOneShellWord: Codex runs the registered command through
// a shell, so a home directory holding a space used to register a command the
// shell split in two and no guard fired. The command is executed here, because a
// string comparison passes on the split form as readily as on the quoted one.
func TestASpacedHomeRegistersOneShellWord(t *testing.T) {
	home := filepath.Join(t.TempDir(), "two words")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))

	if _, err := RegisterMaster("global", ""); err != nil {
		t.Fatal(err)
	}
	master, err := hookstore.MasterPath("global")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(master, home) {
		t.Fatalf("master %q is not under the spaced home %q", master, home)
	}

	registered, err := Registrar{}.RegisteredMasterCommand("global")
	if err != nil {
		t.Fatal(err)
	}
	if got := hookstore.MasterPathFromCommand(registered); got != master {
		t.Fatalf("the registration names %q, want %q", got, master)
	}

	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	script := "import sys; print(' '.join(sys.argv[1:]), end='')"
	if err := os.MkdirAll(filepath.Dir(master), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(master, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("sh", "-c", registered).Output()
	if err != nil {
		t.Fatalf("the shell could not run the registration %q: %v", registered, err)
	}
	// The harness flag is what the master reads its reply contract off, so it has
	// to arrive as the master's own argument and not as part of a split path.
	if want := "--harness codex"; string(out) != want {
		t.Fatalf("the master saw %q, want %q", out, want)
	}
}
