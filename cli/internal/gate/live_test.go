package gate

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// heldReviewer answers only after release is closed, and says on entered when a run
// has started, so a test can read the live review file while a round runs.
type heldReviewer struct {
	reply   RawRun
	entered chan struct{}
	release chan struct{}
}

func newHeldReviewer(reply RawRun) *heldReviewer {
	return &heldReviewer{reply: reply, entered: make(chan struct{}, 16), release: make(chan struct{})}
}

func (r *heldReviewer) Name() string       { return "stub" }
func (r *heldReviewer) ModelLabel() string { return "stub-model" }

func (r *heldReviewer) Review(ctx context.Context, _, _ string) RawRun {
	r.entered <- struct{}{}
	select {
	case <-r.release:
	case <-ctx.Done():
	}
	return r.reply
}

// Bound both notifications and completion: mutation probes can bypass the reviewer.
const heldWait = 5 * time.Second

func (h *harness) startHeld(reviewer *heldReviewer) (<-chan Decision, func()) {
	h.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan Decision, 1)
	exited := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(reviewer.release) }) }
	h.t.Cleanup(func() {
		release()
		cancel()
		select {
		case <-exited:
		case <-time.After(heldWait):
			h.t.Error("the held review did not stop during cleanup")
		}
	})
	go func() {
		defer close(exited)
		done <- NewEngine(h.cfg, reviewer).Handle(ctx, Request{
			Repo: h.repo, Actor: "session-1", Channel: ChannelPreToolUse,
			SessionID: "session-1", Spec: h.spec, Message: "subject",
		})
	}()
	return done, release
}

func (h *harness) waitHeldStart(reviewer *heldReviewer, done <-chan Decision) {
	h.t.Helper()
	select {
	case <-reviewer.entered:
	case <-done:
		h.t.Fatal("the review ended before every reviewer started")
	case <-time.After(heldWait):
		h.t.Fatal("the reviewer never started")
	}
}

func (h *harness) waitHeldDone(done <-chan Decision) Decision {
	h.t.Helper()
	select {
	case d := <-done:
		return d
	case <-time.After(heldWait):
		h.t.Fatal("the review never finished after releasing its reviewers")
		return Decision{}
	}
}

// liveFiles reads every live review file of the harness's session.
func (h *harness) liveFiles() []LiveReview {
	h.t.Helper()
	paths, err := filepath.Glob(filepath.Join(h.cfg.LiveDir(), "session-1", "*.json"))
	if err != nil {
		h.t.Fatal(err)
	}
	var out []LiveReview
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			h.t.Fatal(err)
		}
		var doc LiveReview
		if err := json.Unmarshal(raw, &doc); err != nil {
			h.t.Fatalf("unreadable live review file %s: %v\n%s", path, err, raw)
		}
		out = append(out, doc)
	}
	return out
}

// theLiveFile is the session's one live review file.
func (h *harness) theLiveFile() LiveReview {
	h.t.Helper()
	files := h.liveFiles()
	if len(files) != 1 {
		h.t.Fatalf("live review files = %d, want 1", len(files))
	}
	return files[0]
}

// handleHeld runs one commit through a held reviewer and returns the live review file
// as it stood while every run was reading, then the decision.
func (h *harness) handleHeld(reviewer *heldReviewer) (LiveReview, Decision) {
	h.t.Helper()
	done, release := h.startHeld(reviewer)
	for i := 0; i < h.cfg.ReviewerRuns; i++ {
		h.waitHeldStart(reviewer, done)
	}
	running := h.theLiveFile()
	release()
	return running, h.waitHeldDone(done)
}

func TestARunningReviewIsRecordedWhileItsReviewersRead(t *testing.T) {
	h := newHarness(t)
	before := time.Now().UnixMilli()
	running, _ := h.handleHeld(newHeldReviewer(findingReply(oneFinding)))

	if running.State != LiveRunning || running.Outcome != "" || running.EndedMS != 0 {
		t.Errorf("state=%q outcome=%q ended_ms=%d, want a running review with no outcome",
			running.State, running.Outcome, running.EndedMS)
	}
	if running.SessionID != "session-1" || running.Repo != h.repo {
		t.Errorf("session=%q repo=%q, want session-1 and %q", running.SessionID, running.Repo, h.repo)
	}
	if running.Round != 1 || running.MaxRounds != h.cfg.MaxFirings || running.Final {
		t.Errorf("round=%d max_rounds=%d final=%v, want 1 of %d, not final",
			running.Round, running.MaxRounds, running.Final, h.cfg.MaxFirings)
	}
	if running.Reviewer != "stub" || running.Model != "stub-model" || running.Runs != 2 {
		t.Errorf("reviewer=%q model=%q runs=%d, want what the reviewer names and 2 runs",
			running.Reviewer, running.Model, running.Runs)
	}
	if len(running.RunsEnded) != 0 {
		t.Errorf("runs_ended = %v while every run still reads", running.RunsEnded)
	}
	if running.StartedMS < before || running.BeatMS < running.StartedMS ||
		running.BeatEveryMS != liveBeatEvery.Milliseconds() ||
		running.ReviewTimeoutS != int(h.cfg.ReviewTimeout.Seconds()) || running.PID != os.Getpid() {
		t.Errorf("staleness fields are wrong: %+v", running)
	}
}

func TestAHeldRoundEndsItsLiveFileWithItsFindings(t *testing.T) {
	h := newHarness(t)
	_, d := h.handleHeld(newHeldReviewer(findingReply(oneFinding)))
	if d.Kind != Deny {
		t.Fatalf("Kind = %v, want Deny", d.Kind)
	}
	ended := h.theLiveFile()
	if ended.State != LiveEnded || ended.Outcome != "deny" || ended.Answered != 2 || ended.EndedMS < ended.StartedMS {
		t.Errorf("state=%q outcome=%q answered=%d, want an ended deny with 2 answers",
			ended.State, ended.Outcome, ended.Answered)
	}
	want := LiveFinding{Severity: "BLOCKING", File: "a.py", Claim: "x."}
	if len(ended.Findings) != 2 || ended.Findings[0] != want || ended.Findings[1] != want {
		t.Errorf("findings = %+v, want the finding of each of the 2 reviews", ended.Findings)
	}
	seen := map[int]bool{}
	for _, run := range ended.RunsEnded {
		seen[run.Run] = run.Outcome == string(OutcomeOK)
	}
	if len(ended.RunsEnded) != 2 || !seen[1] || !seen[2] {
		t.Errorf("runs_ended = %+v, want runs 1 and 2, each ok", ended.RunsEnded)
	}
}

func TestACleanRoundEndsItsLiveFileWithoutFindings(t *testing.T) {
	h := newHarness(t, findingReply("NO FINDINGS"))
	if d := h.handle(); d.Kind != Allow {
		t.Fatalf("Kind = %v, want Allow", d.Kind)
	}
	ended := h.theLiveFile()
	if ended.State != LiveEnded || ended.Outcome != "clean" || len(ended.Findings) != 0 {
		t.Errorf("state=%q outcome=%q findings=%v, want an ended clean round with none",
			ended.State, ended.Outcome, ended.Findings)
	}
}

func TestTheFinalReviewIsMarkedInItsLiveFile(t *testing.T) {
	h := newHarness(t, findingReply(oneFinding))
	h.cfg.MaxFirings = 1
	h.handle()
	writeFileAt(t, h.repo, "a.py", lines(121)+"fix\n")
	gitCmd(t, h.repo, "add", "-A")
	if d := h.handle(); d.Kind != ContextOnly {
		t.Fatalf("Kind = %v, want ContextOnly", d.Kind)
	}
	ended := h.theLiveFile()
	if !ended.Final || ended.Outcome != "cap" || ended.Round != 2 || ended.MaxRounds != 1 {
		t.Errorf("final=%v outcome=%q round=%d max_rounds=%d, want the final review after 1 round",
			ended.Final, ended.Outcome, ended.Round, ended.MaxRounds)
	}
}

func TestTheRetryOfAHeldRoundRemovesItsLiveFile(t *testing.T) {
	h := newHarness(t, findingReply(oneFinding))
	h.handle()
	if len(h.liveFiles()) != 1 {
		t.Fatal("the held round left no live review file")
	}
	if d := h.handle(); d.Kind != ContextOnly {
		t.Fatalf("Kind = %v, want the unchanged retry let through", d.Kind)
	}
	if files := h.liveFiles(); len(files) != 0 {
		t.Errorf("live review files after the retry = %+v, want none", files)
	}
}

// An empty-index retry puts the held round back unanswered, so its live file stays.
func TestARetryThatKeepsTheHeldRoundKeepsItsLiveFile(t *testing.T) {
	h := newHarness(t, findingReply(oneFinding))
	h.withHistory()
	gitCmd(t, h.repo, "reset", "-q")
	h.spec = DiffSpec{}
	if d := h.handle(); d.Kind != Deny {
		t.Fatalf("first firing Kind = %v, want Deny", d.Kind)
	}
	h.spec = DiffSpec{StagedOnly: true}
	h.handle()
	chain := NewChain(h.cfg, ChainKey{Actor: "session-1", Repo: h.repo})
	if !chain.Pending(chain.Load()) {
		t.Fatal("the empty-index retry did not keep the chain")
	}
	if held := h.theLiveFile(); held.Outcome != "deny" {
		t.Errorf("outcome = %q, want the held round's deny", held.Outcome)
	}
}

func TestARunningLiveFileBeats(t *testing.T) {
	old := liveBeatEvery
	liveBeatEvery = 5 * time.Millisecond
	t.Cleanup(func() { liveBeatEvery = old })

	h := newHarness(t)
	reviewer := newHeldReviewer(findingReply("NO FINDINGS"))
	done, release := h.startHeld(reviewer)
	h.waitHeldStart(reviewer, done)
	first := h.theLiveFile()
	deadline := time.Now().Add(10 * time.Second)
	for h.theLiveFile().BeatMS <= first.BeatMS {
		if time.Now().After(deadline) {
			t.Fatal("a running review never rewrote its beat")
		}
		time.Sleep(5 * time.Millisecond)
	}
	release()
	h.waitHeldDone(done)
}

func TestAnUnwritableLiveDirLeavesTheReviewAsItWas(t *testing.T) {
	h := newHarness(t, findingReply(oneFinding))
	if err := os.WriteFile(h.cfg.LiveDir(), []byte("in the way"), 0o644); err != nil {
		t.Fatal(err)
	}
	d := h.handle()
	if d.Kind != Deny || d.LedgerError != nil {
		t.Fatalf("Kind=%v LedgerError=%v, want a deny with no ledger error", d.Kind, d.LedgerError)
	}
	if row := h.lastRow(); row["phase"] != "deny" || row["gate_errors"] != nil {
		t.Errorf("row phase=%v gate_errors=%v, want an ordinary deny row", row["phase"], row["gate_errors"])
	}
	if d := h.handle(); d.Kind != ContextOnly {
		t.Errorf("Kind = %v, want the unchanged retry let through", d.Kind)
	}
}

func TestACommitWithNoSessionKeepsNoLiveFile(t *testing.T) {
	h := newHarness(t, findingReply(oneFinding))
	d := NewEngine(h.cfg, h.reviewer).Handle(context.Background(), Request{
		Repo: h.repo, Actor: "HEAD", Channel: ChannelPreCommit, Spec: h.spec, Message: "subject",
	})
	if d.Kind != Deny {
		t.Fatalf("Kind = %v, want Deny", d.Kind)
	}
	if _, err := os.Stat(h.cfg.LiveDir()); !os.IsNotExist(err) {
		t.Errorf("a commit with no session created %s (stat: %v)", h.cfg.LiveDir(), err)
	}
}

func TestANewReviewPrunesLiveFilesPastStaleAfter(t *testing.T) {
	h := newHarness(t, findingReply("NO FINDINGS"))
	gone := filepath.Join(h.cfg.LiveDir(), "session-old")
	kept := filepath.Join(h.cfg.LiveDir(), "session-young")
	for _, dir := range []string{gone, kept} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "a.json"), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	past := time.Now().Add(-h.cfg.StaleAfter - time.Minute)
	if err := os.Chtimes(filepath.Join(gone, "a.json"), past, past); err != nil {
		t.Fatal(err)
	}
	h.handle()
	if _, err := os.Stat(gone); !os.IsNotExist(err) {
		t.Errorf("the stale session directory is still there (stat: %v)", err)
	}
	if _, err := os.Stat(filepath.Join(kept, "a.json")); err != nil {
		t.Errorf("a young live review file was pruned: %v", err)
	}
}

func TestALongClaimIsCutOnACharacterBoundary(t *testing.T) {
	claim := strings.Repeat("a", maxLiveClaimChars-1) + "é and more"
	got := truncateRunes(claim, maxLiveClaimChars)
	if got != strings.Repeat("a", maxLiveClaimChars-1) {
		t.Errorf("cut claim ends %q, want the whole characters before the limit", got[len(got)-4:])
	}
}
