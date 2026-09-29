package mine

import (
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/gate"
	"github.com/steer-sh/steer-cli/internal/testhome"
)

// testNotMechanizable is the argument an instruction registration carries.
const testNotMechanizable = "the flag is dropped in prose the parser never sees, " +
	"so no repository check and no pre-tool guard reads it"

// registerRuleFixture is a ledger a class matches twelve times, so a registration
// fails on its tier rather than on an empty baseline.
func registerRuleFixture(t *testing.T) (stateDir, ledger string) {
	t.Helper()
	var rows []gate.Row
	ts := 1000
	for i := 0; i < 12; i++ {
		rows = append(rows, row(ts, "deny"),
			concerns(row(ts+1, "retry"), "round", c("widget parser drops a flag")))
		ts += 100
	}
	return writeLedger(t, rows)
}

// An instruction record names the rule its digest was taken over. A record naming
// only a document names no text inside it, which is the state this commit ends.
func TestAnInstructionTierNeedsARuleID(t *testing.T) {
	stateDir, ledger := registerRuleFixture(t)

	err := Register(io.Discard, stateDir, RegisterOpts{
		Prose: testProse, ScopeAll: true, Ledger: ledger,
		Class: "widget parser flag loss", Match: "widget parser",
		Tier: "instruction", Target: "CLAUDE.md",
	})
	if err == nil || !strings.Contains(err.Error(), "--rule-id") {
		t.Fatalf("err = %v", err)
	}
}

// Both spellings of the rung reach the same refusal, because both reach a stored
// record.
func TestBothInstructionSpellingsNeedARuleID(t *testing.T) {
	stateDir, ledger := registerRuleFixture(t)

	for _, spelling := range []string{"instruction", "prose"} {
		err := Register(io.Discard, stateDir, RegisterOpts{
			Prose: testProse, ScopeAll: true, Ledger: ledger,
			Class: "widget parser flag loss", Match: "widget parser",
			Tier: spelling, Target: "CLAUDE.md",
		})
		if err == nil || !strings.Contains(err.Error(), "--rule-id") {
			t.Fatalf("tier %q: err = %v", spelling, err)
		}
	}
}

// A rule id the store does not name is refused. The guard tier warns and records
// instead, because a guard's audit rows can name a hand-written hook the manifest
// never held; a rule exists nowhere but the manifest, so absence is a real error.
func TestAnUnknownRuleIDIsRefused(t *testing.T) {
	stateDir, ledger := registerRuleFixture(t)
	home := t.TempDir()
	testhome.Set(t, home)
	t.Chdir(t.TempDir())

	err := Register(io.Discard, stateDir, RegisterOpts{
		Prose: testProse, ScopeAll: true, Ledger: ledger,
		Class: "widget parser flag loss", Match: "widget parser",
		Tier: "instruction", Target: "CLAUDE.md", RuleID: "config-path",
		NotMechanizable: testNotMechanizable,
	})
	if err == nil || !strings.Contains(err.Error(), "steer rules add") {
		t.Fatalf("err = %v", err)
	}
}

// The record freezes the text and its digest, so a later read can say whether the
// prose still says what the decision was made about.
func TestAnInstructionRecordFreezesItsRuleText(t *testing.T) {
	stateDir, ledger := registerRuleFixture(t)
	home := t.TempDir()
	testhome.Set(t, home)
	t.Chdir(t.TempDir())
	writeRuleStore(t, filepath.Join(home, ".steer", "hooks"), "config-path",
		"resolve a config path from the repo root")

	if err := Register(io.Discard, stateDir, RegisterOpts{
		Prose: testProse, ScopeAll: true, Ledger: ledger,
		Class: "widget parser flag loss", Match: "widget parser",
		Tier: "instruction", Target: "CLAUDE.md", RuleID: "config-path",
		NotMechanizable: testNotMechanizable,
	}); err != nil {
		t.Fatal(err)
	}

	recorded, _ := Load(stateDir)
	check(t, "one intervention recorded", len(recorded), 1)
	check(t, "rule id", str(recorded[0]["rule_id"]), "config-path")
	check(t, "rule text", str(recorded[0]["rule"]), "resolve a config path from the repo root")
	check(t, "rule scope", str(recorded[0]["rule_scope"]), "global")
	if str(recorded[0]["rule_hash"]) == "" {
		t.Fatal("no rule_hash on the record")
	}
}

// The instruction rung is the last one to reach for, so the record states why the
// two rungs above it cannot detect the class. The argument is made in a session
// today and lost there, and a record carrying it is checkable later.
func TestAnInstructionTierNeedsItsNotMechanizableArgument(t *testing.T) {
	stateDir, ledger := registerRuleFixture(t)
	home := t.TempDir()
	testhome.Set(t, home)
	t.Chdir(t.TempDir())
	writeRuleStore(t, filepath.Join(home, ".steer", "hooks"), "config-path",
		"resolve a config path from the repo root")

	opts := RegisterOpts{
		Prose: testProse, ScopeAll: true, Ledger: ledger,
		Class: "widget parser flag loss", Match: "widget parser",
		Tier: "instruction", Target: "CLAUDE.md", RuleID: "config-path",
	}
	for _, tc := range []struct{ name, value string }{
		{"blank", "  "},
		{"restates the class", "Widget Parser Flag Loss"},
	} {
		o := opts
		o.NotMechanizable = tc.value
		err := Register(io.Discard, stateDir, o)
		if err == nil || !strings.Contains(err.Error(), "--not-mechanizable") {
			t.Fatalf("%s: err = %v", tc.name, err)
		}
	}

	o := opts
	o.NotMechanizable = testNotMechanizable
	if err := Register(io.Discard, stateDir, o); err != nil {
		t.Fatal(err)
	}
	recorded, _ := Load(stateDir)
	check(t, "the argument is stored", str(recorded[0][NotMechanizableKey]), testNotMechanizable)
}

// Every rung but the instruction one installs a mechanical check, so none of them
// is asked to argue that a mechanical check is impossible.
func TestOtherTiersNeedNoNotMechanizableArgument(t *testing.T) {
	stateDir, ledger := registerRuleFixture(t)

	if err := Register(io.Discard, stateDir, RegisterOpts{
		Prose: testProse, ScopeAll: true, Ledger: ledger,
		Class: "widget parser flag loss", Match: "widget parser",
		Tier: "guard", HookID: "test-guard", Target: "steer-hooks.py",
	}); err != nil {
		t.Fatal(err)
	}
	recorded, _ := Load(stateDir)
	if _, ok := recorded[0][NotMechanizableKey]; ok {
		t.Fatalf("a check record carries %s", NotMechanizableKey)
	}
}
