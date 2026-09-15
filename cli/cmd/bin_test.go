package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/claude"
	codexhooks "github.com/steer-sh/steer-cli/internal/codex"
	"github.com/steer-sh/steer-cli/internal/steerbin"
)

// TestRelinkRepointsAPATHRegistration pins the repair half: a registration
// written before the stable executable existed names the bare word, and one
// install rewrites it to the absolute path, keeping its timeout and dropping the
// reviewer flags that outrank the settings file.
func TestRelinkRepointsAPATHRegistration(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := claude.RegisterCommitGateHook(claude.GlobalScope,
		"steer commit-gate review --provider gemini", 210); err != nil {
		t.Fatal(err)
	}
	path, err := steerbin.Ensure()
	if err != nil {
		t.Fatal(err)
	}

	rewritten, err := relinkClaude(claude.GlobalScope, path)
	if err != nil {
		t.Fatal(err)
	}
	if rewritten != 1 {
		t.Fatalf("rewrote %d registration(s), want 1", rewritten)
	}

	gate := claude.CommitGateHookArmed(claude.GlobalScope)
	want := steerbin.Quote(path) + " commit-gate review"
	if gate.Command != want {
		t.Fatalf("gate command = %q, want %q", gate.Command, want)
	}
	if gate.Timeout != 210 {
		t.Fatalf("timeout = %d, want 210 preserved", gate.Timeout)
	}
	if steerbin.Resolve(gate.Command) != steerbin.Resolvable {
		t.Fatal("the rewritten command does not resolve")
	}

	// A second install rewrites nothing: the registrations already name the path.
	rewritten, err = relinkClaude(claude.GlobalScope, path)
	if err != nil {
		t.Fatal(err)
	}
	if rewritten != 0 {
		t.Fatalf("second pass rewrote %d registration(s), want 0", rewritten)
	}
}

// TestAForeignCommandIsNeverRewritten: a user's own wrapper script keeps the
// registration it was given. Steer repoints its own command, not everyone's.
func TestAForeignCommandIsNeverRewritten(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	wrapper := "/usr/local/bin/my-wrapper commit-gate review"
	if err := claude.RegisterCommitGateHook(claude.GlobalScope, wrapper, 210); err != nil {
		t.Fatal(err)
	}
	path, err := steerbin.Ensure()
	if err != nil {
		t.Fatal(err)
	}

	rewritten, err := relinkClaude(claude.GlobalScope, path)
	if err != nil {
		t.Fatal(err)
	}

	if rewritten != 0 {
		t.Fatalf("rewrote %d registration(s), want 0", rewritten)
	}
	if got := claude.CommitGateHookArmed(claude.GlobalScope).Command; got != wrapper {
		t.Fatalf("command = %q, want %q", got, wrapper)
	}
}

// TestInstallRegistersTheStableExecutable pins the install half: the command a
// fresh registration carries names the absolute path, not the bare word.
func TestInstallRegistersTheStableExecutable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path, err := steerbin.Ensure()
	if err != nil {
		t.Fatal(err)
	}

	command := steerHookCommand("commit-gate review")

	if !strings.HasPrefix(command, steerbin.Quote(path)) {
		t.Fatalf("command = %q, want it to name %q", command, path)
	}
	if steerbin.Resolve(command) != steerbin.Resolvable {
		t.Fatal("a fresh registration names a command that does not resolve")
	}
}

// TestCodexRelinkWritesNoEmptyResolver pins that repointing a Codex gate whose
// resolver is not registered leaves the turn-end events alone. The writer arms
// both channels in one save, and an empty resolver command means "arm the gate
// only" — not "register a hook that runs nothing".
func TestCodexRelinkWritesNoEmptyResolver(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	cwd := withCmdProjectTempDir(t)
	_ = cwd
	if _, err := codexhooks.RegisterCommitGate(claude.GlobalScope, cwd,
		"steer commit-gate review-codex", 210); err != nil {
		t.Fatal(err)
	}
	path, err := steerbin.Ensure()
	if err != nil {
		t.Fatal(err)
	}

	rewritten, err := relinkCodex(claude.GlobalScope, path)
	if err != nil {
		t.Fatal(err)
	}

	if rewritten != 1 {
		t.Fatalf("rewrote %d registration(s), want 1 (the gate alone)", rewritten)
	}
	gate := codexhooks.CommitGateArming(claude.GlobalScope, cwd)
	if !strings.HasPrefix(gate.Command, steerbin.Quote(path)) {
		t.Fatalf("gate command = %q, want the stable path", gate.Command)
	}
	raw, err := os.ReadFile(filepath.Join(home, ".codex", "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"Stop"`) || strings.Contains(string(raw), `"SubagentStop"`) {
		t.Fatalf("a resolver event was written with no resolver registered:\n%s", raw)
	}
}
