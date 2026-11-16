//go:build e2e

package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// operatorSettings is a Claude Code settings file that holds nothing of Steer's.
const operatorSettings = `{"statusLine":{"type":"command","command":"my-line"},` +
	`"hooks":{"Stop":[{"hooks":[{"type":"command","command":"my-stop"}]}]}}`

// pruneEmpty drops the empty objects and arrays a removal can leave behind, which a
// harness reads the same as absent keys.
func pruneEmpty(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, child := range t {
			child = pruneEmpty(child)
			if m, ok := child.(map[string]any); ok && len(m) == 0 {
				continue
			}
			if a, ok := child.([]any); ok && len(a) == 0 {
				continue
			}
			out[k] = child
		}
		return out
	case []any:
		out := []any{}
		for _, child := range t {
			out = append(out, pruneEmpty(child))
		}
		return out
	}
	return v
}

// steerMarkers are substrings only a Steer registration carries.
var steerMarkers = []string{"commit-gate", "check hook", "steer-hooks", "statusline"}

func (s *Sandbox) homeFile(rel string) string {
	s.t.Helper()
	data, err := os.ReadFile(filepath.Join(s.Home, rel))
	if err != nil {
		s.t.Fatal(err)
	}
	return string(data)
}

// TestUninstallRemovesEveryGlobalRegistration installs every global Steer channel
// into both harnesses through the CLI, beside an operator's status line, Stop hook
// and instruction prose, and requires the plan to change nothing, --apply to leave
// only the operator's entries, and --purge to delete ~/.steer.
//
// pins existing behaviour
func TestUninstallRemovesEveryGlobalRegistration(t *testing.T) {
	t.Parallel()
	s := NewSandbox(t)
	s.Write("../home/.claude/CLAUDE.md", "Claude prose.\n")
	s.Write("../home/.codex/AGENTS.md", "Codex prose.\n")
	s.Write("../home/.claude/settings.json", operatorSettings+"\n")
	src := s.path(noVarHook + ".py")
	if err := os.WriteFile(src, []byte(noVarSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	s.MustSteer("register", src, "--rule", noVarRule, "--all-harnesses", "--scope", "global")
	s.MustSteer("rules", "add", "config-path", "--rule", "resolve a config path from the repo root", "--scope", "global")
	for _, h := range []string{"claude-code", "codex"} {
		s.MustSteer("check", "install", "--harness", h, "--scope", "global")
		s.MustSteer("commit-gate", "install", "--harness", h, "--scope", "global")
	}
	s.MustSteer("statusline", "install", "--scope", "global", "--wrap")

	files := []string{".claude/settings.json", ".codex/hooks.json", ".claude/CLAUDE.md", ".codex/AGENTS.md"}
	before := map[string]string{}
	for _, rel := range files {
		before[rel] = s.homeFile(rel)
	}
	plan := s.MustSteer("uninstall")
	for _, want := range []string{"Claude Code, global scope: commit gate", "Codex, global scope: commit gate",
		"check runner", "guard runner", "Steer Rules section", "gives back: my-line", "steer uninstall --apply"} {
		if !strings.Contains(plan.Stdout, want) {
			t.Errorf("the plan does not name %q:\n%s", want, plan.Stdout)
		}
	}
	for _, rel := range files {
		if s.homeFile(rel) != before[rel] {
			t.Errorf("the plan without --apply rewrote ~/%s", rel)
		}
	}

	s.MustSteer("uninstall", "--apply")
	for _, rel := range []string{".claude/settings.json", ".codex/hooks.json"} {
		text := s.homeFile(rel)
		for _, marker := range steerMarkers {
			if strings.Contains(text, marker) {
				t.Errorf("~/%s still names %q after uninstall:\n%s", rel, marker, text)
			}
		}
	}
	var got, want any
	if err := json.Unmarshal([]byte(s.homeFile(".claude/settings.json")), &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(operatorSettings), &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(pruneEmpty(got), want) {
		t.Errorf("~/.claude/settings.json after uninstall = %v, want the operator's own %v", got, want)
	}
	for rel, prose := range map[string]string{".claude/CLAUDE.md": "Claude prose.\n", ".codex/AGENTS.md": "Codex prose.\n"} {
		if text := s.homeFile(rel); text != prose {
			t.Errorf("~/%s after uninstall = %q, want %q", rel, text, prose)
		}
	}
	if again := s.MustSteer("uninstall"); !strings.Contains(again.Stdout, "no global registration to remove") {
		t.Errorf("a second plan still finds registrations:\n%s", again.Stdout)
	}
	if !fileExists(filepath.Join(s.Home, ".steer", "bin")) {
		t.Error("uninstall without --purge deleted ~/.steer")
	}

	s.MustSteer("uninstall", "--apply", "--purge")
	if fileExists(filepath.Join(s.Home, ".steer")) {
		t.Error("uninstall --purge left ~/.steer")
	}
}

// TestRemovedPluginNoticeNamesUninstall installs the stable executable from a
// Claude Code plugin copy, arms the gate, removes the plugin's install record, and
// requires the status line to name the uninstall command until it runs.
//
// pins existing behaviour
func TestRemovedPluginNoticeNamesUninstall(t *testing.T) {
	t.Parallel()
	s := NewSandbox(t)
	root := filepath.Join(s.Home, ".claude", "plugins", "cache", "m", "steer", "0.1.0")
	copyBin := filepath.Join(root, "bin", runtime.GOOS+"-"+runtime.GOARCH, exe("steer"))
	data, err := os.ReadFile(steerBin)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(copyBin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(copyBin, data, 0o755); err != nil {
		t.Fatal(err)
	}
	record := filepath.Join(s.Home, ".claude", "plugins", "installed_plugins.json")
	s.Write("../home/.claude/plugins/installed_plugins.json",
		`{"plugins":{"steer@m":[{"installPath":"`+root+`"}]}}`+"\n")

	if r := s.Run(s.Repo, "", nil, copyBin, "bin", "install"); r.Code != 0 {
		t.Fatalf("bin install from the plugin copy exited %d:\n%s", r.Code, r.Output())
	}
	s.MustSteer("commit-gate", "install", "--harness", "claude-code", "--scope", "global")
	stable := filepath.Join(s.Home, ".steer", "bin", exe("steer"))
	statusline := func() string {
		r := s.Run(s.Repo, "", nil, stable, "statusline")
		if r.Code != 0 {
			t.Fatalf("the stable executable's statusline exited %d:\n%s", r.Code, r.Output())
		}
		return r.Stdout
	}
	if row := statusline(); strings.Contains(row, "plugin removed") {
		t.Fatalf("an installed plugin reads as removed: %s", row)
	}

	if err := os.WriteFile(record, []byte(`{"plugins":{}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	row := statusline()
	if !strings.Contains(row, "Claude Code plugin removed") || !strings.Contains(row, "uninstall --harness claude-code --apply") {
		t.Errorf("the status line does not name the removed plugin: %s", row)
	}

	s.MustSteer("uninstall", "--harness", "claude-code", "--apply")
	if row := statusline(); strings.Contains(row, "plugin removed") {
		t.Errorf("the notice outlived the uninstall: %s", row)
	}
}
