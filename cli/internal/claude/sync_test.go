package claude

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/harness"
	"github.com/steer-sh/steer-cli/internal/instructions"
)

// failingRegistrar is a Claude Code registrar that stops at the instruction
// document, which is where an install or a removal loses the rule text it can no
// longer look up.
type failingRegistrar struct {
	Registrar
	fail bool
}

func (r *failingRegistrar) SetRules(rules []string, scope string) error {
	if r.fail {
		return fmt.Errorf("interrupted")
	}
	return r.Registrar.SetRules(rules, scope)
}

// agentsRegistrar stands in for a second harness whose document is AGENTS.md in
// the working directory.
type agentsRegistrar struct{ Registrar }

func (agentsRegistrar) InstructionPath(string) (string, error) { return "AGENTS.md", nil }

func (r agentsRegistrar) SetRules(rules []string, scope string) error {
	path, _ := r.InstructionPath(scope)
	return instructions.SetRules(path, rules, "")
}

// A harness installed after the rules were written never received them, and a
// hand edit can take a rule out of a document that did. Both read as missing
// until a sync over every target writes the section again.
func TestRulesPresenceReadsTheSectionAgainstTheManifest(t *testing.T) {
	withProjectTempDir(t)

	if _, err := InstallManagedRule(ProjectScope, "config-path", "resolve a config path from the repo root"); err != nil {
		t.Fatal(err)
	}
	installTestHook(t, "no-bare-python", "use python3 instead")

	late := agentsRegistrar{}
	got, err := TargetRulesPresence(ProjectScope, late)
	if err != nil {
		t.Fatal(err)
	}
	if got.Expected != 2 || strings.Join(got.Missing, ",") != "config-path,no-bare-python" || !got.Drifted() {
		t.Fatalf("late harness presence = %#v", got)
	}

	path, _ := GetClaudeMDFile(ProjectScope)
	edited := strings.Replace(readRules(t, ProjectScope),
		"- resolve a config path from the repo root", "- a line written by hand", 1)
	if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err = TargetRulesPresence(ProjectScope, Registrar{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got.Missing, ",") != "config-path" || strings.Join(got.Extra, ",") != "a line written by hand" {
		t.Fatalf("hand-edited presence = %#v", got)
	}

	if err := SyncTargets(ProjectScope, Registrar{}, late); err != nil {
		t.Fatal(err)
	}
	for _, target := range []harness.Registrar{Registrar{}, late} {
		got, err := TargetRulesPresence(ProjectScope, target)
		if err != nil {
			t.Fatal(err)
		}
		if got.Drifted() || got.Expected != 2 {
			t.Fatalf("%s after sync = %#v", got.Path, got)
		}
	}
}

func readRules(t *testing.T, scope string) string {
	t.Helper()
	path, err := GetClaudeMDFile(scope)
	if err != nil {
		t.Fatalf("GetClaudeMDFile: %v", err)
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func installTestHook(t *testing.T, name, rule string, targets ...harness.Registrar) {
	t.Helper()
	content := []byte("#!/usr/bin/env python3\nimport sys\n\ndef main():\n    sys.exit(0)\n\nif __name__ == '__main__':\n    main()\n")
	if _, err := InstallManagedHook(ProjectScope, InstallManagedHookInput{
		HookName: name,
		Rule:     rule,
		Content:  content,
		Metadata: HookInstallMetadata{Source: SourceGenerated},
	}, targets...); err != nil {
		t.Fatalf("InstallManagedHook(%s): %v", name, err)
	}
}

// An install that replaces a rule and then fails must not leave the old rule
// behind on the retry. The manifest already names the new rule, so a retry
// keyed on the old text would have nothing to look the old rule up by.
func TestReplacedRuleIsDroppedByTheRetry(t *testing.T) {
	withProjectTempDir(t)

	installTestHook(t, "guard", "rule A")
	if got := readRules(t, ProjectScope); !strings.Contains(got, "- rule A") {
		t.Fatalf("expected rule A in document, got:\n%s", got)
	}

	target := &failingRegistrar{fail: true}
	content := []byte("#!/usr/bin/env python3\nimport sys\n\ndef main():\n    sys.exit(0)\n\nif __name__ == '__main__':\n    main()\n")
	if _, err := InstallManagedHook(ProjectScope, InstallManagedHookInput{
		HookName: "guard",
		Rule:     "rule B",
		Content:  content,
		Metadata: HookInstallMetadata{Source: SourceGenerated},
	}, target); err == nil {
		t.Fatal("expected the interrupted install to fail")
	}

	target.fail = false
	installTestHook(t, "guard", "rule B", target)

	got := readRules(t, ProjectScope)
	if strings.Contains(got, "- rule A") {
		t.Fatalf("the replaced rule A survived the retry:\n%s", got)
	}
	if !strings.Contains(got, "- rule B") {
		t.Fatalf("expected rule B in document, got:\n%s", got)
	}
}

// A removal that deletes the manifest entry and then fails must still clean the
// instruction document on the retry, even though the retry can no longer resolve
// the hook.
func TestRemovedRuleIsCleanedByTheRetry(t *testing.T) {
	withProjectTempDir(t)

	installTestHook(t, "guard", "rule A")

	target := &failingRegistrar{fail: true}
	if _, err := RemoveManagedHook(ProjectScope, "guard", RemoveOptions{}, target); err == nil {
		t.Fatal("expected the interrupted removal to fail")
	}
	if got := readRules(t, ProjectScope); !strings.Contains(got, "- rule A") {
		t.Fatalf("expected the interrupted removal to leave rule A, got:\n%s", got)
	}

	target.fail = false
	_, err := RemoveManagedHook(ProjectScope, "guard", RemoveOptions{}, target)
	if err == nil || !strings.Contains(err.Error(), "hook not found") {
		t.Fatalf("expected a hook-not-found retry, got: %v", err)
	}

	if got := readRules(t, ProjectScope); strings.Contains(got, "- rule A") {
		t.Fatalf("the removed rule survived the retry:\n%s", got)
	}
}

func TestSyncTargetsDerivesTheSectionFromTheManifest(t *testing.T) {
	withProjectTempDir(t)

	installTestHook(t, "b-guard", "rule B")
	installTestHook(t, "a-guard", "rule A")

	path, err := GetClaudeMDFile(ProjectScope)
	if err != nil {
		t.Fatalf("GetClaudeMDFile: %v", err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	// A stale line inside the section is not the manifest's, so a sync drops it.
	stale := strings.Replace(string(before), "- rule A", "- rule A\n- stale rule", 1)
	if err := os.WriteFile(path, []byte(stale), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}

	if err := SyncTargets(ProjectScope); err != nil {
		t.Fatalf("SyncTargets: %v", err)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(after) != string(before) {
		t.Fatalf("sync did not restore the derived section:\nwant:\n%s\ngot:\n%s", before, after)
	}
	if strings.Index(string(after), "- rule A") > strings.Index(string(after), "- rule B") {
		t.Fatalf("expected the section ordered by hook name:\n%s", after)
	}
}
