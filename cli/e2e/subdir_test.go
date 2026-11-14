//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestProjectCommandsFromASubdirectoryWriteTheRoot runs the project guard lifecycle
// and status from a repository subdirectory: every write lands in the root's files,
// the subdirectory gains none, and status there reads the root's registrations.
//
// pins existing behaviour
func TestProjectCommandsFromASubdirectoryWriteTheRoot(t *testing.T) {
	t.Parallel()
	s := NewSandbox(t)
	sub := filepath.Join(s.Repo, "cli")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	src := s.path(noVarHook + ".py")
	if err := os.WriteFile(src, []byte(noVarSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	steer := func(args ...string) Result {
		t.Helper()
		r := s.Run(sub, "", nil, "steer", args...)
		if r.Code != 0 {
			t.Fatalf("steer %q from the subdirectory exited %d:\n%s", args, r.Code, r.Output())
		}
		return r
	}
	steer("register", src, "--rule", noVarRule, "--all-harnesses")
	steer("hooks", "sync", "--scope", "project")
	steer("hooks", "rebuild", "--scope", "project")

	if entries, err := os.ReadDir(sub); err != nil || len(entries) != 0 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("the subdirectory holds %v (%v); want nothing", names, err)
	}
	if c := s.RegisteredCommand(filepath.Join(s.Repo, ".claude", "settings.json"), "steer-hooks.py"); !strings.Contains(c, `"$CLAUDE_PROJECT_DIR/.steer/hooks/steer-hooks.py"`) {
		t.Errorf("the root's Claude Code registration = %q; want the root-relative master", c)
	}
	codexCommand := s.RegisteredCommand(filepath.Join(s.Repo, ".codex", "hooks.json"), "steer-hooks.py")
	if !strings.Contains(codexCommand, `/.steer/hooks/steer-hooks.py"`) {
		t.Errorf("the root's Codex registration = %q; want the root-relative master", codexCommand)
	}
	// Codex runs the root's hook in a session's working directory, which may be the
	// subdirectory: the guard must block there, not fail to find its runner.
	payload := `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"var x = 1"},"cwd":"` + sub + `"}`
	if r := s.Run(sub, payload, nil, "sh", "-c", codexCommand); !strings.Contains(r.Output(), "never use var") {
		t.Errorf("the Codex runner from the subdirectory exited %d without the guard's block:\n%s", r.Code, r.Output())
	}
	for _, doc := range []string{"CLAUDE.md", "AGENTS.md"} {
		data, err := os.ReadFile(filepath.Join(s.Repo, doc))
		if err != nil || !strings.Contains(string(data), "- "+noVarRule) {
			t.Errorf("the root's %s does not carry the rule (%v):\n%s", doc, err, data)
		}
	}

	out := steer("status").Output()
	if strings.Contains(out, "hooks sync --scope project") {
		t.Errorf("status from the subdirectory offers a project sync the root does not need:\n%s", out)
	}
}
