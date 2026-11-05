package claude

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/hookstore"
)

// spacedHome points HOME at a directory whose name holds a space and returns it.
// A global registration names an absolute path built from HOME, so this is the
// machine the quoting exists for.
func spacedHome(t *testing.T) string {
	t.Helper()
	home := filepath.Join(t.TempDir(), "two words")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

// TestASpacedHomeRegistersOneShellWord: a home directory holding a space used to
// register a command the shell split in two, so Claude Code ran python3 against
// the first half and no guard fired. The command is executed here, because a
// string comparison passes on the split form as readily as on the quoted one.
func TestASpacedHomeRegistersOneShellWord(t *testing.T) {
	home := spacedHome(t)
	if err := RegisterMasterHook(GlobalScope); err != nil {
		t.Fatal(err)
	}
	master, err := GetMasterHookPath(GlobalScope)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(master, home) {
		t.Fatalf("master %q is not under the spaced home %q", master, home)
	}

	registered, err := Registrar{}.RegisteredMasterCommand(GlobalScope)
	if err != nil {
		t.Fatal(err)
	}
	if got := hookstore.MasterPathFromCommand(registered); got != master {
		t.Fatalf("the registration names %q, want %q", got, master)
	}
	readBack, err := Registrar{}.RegisteredMasterPath(GlobalScope)
	if err != nil {
		t.Fatal(err)
	}
	// SamePath is what sync and the status snapshot compare, and a decoder that
	// truncated the path would send both into a rewrite of the wrong file.
	if !SamePath(readBack, master) {
		t.Fatalf("the registration reads back as %q, want %q", readBack, master)
	}

	// The shell is the reader that matters: it is what both harnesses hand the
	// command to, and it is what used to split this path.
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	script := "import sys; print(sys.argv[1] if len(sys.argv) > 1 else '', end='')"
	if err := os.MkdirAll(filepath.Dir(master), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(master, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("sh", "-c", registered+" seen").Output()
	if err != nil {
		t.Fatalf("the shell could not run the registration %q: %v", registered, err)
	}
	if string(out) != "seen" {
		t.Fatalf("the shell ran the registration as %q, so the path did not stay one word", out)
	}
}

// TestASpacedHomeReplacesAnOlderUnquotedRegistration: the broken registrations
// are on disk already, so a rebuild has to replace one rather than add a second
// master beside it.
func TestASpacedHomeReplacesAnOlderUnquotedRegistration(t *testing.T) {
	spacedHome(t)
	master, err := GetMasterHookPath(GlobalScope)
	if err != nil {
		t.Fatal(err)
	}
	if err := EnsureDirectories(GlobalScope); err != nil {
		t.Fatal(err)
	}
	settingsFile, err := GetSettingsFile(GlobalScope)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(settingsFile), 0o755); err != nil {
		t.Fatal(err)
	}
	unquoted := `{"hooks":{"PreToolUse":[{"matcher":"*","hooks":[` +
		`{"type":"command","command":"python3 ` + master + `"}]}]}}`
	if err := os.WriteFile(settingsFile, []byte(unquoted), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := RegisterMasterHook(GlobalScope); err != nil {
		t.Fatal(err)
	}
	settings, err := LoadSettings(GlobalScope)
	if err != nil {
		t.Fatal(err)
	}
	masters := 0
	for _, entry := range settings.Hooks.PreToolUse {
		for _, hook := range entry.Hooks {
			if hookstore.IsMasterCommand(hook.Command) {
				masters++
				if hookstore.MasterPathFromCommand(hook.Command) != master {
					t.Fatalf("the replacement names %q, want %q", hook.Command, master)
				}
			}
		}
	}
	if masters != 1 {
		t.Fatalf("settings.json holds %d master registrations, want 1", masters)
	}
}

// TestAProjectRegistrationIsAnchoredAtItsConfig: the command names the master
// through the variable Claude Code sets, then the config-relative path, and reading
// it back returns that relative path, which the path comparisons use.
func TestAProjectRegistrationIsAnchoredAtItsConfig(t *testing.T) {
	command, err := GetMasterHookCommand(ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	registration, err := hookstore.MasterRegistration(ProjectScope, "")
	if err != nil {
		t.Fatal(err)
	}
	relative := filepath.ToSlash(registration)
	if want := `python3 "$CLAUDE_PROJECT_DIR/` + relative + `"`; command != want {
		t.Fatalf("project command = %q, want %q", command, want)
	}
	if got := registeredMasterPath(command); got != relative {
		t.Fatalf("read back %q, want %q", got, relative)
	}
}

// TestARebuildReportsAChangedCommand: Codex hashes a handler's definition when
// the operator approves it, so a rebuild that rewrites the command text costs
// that approval. Previous and Path are decoded paths and read the same across a
// change of quoting, so only the command text can report it.
func TestARebuildReportsAChangedCommand(t *testing.T) {
	home := spacedHome(t)
	store := filepath.Join(home, ".steer", "hooks")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestHook(t, filepath.Join(store, "no-var-hook.py"))
	manifest := []byte(`{"hooks":{"no-var-hook":{"file":"no-var-hook.py","rule":"never use var","created":"2026-01-01"}}}`)
	if err := os.WriteFile(filepath.Join(store, ManifestName), manifest, 0o644); err != nil {
		t.Fatal(err)
	}
	master := filepath.Join(store, hookstore.MasterName)
	if err := os.WriteFile(master, []byte("# an older steer wrote this\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	settingsFile, err := GetSettingsFile(GlobalScope)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(settingsFile), 0o755); err != nil {
		t.Fatal(err)
	}
	unquoted := `{"hooks":{"PreToolUse":[{"matcher":"*","hooks":[` +
		`{"type":"command","command":"python3 ` + filepath.ToSlash(master) + `"}]}]}}`
	if err := os.WriteFile(settingsFile, []byte(unquoted), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := RebuildMaster(GlobalScope)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Targets) != 1 {
		t.Fatalf("targets = %d, want 1", len(result.Targets))
	}
	if !result.Targets[0].CommandChanged {
		t.Fatal("a rebuild that quoted the path reported no command change")
	}

	// A second rebuild writes the same text, so it costs no approval and says so.
	again, err := RebuildMaster(GlobalScope)
	if err != nil {
		t.Fatal(err)
	}
	if again.Targets[0].CommandChanged {
		t.Fatal("a rebuild that rewrote nothing reported a command change")
	}
}
