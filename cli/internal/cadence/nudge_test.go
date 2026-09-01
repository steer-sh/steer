package cadence

import (
	"testing"
	"time"
)

func pendingQueue() *Queue {
	return &Queue{
		Version:    QueueVersion,
		Candidates: []Candidate{{Corpus: corpusTranscript, Pattern: "no bare python", Count: 6, Sessions: 4}},
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
	q := &Queue{Version: QueueVersion, Unscored: []string{"x"}, Corpora: []CorpusStatus{{Corpus: corpusTranscript}}}
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
		Candidates:    []Candidate{{Corpus: corpusTranscript}},
	}
	cand, ineff := PendingCounts(q)
	if cand != 1 || ineff != 2 {
		t.Errorf("want 1 candidate and 2 ineffective, got %d and %d", cand, ineff)
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

// TestQueueNudgeCarriesTheMasterKey pins that the two nudges share one record:
// emitting a queue line must not silently clear the master key.
func TestQueueNudgeCarriesTheMasterKey(t *testing.T) {
	dir := t.TempDir()
	key := "claude-code/global:unknown:"
	if err := WriteNudge(dir, LoadNudge(dir).WithMaster(key)); err != nil {
		t.Fatalf("WriteNudge returned error: %v", err)
	}
	q := &Queue{Candidates: []Candidate{{Corpus: "transcript-friction", Pattern: "c"}}}
	_, rec := ShouldNudge(dir, q, "hash", time.Now())
	if rec.Master != key {
		t.Fatalf("master key: got %q, want %q", rec.Master, key)
	}
}
