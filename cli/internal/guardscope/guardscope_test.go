package guardscope

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/codex"
	"github.com/steer-sh/steer-cli/internal/harness"
)

// isolate points every harness at fresh directories and clears the session
// markers. Without the markers cleared the answers follow the harness the test
// itself runs under, so the suite would pass in Claude Code and fail in Codex.
func isolate(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	t.Setenv("STEER_HARNESS", "")
	t.Setenv("CLAUDECODE", "")
	t.Setenv("CODEX_SESSION_ID", "")
	t.Setenv("PI_CODING_AGENT", "")
	// Project scope resolves against the working directory, so a suite run from a
	// repository carrying its own guard would read that registration.
	t.Chdir(t.TempDir())
	return home
}

func TestHookRegistrarsFollowTheInstalledHarnesses(t *testing.T) {
	home := isolate(t)

	installed, err := HookRegistrars(true)
	if err != nil {
		t.Fatal(err)
	}
	if len(installed) != 0 {
		t.Fatalf("empty machine: got %d targets, want none", len(installed))
	}

	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if installed, err = HookRegistrars(true); err != nil || len(installed) != 1 {
		t.Fatalf("claude only: got %d targets (%v)", len(installed), err)
	}

	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	if installed, err = HookRegistrars(true); err != nil || len(installed) != 2 {
		t.Fatalf("both installed: got %d targets (%v)", len(installed), err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".pi", "agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	if installed, err = HookRegistrars(true); err != nil || len(installed) != 3 {
		t.Fatalf("all installed: got %d targets (%v)", len(installed), err)
	}

	// The unfiltered list ignores presence and includes every hook-capable harness.
	all, err := HookRegistrars(false)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("all: got %d targets, want the three that register hooks", len(all))
	}
}

func TestCheckRefusesAWindowTheGuardDoesNotCover(t *testing.T) {
	isolate(t)

	// A non-guard tier is harness-independent: a check.sh or a review prompt runs
	// wherever the repository is.
	if err := Check("check", true, nil, false); err != nil {
		t.Fatalf("check tier: %v", err)
	}

	for name, scope := range map[string][]string{
		"none":       nil,
		"empty":      {""},
		"whitespace": {"  "},
		"commas":     {" , "},
	} {
		if err := Check("steer", true, scope, false); err == nil {
			t.Fatalf("scope %s: want a --scope-harness refusal", name)
		}
	}
	if err := Check("guard", false, nil, false); err == nil {
		t.Fatal("repo-only scope on a guard tier: want a refusal")
	}

	err := Check("steer", false, []string{"claude-code"}, false)
	if err == nil || !strings.Contains(err.Error(), "does not run") {
		t.Fatalf("unregistered harness: got %v", err)
	}

	// Pi supports hooks but carries no registration in this isolated home.
	err = Check("steer", false, []string{"pi"}, false)
	if err == nil || !strings.Contains(err.Error(), "does not run") {
		t.Fatalf("pi: got %v", err)
	}

	if err := Check("steer", false, []string{"claude-code"}, true); err != nil {
		t.Fatalf("--force: %v", err)
	}
	if err := Check("steer", false, []string{"nope"}, false); err == nil {
		t.Fatal("unknown harness id: want an error")
	}
}

// Codex runs no hook it has not approved, so a runner with no approval record
// guards nothing there. A present record passes: Steer cannot verify it, and
// refusing it would refuse every working install.
func TestCheckRefusesACodexRunnerWithNoApprovalRecord(t *testing.T) {
	home := isolate(t)
	if err := (codex.Registrar{}).RegisterMaster(harness.GlobalScope); err != nil {
		t.Fatalf("register master: %v", err)
	}
	if !RunnerUnapproved(harness.Codex) {
		t.Fatal("a runner with no config.toml read as approved")
	}
	err := Check("steer", false, []string{"codex"}, false)
	if err == nil || !strings.Contains(err.Error(), "no approval record") {
		t.Fatalf("unapproved runner: got %v", err)
	}
	if err := Check("steer", false, []string{"codex"}, true); err != nil {
		t.Fatalf("--force: %v", err)
	}

	hooks := filepath.Join(home, ".codex", "hooks.json")
	key := strconv.Quote(hooks + ":pre_tool_use:0:0")
	config := "[hooks.state." + key + "]\nenabled = true\n"
	if err := os.WriteFile(filepath.Join(home, ".codex", "config.toml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Check("steer", false, []string{"codex"}, false); err != nil {
		t.Fatalf("runner with an approval record: %v", err)
	}
}

func TestCheckAcceptsARegisteredHarness(t *testing.T) {
	isolate(t)
	if err := (claude.Registrar{}).RegisterMaster(harness.GlobalScope); err != nil {
		t.Fatalf("register master: %v", err)
	}
	if err := Check("steer", false, []string{"claude-code"}, false); err != nil {
		t.Fatalf("registered harness: %v", err)
	}
	if err := Check("steer", false, []string{"claude-code", "codex"}, false); err == nil {
		t.Fatal("half-covered scope: want a refusal")
	}
}
