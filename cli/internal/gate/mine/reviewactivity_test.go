package mine

import (
	"bytes"
	"encoding/json"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/gate"
)

func decodedRows(t *testing.T, raw ...string) []gate.Row {
	t.Helper()
	var rows []gate.Row
	for _, r := range raw {
		var row gate.Row
		if err := json.Unmarshal([]byte(r), &row); err != nil {
			t.Fatal(err)
		}
		rows = append(rows, row)
	}
	return rows
}

// A reviewed rule's activity counts the rounds that gave it to the reviewer, the rounds
// and chains with a finding naming it, and those findings by severity; another rule's
// rounds and findings, and a record on another tier, count nothing.
func TestReviewActivityCountsTheRulesRoundsAndFindings(t *testing.T) {
	after := []Chain{
		{RepoRoot: "/src/app", Rows: decodedRows(t,
			`{"ts":100,"phase":"deny","reviewers":2,"reviewed_rules_applied":["project:tui"],"rule_findings":{"project:tui":{"SUBSTANTIVE":2},"global:other":{"BLOCKING":1}}}`,
			`{"ts":200,"phase":"clean","reviewers":2,"reviewed_rules_applied":["project:tui"]}`)},
		{RepoRoot: "/src/app", Rows: decodedRows(t,
			`{"ts":300,"phase":"nitpick","reviewers":2,"reviewed_rules_applied":["project:tui","global:other"],"rule_findings":{"project:tui":{"NITPICK":1}}}`)},
		{RepoRoot: "/src/app", Rows: decodedRows(t, `{"ts":400,"phase":"clean","reviewers":2,"reviewed_rules_applied":["global:other"]}`)},
	}
	record := Record{"tier": "reviewed", "rule_id": "tui", "rule_scope": "project", "rule_repo": "/src/app"}

	got := reviewActivityOf(record, after, nil, "")
	want := ReviewActivity{
		Applicable: true, RuleKey: "project:tui", Chains: 3,
		ReviewedRounds: 3, FindingRounds: 2, FindingChains: 2,
		Findings: map[string]int{"SUBSTANTIVE": 2, "NITPICK": 1}, NewestTS: 300,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("activity = %+v\nwant       %+v", got, want)
	}
	var out bytes.Buffer
	writeReviewActivity(&out, got)
	if line := out.String(); !strings.Contains(line, "3 answered rounds over 3 chains; 2 rounds in 2 chains named it (2 SUBSTANTIVE, 1 NITPICK)") {
		t.Fatalf("text line = %q", line)
	}

	record["tier"] = "instruction"
	if got := reviewActivityOf(record, after, nil, ""); got.Applicable || got.ReviewedRounds != 0 {
		t.Fatalf("an instruction record read %+v", got)
	}
}

// A project rule counts only its own repository's chains, and counts none when its
// record names no repository; a round no reviewer answered is not a reviewed round.
func TestReviewActivityKeepsToTheRulesRepositoryAndAnsweredRounds(t *testing.T) {
	applied := `,"reviewed_rules_applied":["project:tui"],"rule_findings":{"project:tui":{"BLOCKING":1}}}`
	after := []Chain{
		{RepoRoot: "/src/app", Rows: decodedRows(t, `{"ts":1,"phase":"deny","reviewers":2`+applied,
			`{"ts":2,"phase":"ungated","reviewers":0`+applied, `{"ts":3,"phase":"cap","reviewers":0`+applied)},
		{RepoRoot: "/src/other", Rows: decodedRows(t, `{"ts":4,"phase":"deny","reviewers":2`+applied)},
	}
	record := Record{"tier": "reviewed", "rule_id": "tui", "rule_scope": "project", "rule_repo": "/src/app"}
	got := reviewActivityOf(record, after, nil, "")
	if got.Chains != 1 || got.ReviewedRounds != 1 || got.Findings["BLOCKING"] != 1 {
		t.Fatalf("activity = %+v", got)
	}
	delete(record, "rule_repo")
	if got := reviewActivityOf(record, after, nil, ""); !got.Unplaced || got.Chains != 0 || got.ReviewedRounds != 0 {
		t.Fatalf("an unplaced project rule read %+v", got)
	}
}

// Each chain the class regex matches lands in exactly one group: a chain in another
// repository first, even with a deferred match, then a deferred match, a chain with a
// finding naming the rule, a chain whose reviewer had the rule and did not name it,
// and a chain no reviewed round gave the rule; an unreached chain's paths are listed.
func TestReviewActivityPlacesEachMatchedChainInOneGroup(t *testing.T) {
	const applied = `{"ts":1,"phase":"deny","reviewers":2,"reviewed_rules_applied":["project:tui"]`
	named := applied + `,"rule_findings":{"project:tui":{"SUBSTANTIVE":1}}}`
	repaired := []Finding{{Verdict: "ACCEPTED", Text: "tui drift"}}
	chain := func(root string, findings []Finding, paths []string, rows ...string) Chain {
		return Chain{RepoRoot: root, Findings: findings, Paths: paths, Rows: decodedRows(t, rows...)}
	}
	after := []Chain{
		chain("/src/app", []Finding{{Verdict: "ACCEPTED", Qualifier: "not fixed", Text: "tui drift"}}, nil, named),
		chain("/src/app", repaired, nil, named),
		chain("/src/app", repaired, nil, applied+`}`),
		chain("/src/app", repaired, []string{"docs/b.md", "cmd/a.go"},
			`{"ts":1,"phase":"deny","reviewers":2,"reviewed_rules_applied":["global:other"]}`),
		chain("/src/app", []Finding{{Verdict: "REJECTED", Text: "tui drift"}}, []string{"z.go"}),
		chain("/src/other", repaired, []string{"x.go"}),
		chain("/src/other", []Finding{{Verdict: "ACCEPTED", Qualifier: "not fixed", Text: "tui drift"}}, []string{"y.go"}),
	}
	record := Record{"tier": "reviewed", "rule_id": "tui", "rule_scope": "project", "rule_repo": "/src/app"}

	got := reviewActivityOf(record, after, regexp.MustCompile("tui"), "accepted").Matched
	want := &MatchedChains{Elsewhere: 2, Deferred: 1, Named: 1, Unnamed: 1, Unreached: 1,
		UnreachedPaths: []string{"cmd/a.go", "docs/b.md"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("matched = %+v\nwant      %+v", got, want)
	}
	if got := reviewActivityOf(record, after, nil, "").Matched; got != nil {
		t.Fatalf("a record without a chain pattern placed %+v", got)
	}
}

// Both scorers fill a reviewed record's Review from the chains their own window admits:
// a chain before the split, a chain outside the record's scope and a chain whose ledger
// is still pending count nothing. Only the chain scorer places matched chains.
func TestBothScorersReadReviewActivityFromTheAdmittedChains(t *testing.T) {
	const clock = 9000
	applied := `,"reviewers":2,"reviewed_rules_applied":["global:tui"],"rule_findings":{"global:tui":{"SUBSTANTIVE":1}}}`
	chain := func(ts float64, root string, rows ...string) Chain {
		return Chain{StartTS: ts, EndTS: ts + 10, RepoRoot: root, RepoRootKnown: true, Rows: decodedRows(t, rows...)}
	}
	chains := []Chain{
		chain(1000, "/src/app", `{"ts":1000,"phase":"clean"`+applied),
		chain(3000, "/src/app", `{"ts":3000,"phase":"deny"`+applied, `{"ts":3010,"phase":"retry","concerns":{}}`),
		chain(3100, "/src/other", `{"ts":3100,"phase":"clean"`+applied),
		chain(clock-10, "/src/app", `{"ts":8990,"phase":"nitpick"`+applied),
	}
	sm, err := BuildScope(false, nil, "", "/src/app", "")
	if err != nil {
		t.Fatal(err)
	}
	record := Record{"id": "tui", "class": "tui", "match": "tui", "tier": "reviewed",
		"installed_ts": 2000, "scope": sm, "rule_id": "tui", "rule_scope": "global"}
	session := Record{"corpus": CorpusTranscript, "unit": UnitSession}
	for k, v := range record {
		session[k] = v
	}
	withClock(clock, func() {
		for name, score := range map[string]func() (map[string]any, error){
			"chain":   func() (map[string]any, error) { return ScoreRecord(record, EvidenceSet{Chains: chains}) },
			"session": func() (map[string]any, error) { return ScoreSessionRecord(session, EvidenceSet{Chains: chains}) },
		} {
			scored, err := score()
			if err != nil {
				t.Fatal(err)
			}
			got := observationOf(t, scored).Review
			if got.Chains != 1 || got.ReviewedRounds != 1 || got.FindingRounds != 1 || got.Findings["SUBSTANTIVE"] != 1 {
				t.Errorf("%s scorer: review = %+v, want the one admitted chain", name, got)
			}
			if (got.Matched != nil) != (name == "chain") {
				t.Errorf("%s scorer: matched = %+v; only the chain scorer places matched chains", name, got.Matched)
			}
		}
	})
}
