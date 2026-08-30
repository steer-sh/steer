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
			Target: "check.sh", Verdict: "ineffective",
			Before: Incidence{Hits: 4, Units: 20, Rate: 0.2},
			After:  Incidence{Hits: 5, Units: 20, Rate: 0.25},
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
	if !reflect.DeepEqual(q, got) {
		t.Errorf("round trip changed the queue:\n want %+v\n  got %+v", q, got)
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
	for _, v := range []string{"harmful", "ineffective", "class-grew",
		"legacy-unscoped", "evidence-incomplete", "confounded"} {
		if !needsAttention(v) {
			t.Errorf("%s should need attention", v)
		}
	}
	for _, v := range []string{"effective", "collecting", "inconclusive", "no-baseline",
		"class-faded", "class-steady", "class-inconclusive"} {
		if needsAttention(v) {
			t.Errorf("%s should not need attention", v)
		}
	}
}
