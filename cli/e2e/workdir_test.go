//go:build e2e

package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestDirectoryFlagWritesTheNamedRepository runs guard, check-tier and status
// commands from one repository with -C naming another: each write lands in the
// named repository and the starting repository is left as it was.
//
// pins existing behaviour
func TestDirectoryFlagWritesTheNamedRepository(t *testing.T) {
	t.Parallel()
	s := NewSandbox(t)
	other := s.path("other")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if r := s.Run(other, "", nil, "git", "init", "-q"); r.Code != 0 {
		t.Fatalf("git init: %s", r.Output())
	}
	src := s.path(noVarHook + ".py")
	if err := os.WriteFile(src, []byte(noVarSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	s.MustSteer("-C", other, "register", src, "--rule", noVarRule, "--all-harnesses")
	s.MustSteer("-C", other, "hooks", "sync", "--scope", "project")
	s.MustSteer("-C", other, "check", "init")

	if !fileExists(filepath.Join(other, ".steer", "hooks", noVarHook+".py")) {
		t.Error("the guard is not in the named repository's store")
	}
	if c := s.RegisteredCommand(filepath.Join(other, ".claude", "settings.json"), "steer-hooks.py"); !strings.Contains(c, "steer-hooks.py") {
		t.Errorf("the named repository's Claude Code registration = %q", c)
	}
	for _, doc := range []string{"CLAUDE.md", "AGENTS.md"} {
		data, err := os.ReadFile(filepath.Join(other, doc))
		if err != nil || !strings.Contains(string(data), "- "+noVarRule) {
			t.Errorf("the named repository's %s does not carry the rule (%v):\n%s", doc, err, data)
		}
	}
	if !fileExists(filepath.Join(other, "check.sh")) {
		t.Error("check init wrote no check.sh in the named repository")
	}
	if out := s.Git("status", "--porcelain", "-uall"); out != "" {
		t.Errorf("the repository the commands ran from changed:\n%s", out)
	}

	var status struct {
		Checks struct {
			Repo string `json:"repo"`
		} `json:"checks"`
		Hooks []struct {
			Name  string `json:"name"`
			Scope string `json:"scope"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(s.MustSteer("-C", other, "status", "--json").Stdout), &status); err != nil {
		t.Fatal(err)
	}
	if status.Checks.Repo != other {
		t.Errorf("status checks.repo = %q, want %s", status.Checks.Repo, other)
	}
	found := false
	for _, h := range status.Hooks {
		found = found || (h.Name == noVarHook && h.Scope == "project")
	}
	if !found {
		t.Errorf("status hooks = %+v, want the named repository's guard", status.Hooks)
	}

	var changed struct {
		Repo  string   `json:"repo"`
		Paths []string `json:"paths"`
	}
	if err := json.Unmarshal([]byte(s.MustSteer("-C", other, "uncommitted", "--json").Stdout), &changed); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"AGENTS.md", "CLAUDE.md", ".claude/settings.json", ".codex/hooks.json", "check.sh"} {
		if changed.Repo != other || !slices.Contains(changed.Paths, want) {
			t.Errorf("uncommitted = %+v, want %s", changed, want)
		}
	}
	if r := s.Steer("-C", s.path("missing"), "status"); r.Code == 0 || !strings.Contains(r.Output(), "no such directory") {
		t.Errorf("-C to a missing directory exited %d:\n%s", r.Code, r.Output())
	}
}
