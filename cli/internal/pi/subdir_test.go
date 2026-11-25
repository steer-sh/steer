package pi

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/steer-sh/steer-cli/internal/harness"
)

// Handed a repository subdirectory, Pi's project extensions and AGENTS.md are the
// root's.
func TestProjectFilesForASubdirectoryAreTheRoots(t *testing.T) {
	isolateHome(t)
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

	if got, err := InstructionFile(harness.ProjectScope, sub); err != nil || got != filepath.Join(root, "AGENTS.md") {
		t.Errorf("InstructionFile = %q, %v; want the root's", got, err)
	}
	if got, err := ExtensionsDir(harness.ProjectScope, sub); err != nil || got != filepath.Join(root, ".pi", "extensions") {
		t.Errorf("ExtensionsDir = %q, %v; want the root's", got, err)
	}
}
