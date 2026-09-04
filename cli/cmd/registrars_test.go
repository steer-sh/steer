package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/steer-sh/steer-cli/internal/harness"
)

// isolatedHome points every harness at a fresh directory and clears the session
// markers, so the default a test reads never follows the harness it runs under.
func isolatedHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	t.Setenv("STEER_HARNESS", "")
	t.Setenv("CLAUDECODE", "")
	t.Setenv("CODEX_SESSION_ID", "")
	t.Setenv("PI_CODING_AGENT", "")
	harnessFlag, allHarnesses = "", false
	t.Cleanup(func() { harnessFlag, allHarnesses = "", false })
	return home
}

func TestCurrentRegistrarsDefaultsToTheInstalledHarnesses(t *testing.T) {
	home := isolatedHome(t)

	// No harness directory at all: the fallback still answers Claude Code, because
	// that is what every settings.json written before this default assumes.
	targets, err := currentRegistrars()
	if err != nil {
		t.Fatalf("currentRegistrars: %v", err)
	}
	if len(targets) != 1 || targets[0].Harness().ID != harness.ClaudeCode {
		t.Fatalf("empty machine: got %s, want claude-code", harnessNames(targets))
	}

	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if targets, err = currentRegistrars(); err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].Harness().ID != harness.ClaudeCode {
		t.Fatalf("claude only: got %s, want claude-code", harnessNames(targets))
	}

	// A second harness on the machine joins the default. The guard store is shared,
	// so a guard installed once is meant to run in both.
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	if targets, err = currentRegistrars(); err != nil {
		t.Fatal(err)
	}
	if len(targets) != 2 {
		t.Fatalf("both installed: got %s, want two targets", harnessNames(targets))
	}

	if err := os.MkdirAll(filepath.Join(home, ".pi", "agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	if targets, err = currentRegistrars(); err != nil {
		t.Fatal(err)
	}
	if len(targets) != 3 {
		t.Fatalf("all installed: got %s, want three targets", harnessNames(targets))
	}
}

func TestCurrentRegistrarsHonoursAnExplicitHarness(t *testing.T) {
	home := isolatedHome(t)
	for _, dir := range []string{".claude", ".codex"} {
		if err := os.MkdirAll(filepath.Join(home, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	harnessFlag = "codex"
	targets, err := currentRegistrars()
	if err != nil {
		t.Fatalf("currentRegistrars: %v", err)
	}
	if len(targets) != 1 || targets[0].Harness().ID != harness.Codex {
		t.Fatalf("--harness codex: got %s", harnessNames(targets))
	}
}
