package firingaudit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/adjudication"
	"github.com/steer-sh/steer-cli/internal/checks"
	"github.com/steer-sh/steer-cli/internal/gate/mine"
)

// Both readers of the stream name a row by the same digest of its own bytes, so
// one exclusion reaches the queue and the recurrence counts alike.
func TestLoadersStampTheSameRowDigest(t *testing.T) {
	dir := t.TempDir()
	lines := []string{
		`{"timestamp":"2026-09-01T00:00:00Z","hook_name":"g","session_id":"s1"}`,
		`{"timestamp":"2026-09-02T00:00:00Z","hook_name":"g","firing_id":"f2"}`,
	}
	if err := os.WriteFile(filepath.Join(dir, AuditLogName),
		[]byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rows, _ := Load(dir)
	firings, _ := mine.LoadFirings(dir)
	if len(rows) != 2 || len(firings) != 2 {
		t.Fatalf("read %d rows and %d firings, want 2 each", len(rows), len(firings))
	}
	for i, line := range lines {
		want := mine.RowDigest(line)
		if rows[i].RowDigest != want || firings[i].RowDigest != want {
			t.Errorf("row %d: firingaudit %q, mine %q, want %q", i, rows[i].RowDigest, firings[i].RowDigest, want)
		}
	}
	if rows[0].RowDigest == rows[1].RowDigest {
		t.Error("two different rows share a digest")
	}
	if Firings(rows)[1].RowDigest != rows[1].RowDigest {
		t.Error("Firing() drops the row digest")
	}
}

func TestFindRefResolvesIdentifierDigestAndUniquePrefix(t *testing.T) {
	rows := []Row{
		{FiringID: "f1", RowDigest: "abcdef0011"},
		{RowDigest: "abcdef0022"},
	}
	for ref, want := range map[string]string{"f1": "abcdef0011", "abcdef0022": "abcdef0022", "abcdef002": "abcdef0022"} {
		row, err := FindRef(rows, ref)
		if err != nil || row.RowDigest != want {
			t.Errorf("%s: got %q, %v; want %q", ref, row.RowDigest, err, want)
		}
	}
	for ref, want := range map[string]string{"abcdef00": "matches 2 rows", "abc": "needs 8 characters", "ffffffff": "no audit row", "": "name a firing"} {
		if _, err := FindRef(rows, ref); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: got %v, want an error containing %q", ref, err, want)
		}
	}
}

// A correction copies the row's own fields, and one that would change nothing
// is refused rather than written.
func TestCorrectCopiesTheRowAndRefusesANoOp(t *testing.T) {
	dir := t.TempDir()
	rows := []Row{{FiringID: "f1", HookName: "g", Harness: "codex", SessionID: "s",
		Timestamp: "2026-09-20T16:41:50Z", StoreDir: "/store", RowDigest: "d1"}}
	e, err := Correct(rows, dir, Correction{Ref: "f1", Action: adjudication.Exclude,
		Reason: "codex exec test run", Evidence: []string{"rollout.jsonl"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if e.RowDigest != "d1" || e.HookID != "g" || e.SessionID != "s" || e.StoreDir != "/store" || e.RecordedAt == "" {
		t.Errorf("the exclusion did not copy the row: %+v", e)
	}
	entries, _ := adjudication.LoadExclusions(dir)
	excluded := adjudication.Excluded(entries)
	if _, err := Correct(rows, dir, Correction{Ref: "f1", Action: adjudication.Exclude,
		Reason: "again", Evidence: []string{"x"}}, excluded); err == nil {
		t.Error("excluding an excluded row was written")
	}
	if _, err := Correct(rows, dir, Correction{Ref: "f1", Action: adjudication.Reinstate,
		Reason: "real"}, nil); err == nil {
		t.Error("reinstating a row that is not excluded was written")
	}
	entries, _ = adjudication.LoadExclusions(dir)
	if len(entries) != 1 {
		t.Errorf("the stream holds %d entries, want 1", len(entries))
	}
}

// An excluded row is neither queued nor counted as work the listing left.
func TestQueueLeavesExcludedRowsOutAndCountsThem(t *testing.T) {
	a := ccRow(t, "aa", "2026-09-01T00:00:00Z")
	a.RowDigest = "ra"
	b := ccRow(t, "bb", "2026-09-02T00:00:00Z")
	b.RowDigest = "rb"
	items, counts := Queue([]Row{a, b}, nil, nil, QueueOptions{Installed: installed,
		Excluded: map[string]adjudication.Exclusion{"ra": {}}})
	if len(items) != 1 || items[0].FiringID != "bb" {
		t.Fatalf("queued %+v, want bb alone", items)
	}
	if counts.Excluded != 1 || counts.Queued != 1 || counts.Unjudged != 1 {
		t.Errorf("counts %+v, want one excluded and one queued", counts)
	}
}

// A check block is excluded by its run and restored the same way; the queue,
// the coverage and the contest counts leave it out while it is excluded.
func TestAnExcludedCheckBlockLeavesTheReadings(t *testing.T) {
	demo := blockRun("/r", "1a", 1)
	real := blockRun("/r", "2b", 2)
	cases := CheckCases(checks.LiveEvidence{Runs: []checks.OracleRun{demo, real}})
	dir := t.TempDir()
	correction := Correction{Ref: "1a", Action: adjudication.Exclude, Reason: "demo session", Evidence: []string{"t.jsonl"}}
	e, err := CorrectCheck(cases, dir, correction, nil)
	if err != nil || e.Subject != adjudication.SubjectCheck || e.RunID != "1a" || e.Repo != "/r" {
		t.Fatalf("exclusion = %+v, %v; want run 1a of /r", e, err)
	}
	entries, _ := adjudication.LoadExclusions(dir)
	retired := adjudication.ExcludedRuns(entries)
	if _, err := CorrectCheck(cases, dir, correction, retired); err == nil {
		t.Error("an excluded run was excluded again")
	}
	if _, err := CorrectCheck(cases, dir, Correction{Ref: "2b", Action: adjudication.Reinstate, Reason: "x"}, retired); err == nil {
		t.Error("a run never excluded was reinstated")
	}
	if _, err := CorrectCheck(cases, dir, Correction{Ref: "9z", Action: adjudication.Exclude, Reason: "x", Evidence: []string{"t"}}, retired); err == nil {
		t.Error("a run no block has was excluded")
	}

	items, counts := Queue(nil, cases, nil, QueueOptions{ExcludedRuns: retired})
	if len(items) != 1 || items[0].RunID != "2b" || counts.Excluded != 1 {
		t.Errorf("queue = %v, counts %+v; want only 2b and one excluded", ids(items), counts)
	}
	if got := CoverChecks(cases, nil, retired); got[0].Blocks != 2 || got[0].Excluded != 1 || got[0].Unjudged != 1 {
		t.Errorf("coverage = %+v; want 2 blocks, 1 excluded, 1 unread", got)
	}
	contests := []adjudication.Contest{{Subject: adjudication.SubjectCheck, RunID: "1a", Reason: "demo"}}
	report := Contests(nil, cases, contests, nil, nil, retired, nil, "")
	if r := report.Checks[0]; r.Blocks != 1 || r.Contested != 0 || r.ContestedExcluded != 1 || len(report.UnmatchedChecks) != 0 {
		t.Errorf("contests = %+v; want the excluded block counted only as excluded", report)
	}
}
