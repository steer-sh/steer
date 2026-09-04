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
	// spec is the tree the gate reads, which the real adapter takes from the shape of
	// the command. Varying it between rounds is what a dropped staging step looks like.
	spec DiffSpec
}

func newHarness(t *testing.T, replies ...RawRun) *harness {
	t.Helper()
	cfg := Defaults()
	cfg.StateDir = t.TempDir()
	cfg.ReviewerRuns = 2
	repo := buildRepo(t, map[string]string{"a.py": lines(120)})
	return &harness{t: t, cfg: cfg, repo: repo, reviewer: &stubReviewer{replies: replies},
		spec: DiffSpec{StagedOnly: true}}
}

func (h *harness) handle() Decision {
	h.t.Helper()
	engine := NewEngine(h.cfg, h.reviewer)
	return engine.Handle(context.Background(), Request{
		Repo:      h.repo,
		Actor:     "session-1",
		Channel:   ChannelPreToolUse,
		SessionID: "session-1",
		Spec:      h.spec,
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
	if row["instrument"] != "stub-model/2/"+PromptDigest()+"/go-findings-v3-shell" {
		t.Errorf("instrument = %v — it must name the reviewer that actually ran", row["instrument"])
	}
	if row["diff_file"] == nil {
		t.Error("a reviewed round must save its diff beside the log")
	}
}

func TestRowCarriesTheInstrumentByDimension(t *testing.T) {
	h := newHarness(t, findingReply("NO FINDINGS"))
	h.cfg.ReviewerEffort = "high"
	h.handle()
	row := h.lastRow()
	if row["instrument"] != "stub-model/2/"+PromptDigest()+"/go-findings-v3-shell/high" {
		t.Errorf("instrument = %v — a set effort is the fifth segment", row["instrument"])
	}
	fields, ok := row["instrument_fields"].(map[string]any)
	if !ok {
		t.Fatalf("instrument_fields = %v; want an object a grader can read by key", row["instrument_fields"])
	}
	want := map[string]any{
		"provider": "stub", "model": "stub-model", "effort": "high",
		"runs": float64(2), "prompt_digest": PromptDigest(), "engine": "go-findings-v3-shell",
	}
	for key, value := range want {
		if fields[key] != value {
			t.Errorf("instrument_fields[%q] = %v, want %v", key, fields[key], value)
		}
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

// withHistory gives the harness a repo that has a HEAD, so a diff against the working
// tree resolves. buildRepo alone leaves the first tree staged and unrecorded.
func (h *harness) withHistory() {
	h.t.Helper()
	root := buildRepo(h.t, map[string]string{"a.py": lines(120)})
	gitCmd(h.t, root, verb, "-m", "base")
	writeFileAt(h.t, root, "a.py", lines(200))
	gitCmd(h.t, root, "add", "-A")
	h.repo = root
}

// A round reviewed on the working tree, retried by a command that reads the index,
// with edits still unstaged: the retry can be reading code from before the fixes and
// nothing in the diff says so, so the agent has to be told.
func TestRetryReadingAStaleIndexTellsTheAgent(t *testing.T) {
	h := newHarness(t, findingReply(oneFinding), findingReply("NO FINDINGS"))
	h.withHistory()
	h.spec = DiffSpec{}
	if d := h.handle(); d.Kind != Deny {
		t.Fatalf("first firing Kind = %v, want Deny", d.Kind)
	}
	// The fix lands in the working tree and is never staged.
	writeFileAt(t, h.repo, "a.py", lines(160))
	h.spec = DiffSpec{StagedOnly: true}
	d := h.handle()
	if !strings.Contains(d.Reason, "REVIEWS THE INDEX") {
		t.Fatalf("Reason did not carry the stale-index notice: %q", d.Reason)
	}
	if d.Kind == Allow {
		t.Error("Kind = Allow — a commit reviewed on a possibly stale index must not " +
			"also skip the permission prompt")
	}
	var retry map[string]any
	for _, row := range h.rows() {
		if row["phase"] == "retry" {
			retry = row
		}
	}
	if retry == nil || retry["spec_changed"] != true {
		t.Fatalf("retry row = %v, want spec_changed true", retry)
	}
}

// Staging everything and then re-issuing the bare command is the ordinary way to
// recover from a denied bundle. Nothing is left unstaged there, so the notice must
// stay quiet even though the two commands read different trees.
func TestRetryAfterStagingEverythingIsQuiet(t *testing.T) {
	h := newHarness(t, findingReply(oneFinding), findingReply("NO FINDINGS"))
	h.withHistory()
	h.spec = DiffSpec{}
	if d := h.handle(); d.Kind != Deny {
		t.Fatalf("first firing Kind = %v, want Deny", d.Kind)
	}
	writeFileAt(t, h.repo, "a.py", lines(160))
	writeFileAt(t, h.repo, "scratch.txt", "untracked, and nobody's fix\n")
	gitCmd(t, h.repo, "add", "a.py")
	h.spec = DiffSpec{StagedOnly: true}
	d := h.handle()
	if strings.Contains(d.Reason, "REVIEWS THE INDEX") {
		t.Fatalf("stale-index notice fired on a fully staged retry: %q", d.Reason)
	}
	if d.Kind != Allow {
		t.Fatalf("Kind = %v, want Allow", d.Kind)
	}
}

// A retry that reads the WORKING TREE sees every edit the last round saw and more. It
// hides nothing, so it earns no notice and keeps its auto-approval.
func TestRetryReadingTheWorkingTreeIsQuiet(t *testing.T) {
	h := newHarness(t, findingReply(oneFinding), findingReply("NO FINDINGS"))
	h.withHistory()
	if d := h.handle(); d.Kind != Deny {
		t.Fatalf("first firing Kind = %v, want Deny", d.Kind)
	}
	writeFileAt(t, h.repo, "a.py", lines(160))
	h.spec = DiffSpec{}
	d := h.handle()
	if d.Reason != "" {
		t.Fatalf("Reason = %q, want none", d.Reason)
	}
	if d.Kind != Allow {
		t.Fatalf("Kind = %v, want Allow", d.Kind)
	}
}

// The denied bundle never ran its own staging step, so a bare retry finds nothing
// staged at all. That empty index IS the drift, and a silent gate hides it in the one
// case that most needs saying.
func TestBareRetryOnAnEmptyIndexIsNotSilent(t *testing.T) {
	h := newHarness(t, findingReply(oneFinding), findingReply("NO FINDINGS"))
	h.withHistory()
	gitCmd(t, h.repo, "reset", "-q")
	h.spec = DiffSpec{}
	if d := h.handle(); d.Kind != Deny {
		t.Fatalf("first firing Kind = %v, want Deny", d.Kind)
	}
	h.spec = DiffSpec{StagedOnly: true}
	d := h.handle()
	if d.Kind == Silent {
		t.Fatal("Kind = Silent — the empty index is exactly what the agent must be told about")
	}
	if !strings.Contains(d.Reason, "REVIEWS THE INDEX") {
		t.Fatalf("Reason did not carry the stale-index notice: %q", d.Reason)
	}
}

// A retry reading the index while edits sit unstaged carries the notice on every path
// out, the byte-identical chain end included — and that end must not write a cleared
// record, which would let the same stale diff skip the gate outright next time.
func TestTheChainEndOnAStaleIndexWarnsAndClearsNothing(t *testing.T) {
	h := newHarness(t, findingReply(oneFinding))
	h.withHistory()
	h.spec = DiffSpec{}
	if d := h.handle(); d.Kind != Deny {
		t.Fatalf("first firing Kind = %v, want Deny", d.Kind)
	}
	// The fix lands in the working tree and is never staged, so the index still holds
	// exactly the diff round 1 was denied on.
	writeFileAt(t, h.repo, "a.py", lines(160))
	h.spec = DiffSpec{StagedOnly: true}
	d := h.handle()
	if !strings.Contains(d.Reason, "REVIEWS THE INDEX") {
		t.Fatalf("the chain end lost the notice: %q", d.Reason)
	}
	if d.Kind == Allow {
		t.Error("Kind = Allow — a commit reviewed on a possibly stale index must not " +
			"also skip the permission prompt")
	}
	chain := NewChain(h.cfg, ChainKey{Actor: "session-1", Repo: h.repo})
	if record := chain.LoadCleared(stagedDiff(t, h.cfg, h.repo)); record != nil {
		t.Error("a cleared record was written for a diff the agent was warned about")
	}
}

// Nothing is reviewed on an empty-index pass, so the round awaiting an answer is still
// awaiting one. Without this the agent follows the notice into a brand-new chain, and
// the firing count and the cap start over.
func TestTheEmptyIndexNoticeKeepsTheChain(t *testing.T) {
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
	state := chain.Load()
	if !chain.Pending(state) {
		t.Fatal("the chain was dropped — the next commit starts over at firing 1")
	}
	if state.Firing != 1 {
		t.Errorf("Firing = %d, want 1 — the pass reviewed nothing and must not count",
			state.Firing)
	}
}

// The clean-review path under a stale index, which the chain-end test cannot reach: the
// index has to differ from the denied round for a review to happen at all. The notice
// rides the clean pass, and that pass writes no cleared record.
func TestACleanReviewOnAStaleIndexWarnsAndClearsNothing(t *testing.T) {
	h := newHarness(t, findingReply(oneFinding), findingReply("NO FINDINGS"))
	h.withHistory()
	h.spec = DiffSpec{}
	if d := h.handle(); d.Kind != Deny {
		t.Fatalf("first firing Kind = %v, want Deny", d.Kind)
	}
	// Part of the work is staged, so the index moved and the round reviews something.
	// The rest stays unstaged, which is the miss the notice names.
	writeFileAt(t, h.repo, "a.py", lines(240))
	gitCmd(t, h.repo, "add", "-A")
	writeFileAt(t, h.repo, "a.py", lines(260))
	h.spec = DiffSpec{StagedOnly: true}
	d := h.handle()
	if !strings.Contains(d.Reason, "REVIEWS THE INDEX") {
		t.Fatalf("the clean pass lost the notice: %q", d.Reason)
	}
	if d.Kind == Allow {
		t.Error("Kind = Allow — a clean review of a possibly stale index must not also " +
			"skip the permission prompt")
	}
	if row := h.lastRow(); row["phase"] != "clean" {
		t.Fatalf("phase = %v, want clean — the index moved, so a review had to run",
			row["phase"])
	}
	chain := NewChain(h.cfg, ChainKey{Actor: "session-1", Repo: h.repo})
	if record := chain.LoadCleared(stagedDiff(t, h.cfg, h.repo)); record != nil {
		t.Error("a cleared record was written for a diff the agent was warned about")
	}
}

// The capped final pass is the last place the notice can reach the agent, and the last
// place a stale diff can be blessed for reuse.
func TestTheCappedPassOnAStaleIndexWarnsAndClearsNothing(t *testing.T) {
	h := newHarness(t, findingReply(oneFinding))
	h.withHistory()
	h.cfg.MaxFirings = 1
	h.spec = DiffSpec{}
	if d := h.handle(); d.Kind != Deny {
		t.Fatalf("first firing Kind = %v, want Deny", d.Kind)
	}
	h.reviewer.replies = []RawRun{findingReply("NO FINDINGS")}
	h.reviewer.calls = 0
	writeFileAt(t, h.repo, "a.py", lines(240))
	gitCmd(t, h.repo, "add", "-A")
	writeFileAt(t, h.repo, "a.py", lines(260))
	h.spec = DiffSpec{StagedOnly: true}
	d := h.handle()
	if !strings.Contains(d.Reason, "REVIEWS THE INDEX") {
		t.Fatalf("the capped pass lost the notice: %q", d.Reason)
	}
	if row := h.lastRow(); row["phase"] != "cap" {
		t.Fatalf("phase = %v, want cap", row["phase"])
	}
	chain := NewChain(h.cfg, ChainKey{Actor: "session-1", Repo: h.repo})
	if record := chain.LoadCleared(stagedDiff(t, h.cfg, h.repo)); record != nil {
		t.Error("a cleared record was written for a diff the agent was warned about")
	}
}
