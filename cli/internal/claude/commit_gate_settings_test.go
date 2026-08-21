package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Registration cases for the commit gate's settings.json entry. The matcher is
// load-bearing: under a catch-all the gate spawns a process on every tool call, and
// under a non-Bash matcher it never sees a commit and silently stops working.

func withProjectDir(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(wd) })
}

func readSettings(t *testing.T) map[string]any {
	t.Helper()
	path, err := GetSettingsFile(ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func writeSettings(t *testing.T, body string) {
	t.Helper()
	path, err := GetSettingsFile(ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRegisterCommitGateHookWritesTimeout(t *testing.T) {
	withProjectDir(t)
	if err := RegisterCommitGateHook(ProjectScope, "steer commit-gate review", 210); err != nil {
		t.Fatal(err)
	}
	arming := CommitGateHookArmed(ProjectScope)
	if !arming.Effective() {
		t.Fatalf("arming = %+v, want effective", arming)
	}
	if arming.Timeout != 210 {
		t.Errorf("timeout = %d, want 210 — an unset timeout kills a slow review "+
			"mid-flight and the change lands with no message and no row", arming.Timeout)
	}
	if arming.Matcher != CommitGateMatcher {
		t.Errorf("matcher = %q, want %q", arming.Matcher, CommitGateMatcher)
	}
}

// A command this package cannot recognise afterwards is one a later register
// duplicates instead of upserting, so the pile would grow without bound.
func TestRegisterCommitGateHookRejectsUnrecognisableCommand(t *testing.T) {
	withProjectDir(t)
	if err := RegisterCommitGateHook(ProjectScope, "python3 -m something_else", 210); err == nil {
		t.Error("a command failing the identity test must be refused")
	}
}

func TestRegisterCommitGateHookIsIdempotent(t *testing.T) {
	withProjectDir(t)
	for i := 0; i < 3; i++ {
		if err := RegisterCommitGateHook(ProjectScope, "steer commit-gate review", 210); err != nil {
			t.Fatal(err)
		}
	}
	settings, err := LoadSettings(ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, entry := range settings.Hooks.PreToolUse {
		for _, hook := range entry.Hooks {
			if IsCommitGateCommand(hook.Command) {
				count++
			}
		}
	}
	if count != 1 {
		t.Errorf("%d gate hooks registered, want 1", count)
	}
}

// A gate hook sitting under the wrong matcher is present in the file and never invoked
// on a commit, so registration must move it rather than update it in place.
func TestRegisterCommitGateHookMovesAWrongMatcher(t *testing.T) {
	withProjectDir(t)
	writeSettings(t, `{"hooks":{"PreToolUse":[{"matcher":"Read","hooks":[
		{"type":"command","command":"steer commit-gate review"}]}]}}`)
	if err := RegisterCommitGateHook(ProjectScope, "steer commit-gate review", 210); err != nil {
		t.Fatal(err)
	}
	arming := CommitGateHookArmed(ProjectScope)
	if arming.Matcher != CommitGateMatcher || !arming.Effective() {
		t.Errorf("arming = %+v, want the hook moved under %q", arming, CommitGateMatcher)
	}
}

// Registered but inert is the state `status` must name plainly: the config looks armed
// and nothing is being reviewed.
func TestArmingReportsInertMatcher(t *testing.T) {
	withProjectDir(t)
	writeSettings(t, `{"hooks":{"PreToolUse":[{"matcher":"Read","hooks":[
		{"type":"command","command":"steer commit-gate review","timeout":210}]}]}}`)
	arming := CommitGateHookArmed(ProjectScope)
	if !arming.Registered {
		t.Fatal("the hook is present and must be reported as registered")
	}
	if arming.Effective() {
		t.Error("a non-Bash matcher never fires on a commit, so it is not effective")
	}
}

// Unmodeled keys and non-steer hooks survive a register/remove round trip.
func TestCommitGateRoundTripPreservesOtherKeys(t *testing.T) {
	withProjectDir(t)
	writeSettings(t, `{"model":"opus","hooks":{"PreToolUse":[{"matcher":".*","hooks":[
		{"type":"command","command":"python3 other.py","timeout":30}]}],
		"SessionStart":[{"matcher":"","hooks":[{"type":"command","command":"echo hi"}]}]}}`)
	if err := RegisterCommitGateHook(ProjectScope, "steer commit-gate review", 210); err != nil {
		t.Fatal(err)
	}
	if err := RemoveCommitGateHook(ProjectScope); err != nil {
		t.Fatal(err)
	}
	if CommitGateHookArmed(ProjectScope).Registered {
		t.Error("remove must drop the gate hook")
	}
	out := readSettings(t)
	if out["model"] != "opus" {
		t.Errorf("unmodeled top-level key lost: %v", out)
	}
	hooks := out["hooks"].(map[string]any)
	if hooks["SessionStart"] == nil {
		t.Error("an unmodeled hook event was dropped")
	}
	pre := hooks["PreToolUse"].([]any)
	entry := pre[0].(map[string]any)
	hook := entry["hooks"].([]any)[0].(map[string]any)
	if hook["command"] != "python3 other.py" || hook["timeout"] != float64(30) {
		t.Errorf("a non-steer hook lost its command or timeout: %v", hook)
	}
}
