package firingaudit

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/adjudication"
	"github.com/steer-sh/steer-cli/internal/transcript"
)

// installed answers with the implementation the fixtures below record.
func installed(Row) adjudication.Identity {
	return adjudication.Identity{HookDigest: "hook1", RuleHash: "rule1"}
}

// transcriptFor writes a Claude Code transcript holding one blocked call, so a
// row built against it reaches a continuation.
func transcriptFor(t *testing.T, callID string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	lines := fmt.Sprintf(
		`{"uuid":"call","parentUuid":"","type":"assistant","message":{"content":[{"type":"tool_use","id":%q,"name":"Bash","input":{"command":"python x.py"}}]}}`+"\n"+
			`{"uuid":"denial","parentUuid":"call","type":"user","message":{"content":[{"type":"tool_result","tool_use_id":%q,"is_error":true,"content":"Blocked by hook"}]}}`+"\n"+
			`{"uuid":"next","parentUuid":"denial","type":"assistant","message":{"content":[{"type":"text","text":"I will use python3."}]}}`+"\n",
		callID, callID)
	if err := os.WriteFile(path, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func ccRow(t *testing.T, id, timestamp string) Row {
	t.Helper()
	call := "toolu_" + id
	return Row{
		FiringID:   id,
		HookName:   "guard",
		Harness:    "claude-code",
		Timestamp:  timestamp,
		HookDigest: "hook1",
		RuleHash:   "rule1",
		Locators: map[string]string{
			"tool_use_id":     call,
			"transcript_path": transcriptFor(t, call),
		},
	}
}

// codexRollout writes a Codex rollout holding one turn, so a Codex row built
// against it reaches that turn. The blocked call itself has no entry here,
// because Codex records none for a call it never ran.
func codexRollout(t *testing.T, turnID string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	lines := fmt.Sprintf(
		`{"timestamp":"2026-09-20T01:00:00Z","ordinal":0,"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"run python x.py"}],"internal_chat_message_metadata_passthrough":{"turn_id":%q,"content_item_kinds":["user.text"]}}}`+"\n"+
			`{"timestamp":"2026-09-20T01:00:02Z","ordinal":1,"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"I will use python3."}],"internal_chat_message_metadata_passthrough":{"turn_id":%q}}}`+"\n",
		turnID, turnID)
	if err := os.WriteFile(path, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// codexRow is a Codex firing, whose locators reach a turn and never a call.
func codexRow(t *testing.T, id, timestamp string) Row {
	t.Helper()
	turn := "turn_" + id
	return Row{
		FiringID:   id,
		HookName:   "guard",
		Harness:    "codex",
		Timestamp:  timestamp,
		HookDigest: "hook1",
		RuleHash:   "rule1",
		Locators: map[string]string{
			"tool_use_id":     "exec-" + id,
			"turn_id":         turn,
			"transcript_path": codexRollout(t, turn),
		},
	}
}

func ids(items []Item) []string {
	var out []string
	for _, i := range items {
		out = append(out, i.ID())
	}
	return out
}

func TestQueueOffersUnreadRowsOfTheInstalledGuardOldestFirst(t *testing.T) {
	newest := ccRow(t, "c", "2026-09-20T03:00:00Z")
	oldest := ccRow(t, "a", "2026-09-20T01:00:00Z")
	middle := ccRow(t, "b", "2026-09-20T02:00:00Z")
	// A row an earlier implementation recorded, and a row too old to carry an
	// identifier at all.
	historic := ccRow(t, "old", "2026-09-20T00:30:00Z")
	historic.HookDigest = "hook0"
	legacy := ccRow(t, "", "2026-09-20T00:00:00Z")

	rows := []Row{newest, oldest, middle, historic, legacy}

	items, _ := Queue(rows, nil, nil, QueueOptions{Installed: installed})
	if got := ids(items); fmt.Sprint(got) != fmt.Sprint([]string{"a", "b", "c"}) {
		t.Errorf("queue = %v; want the installed guard's rows oldest first", got)
	}
	for _, item := range items {
		if item.Reason != Unjudged {
			t.Errorf("%s queued for %q; want %q", item.FiringID, item.Reason, Unjudged)
		}
	}

	widened, _ := Queue(rows, nil, nil, QueueOptions{Installed: installed, Other: true})
	if got := ids(widened); fmt.Sprint(got) != fmt.Sprint([]string{"old", "a", "b", "c"}) {
		t.Errorf("widened queue = %v; want the historic row too, never the legacy one", got)
	}

	limited, _ := Queue(rows, nil, nil, QueueOptions{Installed: installed, Limit: 2})
	if got := ids(limited); fmt.Sprint(got) != fmt.Sprint([]string{"a", "b"}) {
		t.Errorf("limited queue = %v; want the two oldest", got)
	}
}

func TestQueueKeepsAJudgedRowWhoseContinuationNobodyRead(t *testing.T) {
	row := ccRow(t, "a", "2026-09-20T01:00:00Z")
	read := ccRow(t, "b", "2026-09-20T02:00:00Z")

	receipts := []adjudication.Receipt{
		{Subject: adjudication.SubjectGuard, FiringID: "a", Judgment: adjudication.Prevented},
		{Subject: adjudication.SubjectGuard, FiringID: "b", Judgment: adjudication.Prevented, ContinuationInspected: true},
	}

	items, _ := Queue([]Row{row, read}, nil, receipts, QueueOptions{Installed: installed})
	if len(items) != 1 || items[0].FiringID != "a" {
		t.Fatalf("queue = %v; want only the judgment with an unread continuation", ids(items))
	}
	if items[0].Reason != ContinuationUnread {
		t.Errorf("reason = %q; want %q", items[0].Reason, ContinuationUnread)
	}
}

func TestQueueDoesNotReofferEvidenceARereadCannotChange(t *testing.T) {
	// An unresolved judgment is still a judgment. It returns only on new
	// evidence, so a read continuation leaves it out whatever the value.
	unresolved := ccRow(t, "a", "2026-09-20T01:00:00Z")
	// A judgment whose transcript has since gone carries no evidence to add.
	gone := ccRow(t, "b", "2026-09-20T02:00:00Z")
	gone.Locators["transcript_path"] = filepath.Join(t.TempDir(), "absent.jsonl")

	receipts := []adjudication.Receipt{
		{Subject: adjudication.SubjectGuard, FiringID: "a", Judgment: adjudication.Unresolved, ContinuationInspected: true},
		{Subject: adjudication.SubjectGuard, FiringID: "b", Judgment: adjudication.Prevented},
	}

	items, _ := Queue([]Row{unresolved, gone}, nil, receipts, QueueOptions{Installed: installed})
	if len(items) != 0 {
		t.Errorf("queue = %v; want neither offered", ids(items))
	}
}

func TestQueueNarrowsToOneGuard(t *testing.T) {
	mine := ccRow(t, "a", "2026-09-20T01:00:00Z")
	other := ccRow(t, "b", "2026-09-20T02:00:00Z")
	other.HookName = "second"

	items, _ := Queue([]Row{mine, other}, nil, nil,
		QueueOptions{Installed: installed, HookName: "second"})
	if len(items) != 1 || items[0].FiringID != "b" {
		t.Errorf("queue = %v; want only the named guard's row", ids(items))
	}
}

func TestCountsDiscloseWhatTheListingLeft(t *testing.T) {
	rows := []Row{
		ccRow(t, "a", "2026-09-20T01:00:00Z"),
		ccRow(t, "b", "2026-09-20T02:00:00Z"),
		ccRow(t, "c", "2026-09-20T03:00:00Z"),
	}
	_, all := Queue(rows, nil, nil, QueueOptions{Installed: installed})
	if all.Queued != all.Listed {
		t.Fatalf("an unlimited listing left %d of %d unlisted", all.Queued-all.Listed, all.Queued)
	}
	if !strings.Contains(all.Remaining(), "All") {
		t.Errorf("a complete listing does not say so: %q", all.Remaining())
	}

	_, cut := Queue(rows, nil, nil, QueueOptions{Installed: installed, Limit: 1})
	if cut.Listed != 1 || cut.Queued != all.Queued {
		t.Fatalf("a limit changed the queued count: listed=%d queued=%d want listed=1 queued=%d",
			cut.Listed, cut.Queued, all.Queued)
	}
	if !strings.Contains(cut.Remaining(), "stay unexamined") {
		t.Errorf("a limited listing does not disclose what it left: %q", cut.Remaining())
	}
}

// A check run's identifier can read the same as a firing's. A check receipt is
// no judgment of a firing, so the guard queue and the prior readings ignore it.
func TestAGuardReaderIgnoresACheckReceiptOfTheSameIdentifier(t *testing.T) {
	row := ccRow(t, "a", "2026-09-20T01:00:00Z")
	receipts := []adjudication.Receipt{{Subject: adjudication.SubjectCheck, RunID: "a",
		Failures: []adjudication.Failure{{Kind: adjudication.FailureOracle,
			Validity: adjudication.Valid, Attribution: adjudication.Introduced}}}}

	items, _ := Queue([]Row{row}, nil, receipts, QueueOptions{Installed: installed})
	if len(items) != 1 || items[0].Reason != Unjudged {
		t.Errorf("queue = %+v; want the firing offered as unread", items)
	}
	c, err := Show([]Row{row}, "a", receipts, transcript.ContinuationBudget{})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Prior) != 0 {
		t.Errorf("prior = %+v; want no guard reading", c.Prior)
	}
}
