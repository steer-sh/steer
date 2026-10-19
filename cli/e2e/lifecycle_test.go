//go:build e2e

package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixtureBinding reads one guard's fixture binding from the project manifest.
func (s *Sandbox) fixtureBinding(hook string) (binding map[string]any, ruleHash string) {
	s.t.Helper()
	data, err := os.ReadFile(filepath.Join(s.Repo, ".steer", "hooks", "steer-manifest.json"))
	if err != nil {
		s.t.Fatal(err)
	}
	var m struct {
		Hooks map[string]struct {
			RuleHash string         `json:"rule_hash"`
			Fixtures map[string]any `json:"fixtures"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &m); err != nil {
		s.t.Fatalf("manifest: %v", err)
	}
	entry, ok := m.Hooks[hook]
	if !ok {
		s.t.Fatalf("the manifest holds no %s entry:\n%s", hook, data)
	}
	return entry.Fixtures, entry.RuleHash
}

// TestFixtureBindingsAcrossTheGuardLifecycle drives Suite A step 11: each operation
// that rewrites the master reruns the stored cases, so a binding is re-earned under
// the new master and rule, and a failing case is reported without stopping a rebuild
// or a removal.
//
// pins existing behaviour
func TestFixtureBindingsAcrossTheGuardLifecycle(t *testing.T) {
	t.Parallel()
	s := NewSandbox(t)
	src := s.path(noVarHook + ".py")
	if err := os.WriteFile(src, []byte(noVarSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	s.MustSteer("register", src, "--rule", noVarRule, "--harness", "claude-code", "--fixtures", s.writeNoVarFixtures(false))
	first, firstRule := s.fixtureBinding(noVarHook)
	if first["tested"] != true || first["rule_hash"] != firstRule {
		t.Fatalf("registration did not bind the passing cases to the rule: %v", first)
	}

	other := s.path("no-eval.py")
	if err := os.WriteFile(other, []byte(strings.ReplaceAll(noVarSrc, "var ", "eval ")), 0o644); err != nil {
		t.Fatal(err)
	}
	s.MustSteer("register", other, "--rule", "never use eval | call it directly", "--harness", "claude-code")
	consolidated, _ := s.fixtureBinding(noVarHook)
	if consolidated["tested"] != true || consolidated["master_digest"] == first["master_digest"] {
		t.Errorf("consolidating a second guard did not re-earn the binding under the new master: %v -> %v", first, consolidated)
	}

	if r := s.MustSteer("hooks", "rebuild", "--scope", "project"); !strings.Contains(r.Output(), "fixtures: no-var-hook — 2 case(s) intercepted") {
		t.Errorf("rebuild did not report the rerun:\n%s", r.Output())
	}
	if rebuilt, _ := s.fixtureBinding(noVarHook); rebuilt["tested"] != true || rebuilt["master_digest"] != consolidated["master_digest"] {
		t.Errorf("an unchanged rebuild moved the binding: %v -> %v", consolidated, rebuilt)
	}

	s.MustSteer("register", src, "--rule", "never use var | use const", "--harness", "claude-code")
	reworded, rule := s.fixtureBinding(noVarHook)
	if reworded["tested"] != true || reworded["rule_hash"] != rule || rule == firstRule {
		t.Errorf("re-registering with a new rule did not rebind the cases to it: %v (rule %s, was %s)", reworded, rule, firstRule)
	}

	stored := filepath.Join(s.Repo, ".steer", "hooks", noVarHook+".py")
	if err := os.WriteFile(stored, []byte(strings.ReplaceAll(noVarSrc, `"var "`, `"never-matches "`)), 0o644); err != nil {
		t.Fatal(err)
	}
	const failing = "fixtures: no-var-hook — NOT verified"
	if r := s.MustSteer("hooks", "rebuild", "--scope", "project"); !strings.Contains(r.Output(), failing) {
		t.Errorf("rebuild did not report the failing case:\n%s", r.Output())
	}
	if broken, _ := s.fixtureBinding(noVarHook); broken["tested"] != false || broken["hook_digest"] == reworded["hook_digest"] {
		t.Errorf("a guard that no longer blocks kept its tested binding: %v", broken)
	}

	if r := s.MustSteer("remove", "no-eval", "--force"); !strings.Contains(r.Output(), failing) {
		t.Errorf("removing another guard did not report the failing case:\n%s", r.Output())
	}
	s.MustSteer("remove", noVarHook, "--force")
	if fileExists(filepath.Join(s.Repo, ".steer", "hooks", "fixtures", noVarHook+".json")) {
		t.Errorf("the removed guard's fixtures stayed in the store")
	}
	archived, _ := filepath.Glob(filepath.Join(s.Repo, ".steer", "hooks", "archive", noVarHook, "*", "fixtures.json"))
	if len(archived) != 1 {
		t.Errorf("the archive holds %d fixture files for the removed guard, want 1", len(archived))
	}
}
