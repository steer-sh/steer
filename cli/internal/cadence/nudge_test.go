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

// The first session on a fresh machine writes its record before any refresh has
// made the state directory.
func TestWriteNudgeCreatesTheStateDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "steer-report")
	rec := LoadNudge(dir)
	rec.Plugins = "claude-code:/cache/steer"
	if err := WriteNudge(dir, rec); err != nil {
		t.Fatalf("WriteNudge returned error: %v", err)
	}
	if Due(LoadNudge(dir).Plugins, "claude-code:/cache/steer") {
		t.Error("the same plugin copy nudged twice")
	}
}

// standingNudge is one condition that nudges once per distinct key: its record
// field and a key.
type standingNudge struct {
	name  string
	field func(*nudgeRecord) *string
	key   string
}

var standingNudges = []standingNudge{
	{"master", func(r *nudgeRecord) *string { return &r.Master }, "claude-code/global:stale:aaaaaaaa"},
	{"rules", func(r *nudgeRecord) *string { return &r.Rules }, "codex/global:/h/.codex/AGENTS.md:config-path:0"},
	{"detached", func(r *nudgeRecord) *string { return &r.Detached }, "codex/global"},
	{"source drift", func(r *nudgeRecord) *string { return &r.SourceDrift }, "claude-code/global:no-bare-python"},
	{"plugins", func(r *nudgeRecord) *string { return &r.Plugins }, "codex:/h/.codex/plugins/cache/m/steer/0.1.0"},
}

// due reads the record from dir and decides n's key against it.
func (n standingNudge) due(dir, key string) bool {
	rec := LoadNudge(dir)
	return Due(*n.field(&rec), key)
}

// write persists the record from dir with n's field set to key.
func (n standingNudge) write(t *testing.T, dir, key string) {
	t.Helper()
	rec := LoadNudge(dir)
	*n.field(&rec) = key
	if err := WriteNudge(dir, rec); err != nil {
		t.Fatal(err)
	}
}

// A standing condition nudges when its key changes and stays quiet after. A
// repaired condition clears the key, which the cadence hook writes even when it
// emits nothing, so the same condition nudges again if it comes back.
// pins existing behaviour
func TestStandingNudgesFireOncePerKey(t *testing.T) {
	for _, n := range standingNudges {
		t.Run(n.name, func(t *testing.T) {
			dir := t.TempDir()
			if n.due(dir, "") {
				t.Error("an empty key nudged")
			}
			if !n.due(dir, n.key) {
				t.Fatal("a first key did not nudge")
			}
			n.write(t, dir, n.key)
			if n.due(dir, n.key) {
				t.Error("the same key nudged twice")
			}
			if !n.due(dir, n.key+"-other") {
				t.Error("a different key did not nudge")
			}
			n.write(t, dir, "")
			if !n.due(dir, n.key) {
				t.Error("a returning key did not nudge after it was cleared")
			}
		})
	}
}

// The standing nudges share one record with the queue nudge, which rewrites it:
// a queue line must carry every standing key through, or that line repeats in
// the next session.
// pins existing behaviour
func TestQueueNudgeCarriesEveryStandingKey(t *testing.T) {
	for _, n := range standingNudges {
		t.Run(n.name, func(t *testing.T) {
			dir := t.TempDir()
			n.write(t, dir, n.key)
			q := &Queue{Candidates: []Candidate{{Corpus: "transcript-friction", UngroupedClusters: 1}}}
			if _, rec := ShouldNudge(dir, q, "hash", time.Now()); *n.field(&rec) != n.key {
				t.Fatalf("key: got %q, want %q", *n.field(&rec), n.key)
			}
		})
	}
}
