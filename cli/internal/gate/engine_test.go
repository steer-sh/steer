package gate

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Decision-flow cases: the chain, the cap, the cleared record, and the ledger rows each
// branch writes. Driven through a stub reviewer, because the branch structure is what
// is under test and a real panel would make it a model test instead.

// stubReviewer answers from a script, one entry per call, repeating the last.
type stubReviewer struct {
	replies []RawRun
	calls   int
}

func (s *stubReviewer) Name() string       { return "stub" }
func (s *stubReviewer) ModelLabel() string { return "stub-model" }

func (s *stubReviewer) Review(context.Context, string, string) RawRun {
	reply := s.replies[min(s.calls, len(s.replies)-1)]
	s.calls++
	return reply
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func findingReply(text string) RawRun {
	return RawRun{Response: text, Status: "SUCCESS"}
}

const oneFinding = "[1] SEVERITY: BLOCKING\n    FILE: a.py\n    CLAIM: x.\n    FAILS WHEN: y.\n"

// harness wires an engine over a temp state dir and a real repo.
type harness struct {
	t        *testing.T
	cfg      Config
	repo     string
	reviewer *stubReviewer
}

func newHarness(t *testing.T, replies ...RawRun) *harness {
	t.Helper()
	cfg := Defaults()
	cfg.StateDir = t.TempDir()
	cfg.ReviewerRuns = 2
	repo := buildRepo(t, map[string]string{"a.py": lines(120)})
	return &harness{t: t, cfg: cfg, repo: repo, reviewer: &stubReviewer{replies: replies}}
}

func (h *harness) handle() Decision {
	h.t.Helper()
	engine := NewEngine(h.cfg, h.reviewer)
	return engine.Handle(context.Background(), Request{
		Repo:      h.repo,
		Actor:     "session-1",
		Channel:   ChannelPreToolUse,
		SessionID: "session-1",
		Spec:      DiffSpec{StagedOnly: true},
		Message:   "subject",
	})
}

// rows reads back every adjudication row written so far.
func (h *harness) rows() []map[string]any {
	h.t.Helper()
	raw, err := os.ReadFile(h.cfg.LogFile())
	if err != nil {
		return nil
	}
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line == "" {
			continue
		}
		var row map[string]any
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			h.t.Fatalf("unreadable row %q: %v", line, err)
		}
		out = append(out, row)
	}
	return out
}

func (h *harness) lastRow() map[string]any {
	rows := h.rows()
	if len(rows) == 0 {
		h.t.Fatal("no adjudication row was written")
	}
	return rows[len(rows)-1]
}

func TestCleanPassAllows(t *testing.T) {
	h := newHarness(t, findingReply("NO FINDINGS"))
	d := h.handle()
	if d.Kind != Allow {
		t.Fatalf("Kind = %v, want Allow", d.Kind)
	}
	if d.LedgerError != nil {
		t.Fatalf("ledger error: %v", d.LedgerError)
	}
	row := h.lastRow()
	if row["phase"] != "clean" {
		t.Errorf("phase = %v, want clean", row["phase"])
	}
	if row["channel"] != ChannelPreToolUse {
		t.Errorf("channel = %v, want %s — a reader must be able to tell an absent "+
			"field from a lost one", row["channel"], ChannelPreToolUse)
	}
	if row["instrument"] != "stub-model/2/"+PromptDigest()+"/go" {
		t.Errorf("instrument = %v — it must name the reviewer that actually ran", row["instrument"])
	}
	if row["diff_file"] == nil {
		t.Error("a reviewed round must save its diff beside the log")
	}
}

func TestFindingsDeny(t *testing.T) {
	h := newHarness(t, findingReply(oneFinding))
	d := h.handle()
	if d.Kind != Deny {
		t.Fatalf("Kind = %v, want Deny", d.Kind)
	}
	if !strings.Contains(d.Reason, oneFinding) {
		t.Error("the deny must carry the reviewers' raw output")
	}
	if !strings.Contains(d.SystemMessage, "gate FIRED (round 1/") {
		t.Errorf("the operator receipt must name the round: %q", d.SystemMessage)
	}
	row := h.lastRow()
	if row["phase"] != "deny" || row["finding_slots"] != float64(2) {
		t.Errorf("phase=%v finding_slots=%v, want deny/2", row["phase"], row["finding_slots"])
	}
	if row["severity_lines_loose"] == nil {
		t.Error("the loose count must ride the row for measurement")
	}
}

// The chain's real stop: a retry whose diff is byte-identical was already reviewed, so
// a re-review would read no new code.
func TestUnchangedRetryEndsChain(t *testing.T) {
	h := newHarness(t, findingReply(oneFinding))
	if d := h.handle(); d.Kind != Deny {
		t.Fatalf("first round Kind = %v, want Deny", d.Kind)
	}
	d := h.handle()
	if d.Kind != NoteOnly {
		t.Fatalf("retry Kind = %v, want NoteOnly — the chain end must not also start "+
			"auto-approving", d.Kind)
	}
	if !strings.Contains(d.SystemMessage, "byte-identical") {
		t.Errorf("the receipt must say why the chain ended: %q", d.SystemMessage)
	}
	rows := h.rows()
	retry := rows[len(rows)-1]
	if retry["phase"] != "retry" || retry["diff_changed"] != false {
		t.Errorf("retry row = %v, want phase retry with diff_changed false", retry)
	}
	if retry["ledger_expected"] != true {
		t.Error("a fired gate expects a ledger, and that expectation rides the row")
	}
	// A third attempt presents the same diff the gate cleared, so it must not re-enter
	// as firing 1 and re-review adjudicated code.
	third := h.handle()
	if third.Kind != NoteOnly || !strings.Contains(third.SystemMessage, "already") {
		t.Errorf("third attempt = %v / %q, want the cleared-diff skip", third.Kind, third.SystemMessage)
	}
	if got := h.lastRow()["phase"]; got != "skip-cleared" {
		t.Errorf("phase = %v, want skip-cleared — an invisible skip corrupts the log in "+
			"the direction that flatters the gate", got)
	}
}

// A retry whose diff CHANGED reviews code no reviewer has seen, so the gate re-fires.
func TestChangedRetryRefires(t *testing.T) {
	h := newHarness(t, findingReply(oneFinding))
	if d := h.handle(); d.Kind != Deny {
		t.Fatalf("first round Kind = %v, want Deny", d.Kind)
	}
	writeFileAt(t, h.repo, "a.py", lines(120)+"fixed = 1\n")
	gitCmd(t, h.repo, "add", "-A")

	d := h.handle()
	if d.Kind != Deny {
		t.Fatalf("second round Kind = %v, want Deny", d.Kind)
	}
	if !strings.Contains(d.SystemMessage, "round 2/") {
		t.Errorf("the receipt must name round 2: %q", d.SystemMessage)
	}
	if !strings.Contains(d.Reason, "This is firing 2 of at most") {
		t.Error("firing 2 must carry the round note that makes a repeat cheap to dismiss")
	}
}

// The cap stops the BLOCKING chain, not the reading of the last round's fixes.
func TestCapEndsWithTerminalReview(t *testing.T) {
	h := newHarness(t, findingReply(oneFinding))
	h.cfg.MaxFirings = 2
	for round := 1; round <= 2; round++ {
		if d := h.handle(); d.Kind != Deny {
			t.Fatalf("round %d Kind = %v, want Deny", round, d.Kind)
		}
		writeFileAt(t, h.repo, "a.py", lines(120+round)+"fix\n")
		gitCmd(t, h.repo, "add", "-A")
	}
	d := h.handle()
	if d.Kind != ContextOnly {
		t.Fatalf("capped Kind = %v, want ContextOnly — the commit keeps its normal "+
			"permission path while the findings still reach the agent", d.Kind)
	}
	if !strings.Contains(d.Reason, "ENDED at its") {
		t.Error("the terminal message must say the chain ended and is not blocking")
	}
	if got := h.lastRow()["phase"]; got != "cap" {
		t.Errorf("phase = %v, want cap", got)
	}
}

// Every reviewer unavailable means the change went through unreviewed, and a pass the
// log does not carry reads as a change that never happened.
func TestAllReviewersUnavailableLogsUngated(t *testing.T) {
	h := newHarness(t, RawRun{Outcome: OutcomeTimeout, Detail: "no response"})
	d := h.handle()
	if d.Kind != Allow {
		t.Fatalf("Kind = %v, want Allow — the gate fails open", d.Kind)
	}
	if !strings.Contains(d.SystemMessage, "ungated") {
		t.Errorf("the operator must be told the change was not reviewed: %q", d.SystemMessage)
	}
	row := h.lastRow()
	if row["phase"] != "ungated" || row["reviewers"] != float64(0) {
		t.Errorf("row = %v, want phase ungated with 0 reviewers", row)
	}
	outcomes := row["reviewer_outcomes"].(map[string]any)
	if outcomes["timeout"] != float64(2) {
		t.Errorf("reviewer_outcomes = %v — a thin panel must be diagnosable from the "+
			"ledger alone", outcomes)
	}
	// Nothing was reviewed, so nothing is cleared: the next attempt must review.
	if h.reviewer.calls != 2 {
		t.Fatalf("reviewer calls = %d, want 2", h.reviewer.calls)
	}
	h.handle()
	if h.reviewer.calls != 4 {
		t.Error("an ungated pass must not write a cleared record")
	}
}

func TestProseOnlySkips(t *testing.T) {
	cfg := Defaults()
	cfg.StateDir = t.TempDir()
	repo := buildRepo(t, map[string]string{"NOTES.md": lines(120)})
	reviewer := &stubReviewer{replies: []RawRun{findingReply(oneFinding)}}
	engine := NewEngine(cfg, reviewer)
	d := engine.Handle(context.Background(), Request{
		Repo: repo, Actor: "s", Channel: ChannelPreToolUse, SessionID: "s",
		Spec: DiffSpec{StagedOnly: true}, Message: "notes",
	})
	if d.Kind != Silent {
		t.Fatalf("Kind = %v, want Silent", d.Kind)
	}
	if reviewer.calls != 0 {
		t.Errorf("reviewer ran %d times on a prose-only change", reviewer.calls)
	}
	raw, err := os.ReadFile(filepath.Join(cfg.StateDir, filepath.Base(cfg.LogFile())))
	if err != nil {
		t.Fatalf("the skip must still be logged: %v", err)
	}
	if !strings.Contains(string(raw), `"phase":"skip-prose"`) {
		t.Errorf("row = %s, want a skip-prose phase", raw)
	}
}

// A sidecar failure precedes the row, so it rides that same row rather than being
// swallowed. The gate is an ephemeral process and cannot carry it anywhere else.
func TestSidecarFailureRidesTheRow(t *testing.T) {
	cfg := Defaults()
	cfg.StateDir = t.TempDir()
	ledger := NewLedger(cfg)
	// A file where the diffs directory must go, so the write cannot succeed.
	if err := os.WriteFile(cfg.DiffDir(), []byte("blocker"), 0o644); err != nil {
		t.Fatal(err)
	}
	if fields := ledger.SaveDiff("s", 1, "diff"); len(fields) != 0 {
		t.Errorf("SaveDiff = %v, want no fields when the write failed", fields)
	}
	if err := ledger.Log(Row{"phase": "deny"}); err != nil {
		t.Fatalf("the row itself must still be written: %v", err)
	}
	raw, err := os.ReadFile(cfg.LogFile())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "gate_errors") {
		t.Errorf("row = %s, want the sidecar failure recorded on it", raw)
	}
}

// A failure of the ledger append itself has no disk to record on, so it is returned for
// the adapter to report on the operator channel.
func TestLedgerAppendFailureIsReturned(t *testing.T) {
	cfg := Defaults()
	cfg.StateDir = filepath.Join(t.TempDir(), "blocked", "state")
	if err := os.MkdirAll(filepath.Dir(cfg.StateDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg.StateDir, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := NewLedger(cfg).Log(Row{"phase": "deny"}); err == nil {
		t.Error("a ledger append that cannot happen must be reported, not swallowed")
	}
}

// The stat-only fallback renders SHORTER than the raw diff it replaces, so testing the
// rendered length against MinDiffChars made the smallest-commit filter discard the
// largest commits: a one-file change too big to inline was never reviewed.
func TestOverBudgetSingleFileIsReviewed(t *testing.T) {
	h := newHarness(t, findingReply("NO FINDINGS"))
	h.cfg.MaxDiffChars = 500
	h.repo = buildRepo(t, map[string]string{"a.py": lines(300)})
	d := h.handle()
	if d.Kind != Allow {
		t.Fatalf("Kind = %v, want Allow", d.Kind)
	}
	row := h.lastRow()
	if row["phase"] != "clean" {
		t.Fatalf("phase = %v, want clean — the commit is one file over the diff budget, "+
			"which the stat-only fallback exists to review anyway", row["phase"])
	}
}

// The chain's changed-since-last-round test keys on the RAW diff. Two different
// versions of one over-budget file render to the same stat block, so a rendered
// comparison would end the chain on "byte-identical" without reading the fix.
func TestRetryOnOverBudgetFileSeesTheEdit(t *testing.T) {
	h := newHarness(t, findingReply(oneFinding), findingReply("NO FINDINGS"))
	h.cfg.MaxDiffChars = 500
	h.repo = buildRepo(t, map[string]string{"a.py": lines(300)})
	if d := h.handle(); d.Kind != Deny {
		t.Fatalf("first firing Kind = %v, want Deny", d.Kind)
	}
	// An edit of the same byte length: the stat block and the raw char count both
	// stay put, so only the raw text itself can report the change.
	writeFileAt(t, h.repo, "a.py", strings.Replace(lines(300), "line x", "line y", 1))
	gitCmd(t, h.repo, "add", "-A")
	d := h.handle()
	if d.Kind != Allow {
		t.Fatalf("retry Kind = %v, want Allow — the retry diff differs and must be reviewed", d.Kind)
	}
	row := h.lastRow()
	if row["phase"] != "clean" {
		t.Fatalf("phase = %v, want clean — a re-review, not a byte-identical chain end", row["phase"])
	}
}
