package mine

import (
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/gate"
)

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
	t.Setenv("HOME", home)
	t.Chdir(t.TempDir())

	err := Register(io.Discard, stateDir, RegisterOpts{
		Prose: testProse, ScopeAll: true, Ledger: ledger,
		Class: "widget parser flag loss", Match: "widget parser",
		Tier: "instruction", Target: "CLAUDE.md", RuleID: "config-path",
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
	t.Setenv("HOME", home)
	t.Chdir(t.TempDir())
	writeRuleStore(t, filepath.Join(home, ".steer", "hooks"), "config-path",
		"resolve a config path from the repo root")

	if err := Register(io.Discard, stateDir, RegisterOpts{
		Prose: testProse, ScopeAll: true, Ledger: ledger,
		Class: "widget parser flag loss", Match: "widget parser",
		Tier: "instruction", Target: "CLAUDE.md", RuleID: "config-path",
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
