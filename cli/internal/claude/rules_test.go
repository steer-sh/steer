package claude

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An instruction rule reaches the agent's document the same way a guard's rule
// does, because the derived section cannot tell them apart.
func TestAnInstructionRuleReachesTheDocument(t *testing.T) {
	withProjectTempDir(t)

	if _, err := InstallManagedRule(ProjectScope, "config-path",
		"resolve a config path from the repo root"); err != nil {
		t.Fatal(err)
	}
	if body := readRules(t, ProjectScope); !strings.Contains(body, "- resolve a config path from the repo root") {
		t.Fatalf("document = %q", body)
	}

	if _, err := RemoveManagedRule(ProjectScope, "config-path"); err != nil {
		t.Fatal(err)
	}
	if body := readRules(t, ProjectScope); strings.Contains(body, "resolve a config path") {
		t.Fatalf("removal left the rule: %q", body)
	}
}

// The store keeps the pre-image, so a later read recomputes the digest rather
// than trusting a stamp. Rewording changes it; the created date does not move.
func TestAnInstructionRuleCarriesItsDigestAndItsPreImage(t *testing.T) {
	withProjectTempDir(t)

	if _, err := InstallManagedRule(ProjectScope, "config-path", "  first   wording  "); err != nil {
		t.Fatal(err)
	}
	rules, err := ListManagedRules(ProjectScope)
	if err != nil || len(rules) != 1 {
		t.Fatalf("rules = %#v, err = %v", rules, err)
	}
	first := rules[0]
	if first.Text != "first   wording" {
		t.Fatalf("text = %q — the pre-image is trimmed, not collapsed", first.Text)
	}
	if first.RuleHash != HashRule(first.Text) {
		t.Fatalf("rule_hash = %s, recomputed = %s", first.RuleHash, HashRule(first.Text))
	}

	result, err := InstallManagedRule(ProjectScope, "config-path", "second wording")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Overwritten || result.PreviousText != "first   wording" {
		t.Fatalf("result = %#v", result)
	}
	rules, _ = ListManagedRules(ProjectScope)
	if rules[0].RuleHash == first.RuleHash {
		t.Fatal("rewording left the digest standing")
	}
	if rules[0].Created != first.Created {
		t.Fatalf("created moved on a rewording: %s -> %s", first.Created, rules[0].Created)
	}
}

// One id names one artifact. A rule taking a guard's name would make a record
// naming that id unable to say which text its digest was taken over.
func TestARuleIDCannotTakeAGuardsName(t *testing.T) {
	withProjectTempDir(t)
	installTestHook(t, "no-bare-python", "use python3 instead")

	if _, err := InstallManagedRule(ProjectScope, "no-bare-python", "some prose"); err == nil ||
		!strings.Contains(err.Error(), "already names a guard") {
		t.Fatalf("err = %v", err)
	}
}

// A store holding only instruction rules registers no master: nothing runs at a
// tool call, so a registration would name a master over an empty guard set.
func TestRulesAloneRegisterNoMaster(t *testing.T) {
	withProjectTempDir(t)

	if _, err := InstallManagedRule(ProjectScope, "config-path", "a rule"); err != nil {
		t.Fatal(err)
	}
	hooksDir, err := GetHooksDir(ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(hooksDir, MasterHookName)); !os.IsNotExist(err) {
		t.Fatalf("a master exists for a store holding no guard: %v", err)
	}
	registrar := Registrar{}
	registered, err := registrar.MasterRegistered(ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	if registered {
		t.Fatal("the master is registered for a store holding no guard")
	}
}

// Guards and rules sort into one section by the id that names each, so a second
// sync over identical state writes identical bytes whichever source supplied a line.
func TestGuardsAndRulesShareOneOrderedSection(t *testing.T) {
	withProjectTempDir(t)

	installTestHook(t, "b-guard", "the guard rule")
	if _, err := InstallManagedRule(ProjectScope, "a-rule", "the instruction rule"); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallManagedRule(ProjectScope, "c-rule", "the later rule"); err != nil {
		t.Fatal(err)
	}

	rules, err := ManifestRules(ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"the instruction rule", "the guard rule", "the later rule"}
	if len(rules) != len(want) {
		t.Fatalf("rules = %#v", rules)
	}
	for i := range want {
		if rules[i] != want[i] {
			t.Fatalf("rules = %#v, want %#v", rules, want)
		}
	}
}

// Removing an id the store no longer names runs the sync first: that is the retry
// for a removal that stopped part way through the targets.
func TestRemovingAnUnknownRuleSyncsBeforeItReports(t *testing.T) {
	withProjectTempDir(t)

	installTestHook(t, "a-guard", "the guard rule")
	path, err := GetClaudeMDFile(ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("## Steer Rules\n<!-- managed-by-steer -->\n- a stranded line\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := RemoveManagedRule(ProjectScope, "no-such-rule"); err == nil ||
		!strings.Contains(err.Error(), "rule not found") {
		t.Fatalf("err = %v", err)
	}
	body := readRules(t, ProjectScope)
	if strings.Contains(body, "a stranded line") || !strings.Contains(body, "- the guard rule") {
		t.Fatalf("the sync did not re-derive the section: %q", body)
	}
}

// A store that held a rule and no longer does writes the bytes it wrote before any
// rule existed, so this commit moves no manifest an operator already has.
func TestAStoreWithNoRulesWritesNoRulesKey(t *testing.T) {
	withProjectTempDir(t)

	installTestHook(t, "a-guard", "the guard rule")
	before, err := os.ReadFile(mustManifestPath(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := InstallManagedRule(ProjectScope, "a-rule", "prose"); err != nil {
		t.Fatal(err)
	}
	if _, err := RemoveManagedRule(ProjectScope, "a-rule"); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(mustManifestPath(t))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("manifest moved:\nbefore %s\nafter  %s", before, after)
	}
}

func mustManifestPath(t *testing.T) string {
	t.Helper()
	path, err := GetManifestFile(ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

// The refusal is on both write boundaries. A guard installed over a rule's id
// would leave the derived section carrying one of the two texts.
func TestAGuardCannotTakeARuleID(t *testing.T) {
	withProjectTempDir(t)

	if _, err := InstallManagedRule(ProjectScope, "config-path", "the instruction rule"); err != nil {
		t.Fatal(err)
	}
	content := []byte("#!/usr/bin/env python3\nimport sys\n\ndef main():\n    sys.exit(0)\n")
	_, err := InstallManagedHook(ProjectScope, InstallManagedHookInput{
		HookName: "config-path",
		Rule:     "the guard rule",
		Content:  content,
		Metadata: HookInstallMetadata{Source: SourceGenerated},
	})
	if err == nil || !strings.Contains(err.Error(), "already names an instruction rule") {
		t.Fatalf("err = %v", err)
	}
}

// Only a hand-edited manifest reaches a collision. It yields both lines rather
// than losing one, and the order is total whichever map the reader ranged first.
func TestAHandEditedCollisionLosesNoText(t *testing.T) {
	withProjectTempDir(t)

	if err := SaveManifest(ProjectScope, &Manifest{
		Hooks: map[string]ManifestEntry{"config-path": {File: "config-path.py", Rule: "the guard rule"}},
		Rules: map[string]RuleEntry{"config-path": {Text: "the instruction rule"}},
	}); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 5; i++ {
		rules, err := ManifestRules(ProjectScope)
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"the guard rule", "the instruction rule"}
		if len(rules) != len(want) {
			t.Fatalf("rules = %#v", rules)
		}
		for j := range want {
			if rules[j] != want[j] {
				t.Fatalf("rules = %#v, want %#v", rules, want)
			}
		}
	}
}
