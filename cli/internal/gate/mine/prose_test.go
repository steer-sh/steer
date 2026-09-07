package mine

import (
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/gate"
)

// testProse is the explanation every other test's write supplies. The writers refuse
// a blank, so a test about scoring still has to say what its class is.
var testProse = Prose{
	Problem:      "the fixture class occurs in the corpus under test",
	Intervention: "the fixture record stands in for an installed rule",
}

// A required field a blank satisfies is not required. These are the four answers the
// writers refuse and the one empty case they accept in words.
func TestWritersRefuseAnExplanationThatExplainsNothing(t *testing.T) {
	class := "stale test-count claim"
	cases := []struct {
		name  string
		prose Prose
		want  string
	}{
		{"no problem", Prose{Intervention: "bans a printed literal count"}, "--problem is required"},
		{"no intervention", Prose{Problem: "the count goes stale"}, "--intervention is required"},
		{"whitespace only", Prose{Problem: "   ", Intervention: "x"}, "--problem is required"},
		{"problem repeats the class", Prose{Problem: class, Intervention: "x"}, "repeats the class name"},
		{"intervention repeats the class", Prose{Problem: "x", Intervention: "  " + strings.ToUpper(class)},
			"repeats the class name"},
	}
	for _, tc := range cases {
		err := tc.prose.Validate(class)
		if err == nil {
			t.Errorf("%s: want a refusal, got none", tc.name)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: want %q in %q", tc.name, tc.want, err.Error())
		}
	}
	found := Prose{Problem: "the class recurs", Intervention: NoInterventionFound}
	if err := found.Validate(class); err != nil {
		t.Errorf("a rejection naming no intervention must be sayable: %v", err)
	}
}

// The refusal for a blank intervention names the sentence a rejection uses, because a
// rejection that installed nothing has no other way past the required field.
func TestTheBlankInterventionRefusalNamesTheRejectionForm(t *testing.T) {
	err := Prose{Problem: "the class recurs"}.Validate("some class")
	if err == nil || !strings.Contains(err.Error(), NoInterventionFound) {
		t.Fatalf("want the no-intervention sentence in the refusal, got %v", err)
	}
}

// A note written before the prose fields existed holds implementation detail, not a
// problem statement, so it is read back under its own label and promoted to neither.
func TestALegacyNoteIsReadBackUnderItsOwnLabel(t *testing.T) {
	p := ProseOf(Record{"note": "check.sh tier 6: bans a printed literal count"})
	check(t, "legacy note survives", p.LegacyNote, "check.sh tier 6: bans a printed literal count")
	check(t, "problem is not filled from the note", p.Problem, "")
	check(t, "intervention is not filled from the note", p.Intervention, "")
}

// Describe writes the two prose keys and nothing else. Wording is not evidence: the
// id, match, scope, baseline, instrument and both window stamps come back byte for
// byte, so a clearer sentence can never restart the measurement it describes.
func TestDescribeRewritesOnlyTheProse(t *testing.T) {
	stateDir := t.TempDir()
	original := Record{
		"id": "stale-test-count-claim", "class": "stale test-count claim",
		"match": "EXPECTED", "kind": "defect", "corpus": CorpusReviewFindings,
		"unit": UnitChain, "tier": "check", "target": "check.sh",
		"note": "check.sh tier 6", "problem": "old wording", "intervention": "old wording",
		"installed_ts": 1000, "installed_date": stamp(1000), "status": "active",
		"scope":      map[string]any{"version": 1, "all": true},
		"baseline":   map[string]any{"hits": 5, "units": 20, "rate": 0.25},
		"instrument": "codex/gpt-5/3/abc/v1",
	}
	if err := Save(stateDir, []Record{original}); err != nil {
		t.Fatal(err)
	}
	before := loadOne(t, stateDir)

	err := Describe(io.Discard, stateDir, "stale-test-count-claim", DescribeOpts{
		Problem:      "a check counts test cases and the literal it prints goes stale",
		Intervention: "check.sh refuses a printed literal count",
	})
	if err != nil {
		t.Fatal(err)
	}
	after := loadOne(t, stateDir)

	check(t, "problem is rewritten", str(after["problem"]),
		"a check counts test cases and the literal it prints goes stale")
	check(t, "intervention is rewritten", str(after["intervention"]),
		"check.sh refuses a printed literal count")
	check(t, "the legacy note is untouched", str(after["note"]), "check.sh tier 6")

	delete(before, "problem")
	delete(before, "intervention")
	delete(after, "problem")
	delete(after, "intervention")
	if got, want := pyDumps(after, 0), pyDumps(before, 0); got != want {
		t.Errorf("a describe moved a non-prose field:\n before: %s\n  after: %s", want, got)
	}
}

// An omitted flag keeps the value the record already carries, so one half of an
// explanation can be corrected without retyping the other.
func TestDescribeKeepsTheHalfItIsNotGiven(t *testing.T) {
	stateDir := t.TempDir()
	if err := Save(stateDir, []Record{{
		"id": "c", "class": "c", "problem": "kept", "intervention": "replaced",
	}}); err != nil {
		t.Fatal(err)
	}
	if err := Describe(io.Discard, stateDir, "c", DescribeOpts{Intervention: "new"}); err != nil {
		t.Fatal(err)
	}
	after := loadOne(t, stateDir)
	check(t, "the omitted half is kept", str(after["problem"]), "kept")
	check(t, "the given half is written", str(after["intervention"]), "new")
}

// A record written before the prose fields existed cannot be half-described: the
// validation runs over the merged result, so filling one half still refuses.
func TestDescribeRefusesToLeaveALegacyRecordHalfExplained(t *testing.T) {
	stateDir := t.TempDir()
	if err := Save(stateDir, []Record{{"id": "c", "class": "c", "note": "old"}}); err != nil {
		t.Fatal(err)
	}
	err := Describe(io.Discard, stateDir, "c", DescribeOpts{Problem: "the class recurs"})
	if err == nil || !strings.Contains(err.Error(), "--intervention is required") {
		t.Fatalf("want the intervention refusal, got %v", err)
	}
	if got := str(loadOne(t, stateDir)["problem"]); got != "" {
		t.Errorf("a refused describe wrote %q", got)
	}
}

// The verdict is unchanged by a rewording, on the chain corpus.
func TestDescribeLeavesAChainVerdictUnchanged(t *testing.T) {
	var rows []gate.Row
	ts := 1000
	for i := 0; i < 12; i++ {
		rows = append(rows, row(ts, "deny"),
			concerns(row(ts+1, "retry"), "round", c("widget parser drops a flag")),
			firing(row(ts+2, "clean"), 2))
		ts += 100
	}
	install := ts
	for i := 0; i < 12; i++ {
		rows = append(rows, row(ts, "deny"),
			concerns(row(ts+1, "retry"), "round", c("unrelated naming nit")),
			firing(row(ts+2, "clean"), 2))
		ts += 100
	}
	stateDir, ledger := writeLedger(t, rows)
	if err := Register(io.Discard, stateDir, RegisterOpts{
		Prose: testProse, ScopeAll: true, Ledger: ledger,
		Class: "widget parser flag loss", Match: "widget parser",
		Kind: "defect", Tier: "check", Target: "check.sh", InstalledTS: int64(install),
	}); err != nil {
		t.Fatal(err)
	}
	rowsAll, loadErr := LoadRows(stateDir, ledger)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	chains := BuildChains(rowsAll, false)
	before := scoreOne(t, stateDir, chains)
	if err := Describe(io.Discard, stateDir, "widget-parser-flag-loss", DescribeOpts{
		Problem:      "the reviewer finds a dropped flag in the widget parser",
		Intervention: "check.sh refuses a parser branch that drops a flag",
	}); err != nil {
		t.Fatal(err)
	}
	after := scoreOne(t, stateDir, chains)
	if !reflect.DeepEqual(before, after) {
		t.Errorf("a rewording moved the chain verdict:\n before: %v\n  after: %v", before, after)
	}
}

// The verdict is unchanged by a rewording, on the session corpus. The two engines
// read different fields, so one corpus proving preservation does not prove the other.
func TestDescribeLeavesASessionVerdictUnchanged(t *testing.T) {
	stateDir := t.TempDir()
	rec := sessionRecord(t, "bare python here", "check", "", 1500)
	rec["problem"] = "old"
	rec["intervention"] = "old"
	if err := Save(stateDir, []Record{rec}); err != nil {
		t.Fatal(err)
	}
	units := append(window(1000, 12, "claude-code", "proj", "bare python here"),
		window(2000, 12, "claude-code", "proj", "something else")...)
	ev := EvidenceSet{Sessions: units}

	before := scoreSession(t, loadOne(t, stateDir), ev)
	if err := Describe(io.Discard, stateDir, "friction", DescribeOpts{
		Problem:      "a session runs bare python and the interpreter is missing",
		Intervention: "a guard refuses a bare python invocation",
	}); err != nil {
		t.Fatal(err)
	}
	after := scoreSession(t, loadOne(t, stateDir), ev)
	if !reflect.DeepEqual(before, after) {
		t.Errorf("a rewording moved the session verdict:\n before: %v\n  after: %v", before, after)
	}
}

// loadOne reads the single record a prose test seeded.
func loadOne(t *testing.T, stateDir string) Record {
	t.Helper()
	records, err := Load(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("want 1 record, got %d", len(records))
	}
	return records[0]
}

// scoreOne scores the single seeded record through the chain engine.
func scoreOne(t *testing.T, stateDir string, chains []Chain) map[string]any {
	t.Helper()
	rec := loadOne(t, stateDir)
	res, err := ScoreRecord(rec, EvidenceSet{Chains: chains})
	if err != nil {
		t.Fatal(err)
	}
	delete(res, "problem")
	delete(res, "intervention")
	return res
}
