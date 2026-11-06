package codex

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// bareMasterCommand is the project registration Steer wrote before the root anchor.
const bareMasterCommand = "python3 .steer/hooks/steer-hooks.py --harness codex"

// gitRepo returns the physical root of a new repository.
func gitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Skipf("git init unavailable: %s", out)
	}
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// Handed a repository subdirectory, the project hooks.json and AGENTS.md are the
// root's, and the registration names the master from the root.
func TestProjectFilesForASubdirectoryAreTheRoots(t *testing.T) {
	root := gitRepo(t)
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
	if command := MasterArming("project", sub).Command; !strings.HasSuffix(command, `/.steer/hooks/steer-hooks.py" --harness codex`) {
		t.Errorf("registered command = %q; want the root-relative master", command)
	}
}

// writeProbeMaster puts a master at dir's project store that prints "ran".
func writeProbeMaster(t *testing.T, dir string) {
	t.Helper()
	store := filepath.Join(dir, ".steer", "hooks")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	script := "import sys\nsys.stdin.read()\nprint('ran')\n"
	if err := os.WriteFile(filepath.Join(store, "steer-hooks.py"), []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
}

// runCommand runs a registered command through a shell in dir, as Codex runs a hook
// in the session's working directory.
func runCommand(t *testing.T, command, dir string) (string, error) {
	t.Helper()
	run := exec.Command("sh", "-c", command)
	run.Dir = dir
	run.Stdin = strings.NewReader(`{"tool_name":"Bash","tool_input":{"command":"ls"}}`)
	out, err := run.CombinedOutput()
	return string(out), err
}

// Codex loads the root's hooks.json in a session started from a subdirectory and
// runs the hook there. The master must still run, or python3's exit 2 blocks every
// call. Outside any repository the store anchors at the directory itself, and the
// command must find it there.
func TestARegisteredMasterRunsFromAnySubdirectory(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not on PATH")
	}
	t.Setenv("CODEX_HOME", t.TempDir())
	root := gitRepo(t)
	writeProbeMaster(t, root)
	sub := filepath.Join(root, "cli", "deep")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := RegisterMaster("project", sub); err != nil {
		t.Fatal(err)
	}
	command := MasterArming("project", root).Command
	for _, dir := range []string{root, sub} {
		if out, err := runCommand(t, command, dir); err != nil || strings.TrimSpace(out) != "ran" {
			t.Errorf("from %s: %q, %v", dir, out, err)
		}
	}

	plain, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if exec.Command("git", "-C", plain, "rev-parse", "--show-toplevel").Run() == nil {
		t.Skip("the temporary directory sits inside a repository")
	}
	writeProbeMaster(t, plain)
	if _, err := RegisterMaster("project", plain); err != nil {
		t.Fatal(err)
	}
	command = MasterArming("project", plain).Command
	if out, err := runCommand(t, command, plain); err != nil || strings.TrimSpace(out) != "ran" {
		t.Errorf("outside a repository: %q, %v", out, err)
	}
}

// A cmd.exe host keeps the bare form, and a global registration stays absolute.
func TestMasterCommandFormsOutsideAPosixProject(t *testing.T) {
	root := gitRepo(t)
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	if got, err := masterCommandOn("windows", "project", root); err != nil || got != bareMasterCommand {
		t.Errorf("windows project command = %q, %v; want %q", got, err, bareMasterCommand)
	}
	got, err := masterCommandOn("linux", "global", root)
	if err != nil || strings.Contains(got, projectRootPrefix) || !strings.Contains(got, string(filepath.Separator)+".steer") {
		t.Errorf("global command = %q, %v; want the absolute master", got, err)
	}
}

// A registration written before the anchor names the same master, so only its text
// shows it is stale. Re-registering rewrites it in its own slot, keeps the timeout,
// and the approval Codex saved for the old text reads as modified.
func TestABareRegistrationIsRewrittenInItsSlot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	root := gitRepo(t)
	path := filepath.Join(root, ".codex", "hooks.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	other := map[string]any{"matcher": "Bash", "hooks": []any{map[string]any{"type": "command", "command": "other-tool"}}}
	master := map[string]any{"matcher": MasterMatcher, "hooks": []any{map[string]any{
		"type": "command", "command": bareMasterCommand, "timeout": float64(45), "statusMessage": MasterStatusMessage}}}
	raw, err := json.Marshal(map[string]any{"hooks": map[string]any{"PreToolUse": []any{other, master}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	hash, ok := handlerHash(handlerSlot{event: "PreToolUse", matcher: MasterMatcher, hook: master["hooks"].([]any)[0].(map[string]any)})
	if !ok {
		t.Fatal("no hash for the bare handler")
	}
	config := fmt.Sprintf("[hooks.state.%s]\ntrusted_hash = %q\n", strconv.Quote(path+":pre_tool_use:1:0"), hash)
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := MasterArming("project", root).Approval; got != ApprovalApproved {
		t.Fatalf("before: approval = %q", got)
	}
	if stale, err := MasterCommandStale("project", root); err != nil || !stale {
		t.Fatalf("MasterCommandStale = %v, %v; want true", stale, err)
	}
	if got := registeredMasterPath(bareMasterCommand); got != ".steer/hooks/steer-hooks.py" {
		t.Fatalf("bare path = %q", got)
	}

	if _, err := RegisterMaster("project", root); err != nil {
		t.Fatal(err)
	}
	want, err := MasterCommand("project", root)
	if err != nil {
		t.Fatal(err)
	}
	if got := entryCommands(t, path, "PreToolUse"); len(got) != 2 || got[0] != "other-tool" || got[1] != want {
		t.Fatalf("entries = %q; want other-tool then %q", got, want)
	}
	a := MasterArming("project", root)
	if a.Timeout != 45 || a.Approval != ApprovalModified {
		t.Errorf("after: timeout %d, approval %q; want 45, %q", a.Timeout, a.Approval, ApprovalModified)
	}
	if got := registeredMasterPath(a.Command); got != ".steer/hooks/steer-hooks.py" {
		t.Errorf("anchored path = %q", got)
	}
	if stale, err := MasterCommandStale("project", root); err != nil || stale {
		t.Errorf("after: MasterCommandStale = %v, %v; want false", stale, err)
	}
	if text, _ := os.ReadFile(path); !strings.Contains(string(text), "2>/dev/null") {
		t.Errorf("hooks.json escapes the redirection:\n%s", text)
	}
}
