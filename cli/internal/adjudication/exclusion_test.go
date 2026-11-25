package adjudication

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/gate/mine"
)

// An exclusion retires evidence, so it must name its row, say why, and point at
// what shows the row records no real work.
func TestAppendExclusionRefusesAnUnsupportedCorrection(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name string
		e    Exclusion
		want string
	}{
		{"no row", Exclusion{Subject: SubjectGuard, Action: Exclude, Reason: "test", Evidence: []string{"x"}}, "name the row"},
		{"unknown action", Exclusion{Subject: SubjectGuard, RowDigest: "d", Action: "hide", Reason: "test"}, "invalid exclusion action"},
		{"no reason", Exclusion{Subject: SubjectGuard, RowDigest: "d", Action: Exclude, Reason: " ", Evidence: []string{"x"}}, "state its reason"},
		{"no evidence", Exclusion{Subject: SubjectGuard, RowDigest: "d", Action: Exclude, Reason: "test"}, "name the evidence"},
	}
	for _, c := range cases {
		err := AppendExclusion(dir, c.e)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want an error containing %q", c.name, err, c.want)
		}
	}
	if err := AppendExclusion(dir, Exclusion{Subject: SubjectGuard, RowDigest: "d", Action: Reinstate, Reason: "real work"}); err != nil {
		t.Errorf("a reinstate needs no evidence: %v", err)
	}
}

// The stream is append-only, so the latest entry for a row decides.
func TestExcludedFollowsTheLatestEntryPerRow(t *testing.T) {
	dir := t.TempDir()
	for _, e := range []Exclusion{
		{Subject: SubjectGuard, RowDigest: "a", Action: Exclude, Reason: "test run", Evidence: []string{"t"}},
		{Subject: SubjectGuard, RowDigest: "b", Action: Exclude, Reason: "test run", Evidence: []string{"t"}},
		{Subject: SubjectGuard, RowDigest: "a", Action: Reinstate, Reason: "it was real work"},
	} {
		if err := AppendExclusion(dir, e); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := LoadExclusions(dir)
	if err != nil {
		t.Fatal(err)
	}
	check(t, "every entry is kept", len(entries), 3)
	excluded := Excluded(entries)
	_, a := excluded["a"]
	_, b := excluded["b"]
	check(t, "a reinstated row is not excluded", a, false)
	check(t, "an excluded row is", b, true)
	check(t, "and nothing else is", len(excluded), 1)
}

// An excluded row is counted once, apart from both the legacy and the
// adjudicable rows, and a receipt of it moves no judged count.
func TestCoverCountsExcludedRowsApart(t *testing.T) {
	installed := Identity{HookDigest: "d1", RuleHash: "r1"}
	firings := []mine.Firing{
		{HookID: "g", FiringID: "aa", HookDigest: "d1", RuleHash: "r1", RowDigest: "ra"},
		{HookID: "g", FiringID: "bb", HookDigest: "d1", RuleHash: "r1", RowDigest: "rb"},
		{HookID: "g", RowDigest: "rc"},
		{HookID: "g", RowDigest: "rd"},
	}
	receipts := []Receipt{{Subject: SubjectGuard, FiringID: "bb", Judgment: Unresolved, HookDigest: "d1", RuleHash: "r1"}}
	excluded := map[string]Exclusion{"rb": {}, "rd": {}}
	c := Cover("g", firings, receipts, installed, excluded)

	check(t, "every row is counted", c.Rows, 4)
	check(t, "two are excluded", c.Excluded, 2)
	check(t, "one legacy row remains", c.Legacy, 1)
	check(t, "one adjudicable row remains", c.Adjudicable, 1)
	check(t, "the excluded row's receipt judges nothing here", c.Judged, 0)
	check(t, "the remaining row is unread", c.CurrentUnjudged, 1)
	check(t, "rows account exactly once", c.Adjudicable+c.Legacy+c.Excluded, c.Rows)
}

// An exclusion states its subject: a legacy line retires a guard row, a check
// exclusion retires a run, and neither reader sees the other's.
func TestExclusionsKeepSubjectsApart(t *testing.T) {
	dir := t.TempDir()
	legacy := `{"row_digest":"aa","action":"exclude","reason":"test","evidence":["t"]}` + "\n" +
		`{"subject":"review","row_digest":"bb","action":"exclude","reason":"x","evidence":["t"]}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, ExclusionLogName), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, e := range []Exclusion{
		{Subject: SubjectCheck, RunID: "aa", Action: Exclude, Reason: "demo session", Evidence: []string{"t"}},
		{Subject: SubjectCheck, RunID: "1b", Action: Exclude, Reason: "demo session", Evidence: []string{"t"}},
		{Subject: SubjectCheck, RunID: "1b", Action: Reinstate, Reason: "it was real work"},
	} {
		if err := AppendExclusion(dir, e); err != nil {
			t.Fatal(err)
		}
	}
	for name, e := range map[string]Exclusion{
		"no subject":       {RowDigest: "cc", Action: Exclude, Reason: "x", Evidence: []string{"t"}},
		"a run on a guard": {Subject: SubjectGuard, RowDigest: "cc", RunID: "1a", Action: Exclude, Reason: "x", Evidence: []string{"t"}},
		"a row on a check": {Subject: SubjectCheck, RunID: "1a", RowDigest: "cc", Action: Exclude, Reason: "x", Evidence: []string{"t"}},
		"no run":           {Subject: SubjectCheck, Action: Exclude, Reason: "x", Evidence: []string{"t"}},
	} {
		if err := AppendExclusion(dir, e); err == nil {
			t.Errorf("%s: the exclusion was accepted", name)
		}
	}
	entries, err := LoadExclusions(dir)
	if err != nil {
		t.Fatal(err)
	}
	guards, runs := Excluded(entries), ExcludedRuns(entries)
	_, guardAA := guards["aa"]
	_, runAA := runs["aa"]
	_, run1b := runs["1b"]
	if len(entries) != 4 || len(guards) != 1 || !guardAA || len(runs) != 1 || !runAA || run1b {
		t.Errorf("guards %v, runs %v from %d entries; want row aa, run aa, and the reinstated run back", guards, runs, len(entries))
	}
}
