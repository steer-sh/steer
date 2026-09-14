package cadence

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/steer-sh/steer-cli/internal/gate/mine"
)

// isolate points HOME at a temp dir so the transcript scan, the denials read, and
// the output dir all resolve inside the sandbox, never the real machine.
func isolate(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

func TestRefreshEmptyIsDeterministic(t *testing.T) {
	isolate(t)
	gateDir := t.TempDir()
	opts := Options{GateStateDir: gateDir, WindowDays: 7}

	q1, err := Refresh(opts)
	if err != nil {
		t.Fatal(err)
	}
	q2, err := Refresh(opts)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(q1, q2) {
		t.Error("two refreshes over empty corpora differ")
	}
	if len(q1.Interventions) != 0 || len(q1.Candidates) != 0 || len(q1.Unscored) != 0 {
		t.Errorf("empty machine should yield an empty queue, got %+v", q1)
	}
	// Every corpus emits one row even when absent — the sweep, not an error.
	if len(q1.Corpora) != 3 {
		t.Fatalf("want 3 corpus rows, got %d", len(q1.Corpora))
	}
	for _, c := range q1.Corpora {
		if c.Present || c.Size != 0 {
			t.Errorf("corpus %s should read absent on an empty machine, got %+v", c.Corpus, c)
		}
	}
}

func TestWriteQueueIsByteIdentical(t *testing.T) {
	dir := t.TempDir()
	q := &Queue{
		Version:       QueueVersion,
		Interventions: []Intervention{},
		Candidates: []Candidate{
			{Corpus: corpusReviewFindings, UndecidedChains: 5, Dump: "steer commit-gate mine findings"},
		},
		Corpora: []CorpusStatus{{Corpus: corpusReviewFindings, Present: true, Size: 10}},
	}
	if err := WriteQueue(dir, q, time.Now()); err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(QueuePath(dir))
	if err := WriteQueue(dir, q, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(QueuePath(dir))
	if !bytes.Equal(first, second) {
		t.Error("queue bytes changed although the queue content did not")
	}
}

func TestQueueRoundTrip(t *testing.T) {
	dir := t.TempDir()
	q := &Queue{
		Version: QueueVersion,
		Interventions: []Intervention{{
			ID: "widget-flag", Class: "widget flag loss", Match: "widget parser",
			Corpus: corpusReviewFindings, Unit: mine.UnitChain, Tier: "check",
			Target:      "check.sh",
			Observation: mine.Observation{Coverage: mine.ObservationCoverage{After: map[string]any{"eligible": float64(2)}}},
			Before:      Incidence{Hits: 4, Units: 20, Rate: 0.2},
			After:       Incidence{Hits: 5, Units: 20, Rate: 0.25},
		}},
		Unscored:   []string{"some-session-hook"},
		Candidates: []Candidate{{Corpus: corpusTranscript, Pattern: "no bare python", Count: 6, Sessions: 4}},
		Corpora:    []CorpusStatus{{Corpus: corpusTranscript, Present: true, Size: 12}},
	}
	if err := WriteQueue(dir, q, time.Now()); err != nil {
		t.Fatal(err)
	}
	got, err := ReadQueue(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Every field is compared, not a spot check: a corruption of Before, Scope,
	// Candidates or Corpora is exactly what a round-trip test is for. The coverage
	// map is written with float64 values because JSON has one number type, so the
	// want side already holds what a read produces.
	if !reflect.DeepEqual(q, got) {
		t.Errorf("round trip changed the queue:\n want %+v\n got  %+v", q, got)
	}
}

func TestReadQueueMissingIsEmptyNotError(t *testing.T) {
	q, err := ReadQueue(t.TempDir())
	if err != nil {
		t.Fatalf("a missing queue must not error: %v", err)
	}
	if len(q.Interventions) != 0 || len(q.Candidates) != 0 {
		t.Error("a missing queue must read empty")
	}
}

func TestLockExcludesSecondWriter(t *testing.T) {
	dir := t.TempDir()
	release, err := acquireLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := acquireLock(dir); err == nil {
		t.Error("a second writer must not acquire a held lock")
	}
	release()
	release2, err := acquireLock(dir)
	if err != nil {
		t.Error("the lock must be free after release")
	} else {
		release2()
	}
}

func TestLockStealsStale(t *testing.T) {
	dir := t.TempDir()
	stale := filepath.Join(dir, ".refresh.lock")
	if err := os.WriteFile(stale, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * lockStaleAfter)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}
	release, err := acquireLock(dir)
	if err != nil {
		t.Fatalf("a stale lock must be stolen, got %v", err)
	}
	release()
}

func TestUndecidedChainsCountsUnmatchedAccepted(t *testing.T) {
	chains := []mine.Chain{
		{Findings: []mine.Finding{{Verdict: "ACCEPTED", Text: "widget parser drops a flag"}}},
		{Findings: []mine.Finding{{Verdict: "ACCEPTED", Text: "unrelated naming nit"}}},
		{Findings: []mine.Finding{{Verdict: "REJECTED", Text: "false alarm"}}},
	}
	records := []mine.Record{
		{"match": "widget parser", "kind": "defect", "status": "active"},
	}
	// One accepted chain is decided by the active class; the other accepted chain is
	// undecided; the rejected-only chain has no accepted finding and does not count.
	if got := undecidedChains(chains, records); got != 1 {
		t.Errorf("want 1 undecided chain, got %d", got)
	}
}

func TestUndecidedChainsIgnoresRetiredAndFalsePositiveClasses(t *testing.T) {
	// One chain carries an unclassified accepted defect and a rejected finding.
	chains := []mine.Chain{{Findings: []mine.Finding{
		{Verdict: "ACCEPTED", Text: "widget parser drops a flag"},
		{Verdict: "REJECTED", Text: "style nit the reviewer over-reported"},
	}}}
	records := []mine.Record{
		// A removed defect class must not decide the accepted finding.
		{"match": "widget parser", "kind": "defect", "status": "removed"},
		// A live false-positive class matches the rejected finding, not the accepted
		// defect, so it must not mark the chain decided either.
		{"match": "style nit", "kind": "false-positive", "status": "active"},
	}
	if got := undecidedChains(chains, records); got != 1 {
		t.Errorf("the accepted defect is still undecided; want 1, got %d", got)
	}
}

func TestUndecidedChainsResidualFindingInMixedChain(t *testing.T) {
	// One chain carries a classified accepted finding AND an unclassified one. The
	// classified finding must not decide the whole chain — the residual counts.
	chains := []mine.Chain{{Findings: []mine.Finding{
		{Verdict: "ACCEPTED", Text: "widget parser drops a flag"},
		{Verdict: "ACCEPTED", Text: "a brand new defect no class names"},
	}}}
	records := []mine.Record{
		{"match": "widget parser", "kind": "defect", "status": "active"},
	}
	if got := undecidedChains(chains, records); got != 1 {
		t.Errorf("a residual unclassified finding must keep the chain undecided; want 1, got %d", got)
	}
}

func TestNeedsAttention(t *testing.T) {
	if !needsAttention(mine.Observation{Recurrence: mine.Recurrence{Occurred: true}}, struct{}{}, nil) {
		t.Error("recurrence should need attention")
	}
	if !needsAttention(mine.Observation{Firing: mine.FiringActivity{Applicable: true}}, struct{}{}, nil) {
		t.Error("a missing applicable audit log should need attention")
	}
	// `pending` is counted inside `unknown_evidence`, and a pending row settles on
	// its own, so only the excess over it is a gap the reader can act on.
	if needsAttention(mine.Observation{Coverage: mine.ObservationCoverage{
		After: map[string]any{"unknown": 1, "unknown_evidence": 1, "pending": 1}}}, struct{}{}, nil) {
		t.Error("a window whose unknowns are all still settling should not need attention")
	}
	if !needsAttention(mine.Observation{Coverage: mine.ObservationCoverage{
		After: map[string]any{"unknown": 2, "unknown_evidence": 2, "pending": 1}}}, struct{}{}, nil) {
		t.Error("evidence beyond what is still settling should need attention")
	}
	if needsAttention(mine.Observation{Coverage: mine.ObservationCoverage{After: map[string]any{"eligible": 2}}}, struct{}{}, nil) {
		t.Error("quiet eligible work should not need attention")
	}
	if !needsAttention(mine.Observation{}, nil, nil) {
		t.Error("a missing scope should need attention")
	}
}

func TestTranscriptCandidatesRankByStrength(t *testing.T) {
	// Alphabetical order and strength order disagree here, so a pass that still
	// sorts by pattern truncates the strongest row away.
	cs := []Candidate{
		{Corpus: corpusTranscript, Pattern: "aaa seen once", Count: 1, Sessions: 1},
		{Corpus: corpusTranscript, Pattern: "zzz seen everywhere", Count: 9, Sessions: 4},
		{Corpus: corpusTranscript, Pattern: "mmm seen twice", Count: 5, Sessions: 2},
	}
	sortByStrength(cs)

	want := []string{"zzz seen everywhere", "mmm seen twice", "aaa seen once"}
	for i, w := range want {
		if cs[i].Pattern != w {
			t.Fatalf("position %d: got %q, want %q", i, cs[i].Pattern, w)
		}
	}
}

func TestCandidateOrderIsDeterministicOnTies(t *testing.T) {
	tie := func() []Candidate {
		return []Candidate{
			{Corpus: corpusTranscript, Pattern: "b same weight", Count: 2, Sessions: 2},
			{Corpus: corpusTranscript, Pattern: "a same weight", Count: 2, Sessions: 2},
		}
	}
	first, second := tie(), tie()
	sortCandidates(first)
	sortCandidates(second)
	if first[0].Pattern != "a same weight" || first[0].Pattern != second[0].Pattern {
		t.Fatalf("equal-weight rows must order by pattern: %q then %q", first[0].Pattern, first[1].Pattern)
	}
}

func TestSortCandidatesGroupsByCorpusBeforeStrength(t *testing.T) {
	cs := []Candidate{
		{Corpus: corpusTranscript, Pattern: "strong", Count: 9, Sessions: 9},
		{Corpus: corpusReviewFindings, UndecidedChains: 90},
	}
	sortCandidates(cs)
	if cs[0].Corpus != corpusReviewFindings {
		t.Errorf("corpus grouping must outrank strength: got %q first", cs[0].Corpus)
	}
}
