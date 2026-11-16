package claude

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// From a repository subdirectory, the project settings.json, CLAUDE.md and the
// master command all belong to the root, and registering writes nothing in the
// subdirectory.
func TestProjectFilesFromASubdirectoryAreTheRoots(t *testing.T) {
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
	t.Setenv("HOME", t.TempDir())
	t.Chdir(sub)

	if got, err := GetSettingsFile(ProjectScope); err != nil || got != filepath.Join(root, ".claude", "settings.json") {
		t.Errorf("GetSettingsFile = %q, %v; want the root's", got, err)
	}
	if got, err := GetClaudeMDFile(ProjectScope); err != nil || got != filepath.Join(root, "CLAUDE.md") {
		t.Errorf("GetClaudeMDFile = %q, %v; want the root's", got, err)
	}
	if err := RegisterMasterHook(ProjectScope); err != nil {
		t.Fatal(err)
	}
	if err := SetRules([]string{"use python3"}, ProjectScope); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(sub); len(entries) != 0 {
		t.Errorf("the subdirectory gained %d entries", len(entries))
	}
	command, err := Registrar{}.RegisteredMasterCommand(ProjectScope)
	if err != nil || !strings.Contains(command, `"$CLAUDE_PROJECT_DIR/.steer/hooks/steer-hooks.py"`) {
		t.Errorf("registered command = %q, %v; want the root-relative master", command, err)
	}

	// A registration naming the master absolutely is the same master as the
	// root-relative form, read from the subdirectory.
	master := filepath.Join(root, ".steer", "hooks", "steer-hooks.py")
	if !SamePath(master, filepath.Join(".steer", "hooks", "steer-hooks.py")) {
		t.Errorf("SamePath(%q, root-relative master) = false from a subdirectory", master)
	}
}
