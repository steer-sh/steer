package mine

import (
	"os"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/gate"
	"github.com/steer-sh/steer-cli/internal/testhome"
)

// evidenceIDs is each entry's unit identity: a chain's start_ts or a session's ID,
// with "+r" appended when the entry is marked recomputed.
func evidenceIDs(rec Record) []string {
	var out []string
	for _, raw := range asList(rec[EvidenceKey]) {
		e := raw.(map[string]any)
		id := numString(e["start_ts"])
		if sid := str(e["session_id"]); sid != "" {
			id = sid
		}
		if truthy(e["recomputed"]) {
			id += "+r"
		}
		out = append(out, id)
	}
	return out
}

// TestRegisterFreezesOneEvidenceEntryPerBaselineHit checks that each engine lists the
// baseline units the regex matched, and no unit outside the scope or after the cutoff.
func TestRegisterFreezesOneEvidenceEntryPerBaselineHit(t *testing.T) {
	testhome.Set(t, t.TempDir())
	t.Chdir(t.TempDir())
	const cutoff = 2000

	var rows []gate.Row
	for i, h := range []string{"codex", "claude-code", "codex"} {
		rows = append(rows, harnessChain(1000+i*100, h, "widget parser drops a flag")...)
	}
	rows = append(rows, harnessChain(1500, "codex", "unrelated naming nit")...)
	rows = append(rows, harnessChain(cutoff+100, "codex", "widget parser drops a flag")...)
	stateDir, ledger := writeLedger(t, rows)
	registeredBaseline(t, stateDir, RegisterOpts{Ledger: ledger, Match: "widget parser",
		Kind: "defect", ScopeFlags: ScopeFlags{ScopeHarness: []string{"codex"}}, InstalledTS: cutoff})
	recs, _ := Load(stateDir)
	check(t, "chain evidence", evidenceIDs(recs[0]), []string{"1000", "1200"})

	hit := friction(1200, "codex", "proj", "bare python here")
	hit.Host = "mini"
	hit.Sources = []SessionSource{{Signals: hit.Signals}, {Path: "/t/s1200.jsonl", Signals: hit.Signals}}
	units := []SessionUnit{
		friction(1000, "codex", "proj", "an unrelated correction"),
		hit,
		friction(1300, "claude-code", "proj", "bare python here"),
		friction(cutoff+500, "codex", "proj", "bare python here"),
	}
	sessDir := t.TempDir()
	registeredBaseline(t, sessDir, RegisterOpts{Match: "bare python", Corpus: CorpusTranscript,
		Sessions: units, ScopeFlags: ScopeFlags{ScopeHarness: []string{"codex"}}, InstalledTS: cutoff})
	recs, _ = Load(sessDir)
	check(t, "session evidence", evidenceIDs(recs[0]), []string{"s1200"})
	entry := asList(recs[0][EvidenceKey])[0].(map[string]any)
	check(t, "session host", str(entry["host"]), "mini")
	sig := asList(entry["signals"])[0].(map[string]any)
	check(t, "signal locator", str(sig["path"]), "/t/s1200.jsonl")
}

// TestBackfillEvidenceMarksEntriesRecomputedAndWritesOnlyWithApply checks that an
// older record without an evidence list gains one only under Apply, with every entry
// marked recomputed; that a later run adds only new units to it; and that a record
// whose writer froze its list is left alone.
func TestBackfillEvidenceMarksEntriesRecomputedAndWritesOnlyWithApply(t *testing.T) {
	testhome.Set(t, t.TempDir())
	t.Chdir(t.TempDir())
	var rows []gate.Row
	rows = append(rows, harnessChain(1000, "codex", "widget parser drops a flag")...)
	rows = append(rows, harnessChain(1100, "codex", "widget parser drops a flag")...)
	stateDir, ledger := writeLedger(t, rows)
	for _, match := range []string{"widget parser", "drops a flag"} {
		registeredBaseline(t, stateDir, RegisterOpts{Ledger: ledger, Match: match,
			Kind: "defect", ScopeFlags: ScopeFlags{ScopeAll: true}, InstalledTS: 2000})
	}
	recs, _ := Load(stateDir)
	delete(recs[0], EvidenceKey)
	if err := save(stateDir, recs); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(InterventionsPath(stateDir))

	var out strings.Builder
	if err := BackfillEvidence(&out, stateDir, BackfillEvidenceOpts{Ledger: ledger}); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(InterventionsPath(stateDir))
	check(t, "preview writes nothing", string(after), string(before))
	if !strings.Contains(out.String(), "2 unit(s) in the before-window match now, 2 new") {
		t.Errorf("preview names the count found; got:\n%s", out.String())
	}

	if err := BackfillEvidence(&out, stateDir, BackfillEvidenceOpts{Ledger: ledger, Apply: true}); err != nil {
		t.Fatal(err)
	}
	recs, _ = Load(stateDir)
	check(t, "backfilled record", evidenceIDs(recs[0]), []string{"1000+r", "1100+r"})
	check(t, "record frozen at register", evidenceIDs(recs[1]), []string{"1000", "1100"})

	rows = append(rows, harnessChain(1200, "codex", "widget parser drops a flag")...)
	_, ledger = writeLedger(t, rows)
	if err := BackfillEvidence(&out, stateDir, BackfillEvidenceOpts{Ledger: ledger, Apply: true}); err != nil {
		t.Fatal(err)
	}
	recs, _ = Load(stateDir)
	check(t, "a later backfill adds only the new unit", evidenceIDs(recs[0]), []string{"1000+r", "1100+r", "1200+r"})
	check(t, "a frozen list stays closed", evidenceIDs(recs[1]), []string{"1000", "1100"})
}

// TestScopeSetClosesABackfilledList checks that a scope migration freezes the list it
// writes, so a later backfill cannot add to it.
func TestScopeSetClosesABackfilledList(t *testing.T) {
	testhome.Set(t, t.TempDir())
	t.Chdir(t.TempDir())
	stateDir, ledger := writeLedger(t, harnessChain(1000, "codex", "widget parser drops a flag"))
	registeredBaseline(t, stateDir, RegisterOpts{Ledger: ledger, Match: "widget parser",
		Kind: "defect", ScopeFlags: ScopeFlags{ScopeAll: true}, InstalledTS: 2000})
	recs, _ := Load(stateDir)
	delete(recs[0], "scope")
	delete(recs[0], EvidenceKey)
	if err := save(stateDir, recs); err != nil {
		t.Fatal(err)
	}
	if err := BackfillEvidence(discard{}, stateDir, BackfillEvidenceOpts{Ledger: ledger, Apply: true}); err != nil {
		t.Fatal(err)
	}
	if err := ScopeSet(discard{}, stateDir, str(recs[0]["id"]), ScopeSetOpts{Ledger: ledger,
		ScopeFlags: ScopeFlags{ScopeAll: true}}); err != nil {
		t.Fatal(err)
	}
	recs, _ = Load(stateDir)
	check(t, "scope set freezes the list", evidenceIDs(recs[0]), []string{"1000"})
	check(t, "and closes it", truthy(recs[0][backfilledKey]), false)
}
