package cadence

import (
	"path/filepath"
	"testing"
	"time"
)

func pendingQueue() *Queue {
	return &Queue{
		Version:    QueueVersion,
		Candidates: []Candidate{{Corpus: CorpusTranscript, UngroupedClusters: 6, Examples: []string{"no bare python"}}},
	}
}

func TestShouldNudgeFirstTimeOnPendingQueue(t *testing.T) {
	dir := t.TempDir()
	q := pendingQueue()
	emit, rec := ShouldNudge(dir, q, "hash-a", time.Now())
	if !emit {
		t.Fatal("a pending queue with no prior nudge must emit")
	}
	if err := WriteNudge(dir, rec); err != nil {
		t.Fatal(err)
	}
}

func TestShouldNotNudgeEmptyQueue(t *testing.T) {
	dir := t.TempDir()
	q := &Queue{Version: QueueVersion, Unscored: []string{"x"}, Corpora: []CorpusStatus{{Corpus: CorpusTranscript}}}
	if emit, _ := ShouldNudge(dir, q, "hash-a", time.Now()); emit {
		t.Error("a queue with only unscored rows and a sweep must not nudge")
	}
}

func TestShouldNotReNudgeSameQueueSameCycle(t *testing.T) {
	dir := t.TempDir()
	q := pendingQueue()
	refresh := time.Now()
	_, rec := ShouldNudge(dir, q, "hash-a", refresh)
	if err := WriteNudge(dir, rec); err != nil {
		t.Fatal(err)
	}
	if emit, _ := ShouldNudge(dir, q, "hash-a", refresh); emit {
		t.Error("the same queue on the same refresh cycle must stay silent")
	}
}

func TestReNudgeWhenHashChanges(t *testing.T) {
	dir := t.TempDir()
	q := pendingQueue()
	refresh := time.Now()
	_, rec := ShouldNudge(dir, q, "hash-a", refresh)
	if err := WriteNudge(dir, rec); err != nil {
		t.Fatal(err)
	}
	if emit, _ := ShouldNudge(dir, q, "hash-b", refresh); !emit {
		t.Error("a changed queue hash must re-nudge")
	}
}

func TestReNudgeWhenNewCycleRan(t *testing.T) {
	dir := t.TempDir()
	q := pendingQueue()
	first := time.Now()
	_, rec := ShouldNudge(dir, q, "hash-a", first)
	if err := WriteNudge(dir, rec); err != nil {
		t.Fatal(err)
	}
	// Same pending queue (identical hash), but a later refresh cycle ran: re-surface
	// so an unseen nudge is not buried forever.
	later := first.Add(8 * 24 * time.Hour)
	if emit, _ := ShouldNudge(dir, q, "hash-a", later); !emit {
		t.Error("a newer refresh cycle must re-surface a still-pending queue")
	}
}

func TestQueueHashStableAndSensitive(t *testing.T) {
	if QueueHash([]byte("same")) != QueueHash([]byte("same")) {
		t.Error("identical bytes must hash identically")
	}
	if QueueHash([]byte("a")) == QueueHash([]byte("b")) {
		t.Error("different bytes must hash differently")
	}
}

func TestPendingCounts(t *testing.T) {
	q := &Queue{
		Interventions: []Intervention{{ID: "a"}, {ID: "b"}},
		Candidates:    []Candidate{{Corpus: CorpusTranscript}},
	}
	cand, ineff := PendingCounts(q, ReadMarks{})
	if cand != 1 || ineff != 2 {
		t.Errorf("want 1 candidate and 2 ineffective, got %d and %d", cand, ineff)
	}
}

// A one-off finding stays in the review pile after a pass reads it, so the count
// that sends the user to /steer-report must stop at the read mark, and resume when
// the row changes: a new chain, or one a retired class returns to the pile.
func TestReadMarkStopsCountingAPileAPassRead(t *testing.T) {
	dir := t.TempDir()
	q := &Queue{Candidates: []Candidate{
		{Corpus: CorpusReviewFindings, UndecidedChains: 1, Dump: "d"},
		{Corpus: CorpusTranscript, UngroupedClusters: 2, Examples: []string{"x"}},
	}}
	if cand, _ := PendingCounts(q, LoadReadMarks(dir)); cand != 2 {
		t.Fatalf("before any pass: %d unread rows, want 2", cand)
	}
	// A pass that read only the review pile leaves the transcript pile unread.
	if _, err := MarkRead(dir, q.Candidates, []string{CorpusReviewFindings}); err != nil {
		t.Fatal(err)
	}
	if cand, _ := PendingCounts(q, LoadReadMarks(dir)); cand != 1 {
		t.Errorf("after a pass read one pile: %d unread rows, want 1", cand)
	}
	if _, err := MarkRead(dir, q.Candidates, []string{CorpusReviewFindings, CorpusTranscript}); err != nil {
		t.Fatal(err)
	}
	if cand, _ := PendingCounts(q, LoadReadMarks(dir)); cand != 0 {
		t.Errorf("after the pass read both: %d unread rows, want 0", cand)
	}
	if ok, _ := ShouldNudge(dir, q, "h", time.Now()); ok {
		t.Error("a pile the pass read must not nudge")
	}
	q.Candidates[0].UndecidedChains = 2
	if cand, _ := PendingCounts(q, LoadReadMarks(dir)); cand != 1 {
		t.Errorf("a changed review row: %d unread rows, want 1", cand)
	}
	if ok, _ := ShouldNudge(dir, q, "h2", time.Now()); !ok {
		t.Error("a changed pile must nudge")
	}
	// A pile that empties drops its mark at the next refresh, so a later pile of
	// the read size is new.
	if err := WriteQueue(dir, &Queue{Candidates: q.Candidates[1:]}, time.Now()); err != nil {
		t.Fatal(err)
	}
	q.Candidates[0].UndecidedChains = 1
	if cand, _ := PendingCounts(q, LoadReadMarks(dir)); cand != 1 {
		t.Errorf("a pile after an empty refresh: %d unread rows, want 1", cand)
	}
}

// TestMasterNudgeFiresOncePerKey pins that a stale guard master is a standing
// condition: it nudges when the key changes and stays quiet after.
func TestMasterNudgeFiresOncePerKey(t *testing.T) {
	dir := t.TempDir()

	if ShouldNudgeMaster(dir, "") {
		t.Error("an empty key nudged")
	}
	if !ShouldNudgeMaster(dir, "claude-code/global:stale:aaaaaaaa") {
		t.Fatal("a first stale master did not nudge")
	}
	if err := WriteNudge(dir, LoadNudge(dir).WithMaster("claude-code/global:stale:aaaaaaaa")); err != nil {
		t.Fatalf("WriteNudge returned error: %v", err)
	}
	if ShouldNudgeMaster(dir, "claude-code/global:stale:aaaaaaaa") {
		t.Error("the same stale master nudged twice")
	}
	if !ShouldNudgeMaster(dir, "claude-code/global:stale:bbbbbbbb") {
		t.Error("a different stale runtime did not nudge")
	}

	// A repaired master clears the key, so the same staleness nudges again if it
	// comes back. The cadence hook writes this even when it emits nothing.
	if err := WriteNudge(dir, LoadNudge(dir).WithMaster("")); err != nil {
		t.Fatalf("WriteNudge returned error: %v", err)
	}
	if !ShouldNudgeMaster(dir, "claude-code/global:stale:aaaaaaaa") {
		t.Error("a returning staleness did not nudge after the key was cleared")
	}
}

// A drifted rules section nudges once per key, and a cleared key lets the same
// drift nudge again when it comes back.
func TestRulesNudgeFiresOncePerKey(t *testing.T) {
	dir := t.TempDir()
	key := "codex/global:/h/.codex/AGENTS.md:config-path:0"

	if ShouldNudgeRules(dir, "") {
		t.Error("an empty key nudged")
	}
	if !ShouldNudgeRules(dir, key) {
		t.Fatal("a first drift did not nudge")
	}
	if err := WriteNudge(dir, LoadNudge(dir).WithRules(key)); err != nil {
		t.Fatal(err)
	}
	if ShouldNudgeRules(dir, key) {
		t.Error("the same drift nudged twice")
	}
	if err := WriteNudge(dir, LoadNudge(dir).WithRules("")); err != nil {
		t.Fatal(err)
	}
	if !ShouldNudgeRules(dir, key) {
		t.Error("a returning drift did not nudge after the key was cleared")
	}
}

// The queue nudge rewrites the record, so it must carry the rules key through, or
// the drift line repeats in the next session.
func TestQueueNudgeCarriesTheRulesKey(t *testing.T) {
	dir := t.TempDir()
	key := "claude-code/project:CLAUDE.md::1"
	if err := WriteNudge(dir, LoadNudge(dir).WithRules(key)); err != nil {
		t.Fatal(err)
	}
	q := &Queue{Candidates: []Candidate{{Corpus: "transcript-friction", UngroupedClusters: 1}}}
	_, rec := ShouldNudge(dir, q, "hash", time.Now())
	if rec.Rules != key {
		t.Fatalf("rules key: got %q, want %q", rec.Rules, key)
	}
}

// TestQueueNudgeCarriesTheMasterKey pins that the two nudges share one record:
// emitting a queue line must not silently clear the master key.
func TestQueueNudgeCarriesTheMasterKey(t *testing.T) {
	dir := t.TempDir()
	key := "claude-code/global:unknown:"
	if err := WriteNudge(dir, LoadNudge(dir).WithMaster(key)); err != nil {
		t.Fatalf("WriteNudge returned error: %v", err)
	}
	q := &Queue{Candidates: []Candidate{{Corpus: "transcript-friction", UngroupedClusters: 1}}}
	_, rec := ShouldNudge(dir, q, "hash", time.Now())
	if rec.Master != key {
		t.Fatalf("master key: got %q, want %q", rec.Master, key)
	}
}

// A differing plugin copy nudges once per key, and a cleared key lets the same
// copy nudge again when it differs again.
func TestPluginsNudgeFiresOncePerKey(t *testing.T) {
	dir := t.TempDir()
	key := "codex:/h/.codex/plugins/cache/m/steer/0.1.0"

	if ShouldNudgePlugins(dir, "") {
		t.Error("an empty key nudged")
	}
	if !ShouldNudgePlugins(dir, key) {
		t.Fatal("a first differing copy did not nudge")
	}
	if err := WriteNudge(dir, LoadNudge(dir).WithPlugins(key)); err != nil {
		t.Fatal(err)
	}
	if ShouldNudgePlugins(dir, key) {
		t.Error("the same differing copy nudged twice")
	}
	if err := WriteNudge(dir, LoadNudge(dir).WithPlugins("")); err != nil {
		t.Fatal(err)
	}
	if !ShouldNudgePlugins(dir, key) {
		t.Error("a copy that differs again did not nudge after the key was cleared")
	}
}

// The first session on a fresh machine writes its record before any refresh has
// made the state directory.
func TestWriteNudgeCreatesTheStateDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "steer-report")
	if err := WriteNudge(dir, LoadNudge(dir).WithPlugins("claude-code:/cache/steer")); err != nil {
		t.Fatalf("WriteNudge returned error: %v", err)
	}
	if ShouldNudgePlugins(dir, "claude-code:/cache/steer") {
		t.Error("the same plugin copy nudged twice")
	}
}
