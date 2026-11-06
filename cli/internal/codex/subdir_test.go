package codex

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Handed a repository subdirectory, the project hooks.json and AGENTS.md are the
// root's, and the registration names the master from the root.
func TestProjectFilesForASubdirectoryAreTheRoots(t *testing.T) {
	dir := t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Skipf("git init unavailable: %s", out)
	}
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "cli")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", t.TempDir())

	if got, err := InstructionFile("project", sub); err != nil || got != filepath.Join(root, "AGENTS.md") {
		t.Errorf("InstructionFile = %q, %v; want the root's", got, err)
	}
	path, err := RegisterMaster("project", sub)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, ".codex", "hooks.json"); path != want {
		t.Errorf("RegisterMaster wrote %q; want %q", path, want)
	}
	if entries, _ := os.ReadDir(sub); len(entries) != 0 {
		t.Errorf("the subdirectory gained %d entries", len(entries))
	}
	if command := MasterArming("project", sub).Command; !strings.HasSuffix(command, " .steer/hooks/steer-hooks.py --harness codex") {
		t.Errorf("registered command = %q; want the root-relative master", command)
	}
}
