package firingaudit

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/steer-sh/steer-cli/internal/adjudication"
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

func ids(items []Item) []string {
	var out []string
	for _, i := range items {
		out = append(out, i.FiringID)
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

	items := Queue(rows, nil, QueueOptions{Installed: installed})
	if got := ids(items); fmt.Sprint(got) != fmt.Sprint([]string{"a", "b", "c"}) {
		t.Errorf("queue = %v; want the installed guard's rows oldest first", got)
	}
	for _, item := range items {
		if item.Reason != Unjudged {
			t.Errorf("%s queued for %q; want %q", item.FiringID, item.Reason, Unjudged)
		}
	}

	widened := Queue(rows, nil, QueueOptions{Installed: installed, Other: true})
	if got := ids(widened); fmt.Sprint(got) != fmt.Sprint([]string{"old", "a", "b", "c"}) {
		t.Errorf("widened queue = %v; want the historic row too, never the legacy one", got)
	}

	limited := Queue(rows, nil, QueueOptions{Installed: installed, Limit: 2})
	if got := ids(limited); fmt.Sprint(got) != fmt.Sprint([]string{"a", "b"}) {
		t.Errorf("limited queue = %v; want the two oldest", got)
	}
}

func TestQueueKeepsAJudgedRowWhoseContinuationNobodyRead(t *testing.T) {
	row := ccRow(t, "a", "2026-09-20T01:00:00Z")
	read := ccRow(t, "b", "2026-09-20T02:00:00Z")

	receipts := []adjudication.Receipt{
		{FiringID: "a", Judgment: adjudication.Prevented},
		{FiringID: "b", Judgment: adjudication.Prevented, ContinuationInspected: true},
	}

	items := Queue([]Row{row, read}, receipts, QueueOptions{Installed: installed})
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
		{FiringID: "a", Judgment: adjudication.Unresolved, ContinuationInspected: true},
		{FiringID: "b", Judgment: adjudication.Prevented},
	}

	items := Queue([]Row{unresolved, gone}, receipts, QueueOptions{Installed: installed})
	if len(items) != 0 {
		t.Errorf("queue = %v; want neither offered", ids(items))
	}
}

func TestQueueNarrowsToOneGuard(t *testing.T) {
	mine := ccRow(t, "a", "2026-09-20T01:00:00Z")
	other := ccRow(t, "b", "2026-09-20T02:00:00Z")
	other.HookName = "second"

	items := Queue([]Row{mine, other}, nil,
		QueueOptions{Installed: installed, HookName: "second"})
	if len(items) != 1 || items[0].FiringID != "b" {
		t.Errorf("queue = %v; want only the named guard's row", ids(items))
	}
}
