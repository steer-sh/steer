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
