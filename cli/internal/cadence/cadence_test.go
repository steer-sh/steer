package cadence

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/steer-sh/steer-cli/internal/gate/mine"
	"github.com/steer-sh/steer-cli/internal/report"
	"github.com/steer-sh/steer-cli/internal/testhome"
)

// isolate points HOME at a temp dir so the transcript scan and the output dir
// resolve inside the sandbox, never the real machine.
func isolate(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	testhome.Set(t, home)
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
	if len(q1.Corpora) != 2 {
		t.Fatalf("want 2 corpus rows, got %d", len(q1.Corpora))
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
			{Corpus: CorpusReviewFindings, UndecidedChains: 5, Dump: "steer commit-gate mine findings"},
		},
		Corpora: []CorpusStatus{{Corpus: CorpusReviewFindings, Present: true, Size: 10}},
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
			Corpus: CorpusReviewFindings, Unit: mine.UnitChain, Tier: "check",
			Target:      "check.sh",
			Observation: mine.Observation{Coverage: mine.ObservationCoverage{After: map[string]any{"eligible": float64(2)}}},
			Before:      Incidence{Hits: 4, Units: 20, Rate: 0.2},
			After:       Incidence{Hits: 5, Units: 20, Rate: 0.25},
		}},
		Unscored:   []string{"some-session-hook"},
		Candidates: []Candidate{{Corpus: CorpusTranscript, UngroupedClusters: 6, Examples: []string{"no bare python"}}},
		Corpora:    []CorpusStatus{{Corpus: CorpusTranscript, Present: true, Size: 12}},
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

func TestUndecidedChainsIgnoresAnotherCorpusDecision(t *testing.T) {
	// A transcript-friction class names a pattern in a session transcript. Its regex
	// meeting a review finding's text decides nothing: the class was never authored
	// over this corpus, so the finding stays undecided.
	chains := []mine.Chain{{Findings: []mine.Finding{
		{Verdict: "ACCEPTED", Text: "FileNotFoundError: [Errno 2] no such file"},
	}}}
	records := []mine.Record{
		{"match": "FileNotFoundError", "kind": "defect", "status": "active",
			"corpus": mine.CorpusTranscript, "unit": mine.UnitSession},
	}
	if got := undecidedChains(chains, records); got != 1 {
		t.Errorf("a transcript class must not decide a review finding; want 1, got %d", got)
	}
}

// pins existing behaviour
func TestUndecidedChainsReadsAnUntaggedRecordAsReviewFindings(t *testing.T) {
	// A record stored before the corpus tags existed carries neither tag. CorpusOf
	// and UnitOf supply the review-findings defaults, so it keeps deciding.
	chains := []mine.Chain{{Findings: []mine.Finding{
		{Verdict: "ACCEPTED", Text: "widget parser drops a flag"},
	}}}
	records := []mine.Record{
		{"match": "widget parser", "kind": "defect", "status": "active"},
	}
	if got := undecidedChains(chains, records); got != 0 {
		t.Errorf("an untagged record still decides; want 0, got %d", got)
	}
}

func TestNeedsAttention(t *testing.T) {
	if !needsAttention(mine.Observation{Recurrence: mine.Recurrence{Occurred: true}}, struct{}{}, "instruction", nil) {
		t.Error("recurrence should need attention")
	}
	if !needsAttention(mine.Observation{Firing: mine.FiringActivity{Applicable: true}}, struct{}{}, "instruction", nil) {
		t.Error("a missing applicable audit log should need attention")
	}
	// `pending` is counted inside `unknown_evidence`, and a pending row settles on
	// its own, so only the excess over it is a gap the reader can act on.
	if needsAttention(mine.Observation{Coverage: mine.ObservationCoverage{
		After: map[string]any{"unknown": 1, "unknown_evidence": 1, "pending": 1}}}, struct{}{}, "instruction", nil) {
		t.Error("a window whose unknowns are all still settling should not need attention")
	}
	// A closed miss has no repair and an overdue row is the resolver's own status
	// entry, so neither queues the record.
	if needsAttention(mine.Observation{Coverage: mine.ObservationCoverage{
		After: map[string]any{"unknown": 3, "unknown_evidence": 3, "pending": 1, "ledger_missed": 1, "ledger_overdue": 1}}}, struct{}{}, "instruction", nil) {
		t.Error("a ledger cause should not need attention")
	}
	if needsAttention(mine.Observation{Coverage: mine.ObservationCoverage{
		After: map[string]any{"unknown": 3, "unknown_scope": 2}}}, struct{}{}, "instruction", nil) {
		t.Error("an unknown scope or a projectless session should not need attention")
	}
	if needsAttention(mine.Observation{Coverage: mine.ObservationCoverage{After: map[string]any{"eligible": 2}}}, struct{}{}, "instruction", nil) {
		t.Error("quiet eligible work should not need attention")
	}
	if !needsAttention(mine.Observation{}, nil, "instruction", nil) {
		t.Error("a missing scope should need attention")
	}
	// A rule reworded under its own id leaves the record counting against text
	// nobody carries, and a check's detector digest moves with the test it runs.
	quiet := mine.Observation{Coverage: mine.ObservationCoverage{After: map[string]any{"eligible": 2}}}
	drifted := &Artifact{ID: "my-fix", State: ArtifactChanged}
	if !needsAttention(quiet, struct{}{}, "instruction", drifted) {
		t.Error("a reworded rule should need attention")
	}
	if needsAttention(quiet, struct{}{}, "check", drifted) {
		t.Error("an edited detector should not need attention")
	}
	if needsAttention(quiet, struct{}{}, "instruction", &Artifact{ID: "my-fix", State: ArtifactPresent}) {
		t.Error("a fix in place as recorded should not need attention")
	}
}

func TestTranscriptClustersFoldIntoOnePointerRow(t *testing.T) {
	// The count is the whole point of the row: a cluster key is one phrasing, so
	// 30 clusters are 30 phrasings and never 30 problems. A pass that emits one
	// row per cluster reports the size of the raw pile as the number of problems.
	clusters := []*report.Cluster{}
	for i := 0; i < 30; i++ {
		clusters = append(clusters, &report.Cluster{
			Pattern: fmt.Sprintf("phrasing %02d", i), Count: 1, Sessions: 1,
		})
	}
	c := transcriptCandidate(clusters)

	if c.Corpus != CorpusTranscript {
		t.Errorf("corpus: got %q", c.Corpus)
	}
	if c.UngroupedClusters != 30 {
		t.Errorf("the row must count every cluster: got %d, want 30", c.UngroupedClusters)
	}
	if len(c.Examples) != maxCandidateExamples {
		t.Errorf("examples are capped at %d: got %d", maxCandidateExamples, len(c.Examples))
	}
}

func TestPointerRowExamplesKeepTheDigestOrder(t *testing.T) {
	// The row does no ranking of its own. It takes the head of the order the digest
	// hands it, so the strongest clusters are the ones a reader sees.
	clusters := []*report.Cluster{}
	for _, p := range []string{"strongest", "second", "third", "fourth", "fifth", "sixth"} {
		clusters = append(clusters, &report.Cluster{Pattern: p})
	}
	c := transcriptCandidate(clusters)

	want := []string{"strongest", "second", "third", "fourth", "fifth"}
	if len(c.Examples) != len(want) {
		t.Fatalf("examples: got %d, want %d", len(c.Examples), len(want))
	}
	for i, w := range want {
		if c.Examples[i] != w {
			t.Errorf("position %d: got %q, want %q", i, c.Examples[i], w)
		}
	}
}

func TestSortCandidatesGroupsByCorpus(t *testing.T) {
	cs := []Candidate{
		{Corpus: CorpusTranscript, UngroupedClusters: 9},
		{Corpus: CorpusReviewFindings, UndecidedChains: 90},
	}
	sortCandidates(cs)
	if cs[0].Corpus != CorpusReviewFindings {
		t.Errorf("review findings order first: got %q first", cs[0].Corpus)
	}
}
