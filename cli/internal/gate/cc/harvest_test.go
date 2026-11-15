package cc

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/steer-sh/steer-cli/internal/gate"
	"github.com/steer-sh/steer-cli/internal/gate/resolve"
)

const ledgerText = "**" + gate.LedgerHeader + " — review sequence #ab12, add mod**\n\n" +
	"**Round 1, blocking** — 3 reviewers.\n\n- **REJECTED** — a concern → the premise is false\n"

// transcriptLine is one transcript row as JSON.
func transcriptLine(t *testing.T, row map[string]any) string {
	t.Helper()
	data, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func assistantBlocks(blocks ...map[string]any) map[string]any {
	content := make([]any, len(blocks))
	for i, b := range blocks {
		content[i] = b
	}
	return map[string]any{"type": "assistant", "sessionId": "s1", "timestamp": "2026-10-09T10:00:00.000Z",
		"message": map[string]any{"role": "assistant", "content": content}}
}

func TestReadLedgersTakesMainThreadReplyTextOnly(t *testing.T) {
	sidechain := assistantBlocks(map[string]any{"type": "text", "text": ledgerText})
	sidechain["isSidechain"] = true
	rootWithAgent := assistantBlocks(map[string]any{"type": "text", "text": ledgerText})
	rootWithAgent["agentId"] = "root-agent"
	user := map[string]any{"type": "user", "sessionId": "s1", "timestamp": "2026-10-09T10:00:00.000Z",
		"message": map[string]any{"role": "user", "content": ledgerText}}
	lines := []string{
		transcriptLine(t, assistantBlocks(map[string]any{"type": "thinking", "thinking": ledgerText})),
		transcriptLine(t, sidechain),
		transcriptLine(t, user),
		transcriptLine(t, rootWithAgent),
		transcriptLine(t, assistantBlocks(map[string]any{"type": "text", "text": ledgerText})),
	}
	path := filepath.Join(t.TempDir(), "s1.jsonl")
	body := ""
	for _, l := range lines {
		body += l + "\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	msgs, err := ReadLedgers(path)
	if err != nil || len(msgs) != 2 || msgs[0].Key != "s1" || msgs[1].Key != "s1" {
		t.Fatalf("ledgers = %+v, err %v; want both root text blocks", msgs, err)
	}
}

func TestReadLedgersReportsAnUnreadableLedgerLine(t *testing.T) {
	whole := transcriptLine(t, assistantBlocks(map[string]any{"type": "text", "text": ledgerText}))
	path := filepath.Join(t.TempDir(), "s1.jsonl")
	if err := os.WriteFile(path, []byte(whole+"\n"+whole[:len(whole)-15]), 0o644); err != nil {
		t.Fatal(err)
	}
	msgs, err := ReadLedgers(path)
	if err != ErrUnreadableLine || len(msgs) != 1 {
		t.Errorf("ledgers = %d, err %v; want the whole ledger and the truncation error", len(msgs), err)
	}
	if _, err := ReadLedgers(filepath.Join(t.TempDir(), "missing.jsonl")); err == nil {
		t.Error("a missing transcript read without error")
	}
}

func writeLedgerTranscript(t *testing.T, path string, rows ...map[string]any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	var body strings.Builder
	for _, row := range rows {
		body.WriteString(transcriptLine(t, row) + "\n")
	}
	if err := os.WriteFile(path, []byte(body.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func childLedgerRow(agent string) map[string]any {
	row := assistantBlocks(map[string]any{"type": "text", "text": ledgerText})
	row["isSidechain"] = true
	row["agentId"] = agent
	return row
}

func TestHarvestKeepsRootAndChildLedgersInTheirOwnThreads(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "project", "s1.jsonl")
	child := filepath.Join(root, "project", "s1", "subagents", "agent-child1.jsonl")
	writeLedgerTranscript(t, parent,
		assistantBlocks(map[string]any{"type": "text", "text": ledgerText}),
		childLedgerRow("child1"))
	missingID := childLedgerRow("")
	thinking := childLedgerRow("child1")
	thinking["message"] = map[string]any{"content": []any{map[string]any{"type": "thinking", "thinking": ledgerText}}}
	notification := childLedgerRow("child1")
	notification["type"] = "user"
	writeLedgerTranscript(t, child,
		assistantBlocks(map[string]any{"type": "text", "text": ledgerText}),
		childLedgerRow("child2"), missingID, thinking, notification, childLedgerRow("child1"))
	// A retained child file remains readable without its parent's root transcript.
	orphan := filepath.Join(root, "project", "s2", "subagents", "agent-child2.jsonl")
	writeLedgerTranscript(t, orphan, childLedgerRow("child2"))
	writeLedgerTranscript(t, filepath.Join(root, "project", "s1", "subagents", "agent-.jsonl"), missingID)
	msgs := Harvest(root)()
	if len(msgs) != 3 {
		t.Fatalf("harvest = %+v, want one root and two child ledgers", msgs)
	}
	want := map[string]string{"s1": parent, "child1": child, "child2": orphan}
	for _, msg := range msgs {
		if path, ok := want[msg.Key]; !ok || msg.Path != path || msg.Text != ledgerText {
			t.Fatalf("unexpected ledger: %+v", msg)
		}
		delete(want, msg.Key)
	}
	if msgs, err := ReadLedgers(child); err != nil || len(msgs) != 1 || msgs[0].Key != "s1" {
		t.Fatalf("root reader changed its stream selection: %+v, %v", msgs, err)
	}
}

func TestChildHarvestKeepsWholeLedgersAroundATornLine(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "project", "s1", "subagents", "agent-child1.jsonl")
	writeLedgerTranscript(t, path, childLedgerRow("child1"))
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString(`{"type":"assistant","message":"Commit gate fired`)
	if closeErr := f.Close(); err != nil || closeErr != nil {
		t.Fatalf("append: %v, close: %v", err, closeErr)
	}
	msgs, err := readLedgers(path, "child1")
	if err != ErrUnreadableLine || len(msgs) != 1 || len(Harvest(root)()) != 1 {
		t.Fatalf("child ledger lost around a torn line: %+v, %v", msgs, err)
	}
}

func TestHarvestRecoversChildMissWithoutClaimingParentOrSiblingRows(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "project", "s1", "subagents", "agent-child1.jsonl")
	writeLedgerTranscript(t, child, childLedgerRow("child1"))
	cfg := gate.Config{StateDir: t.TempDir(), HostID: "test", StaleAfter: 30 * time.Minute}
	stamp, _ := time.Parse(time.RFC3339, "2026-10-09T10:00:00Z")
	var rows []map[string]any
	for _, thread := range []string{"", "child1", "child2"} {
		rows = append(rows, map[string]any{
			"phase": "retry", "ts": stamp.Unix() - 10, "deny_ts": stamp.Unix() - 20,
			"firing": 1, "chain_id": "ab12", "subject": "add mod", "session_id": "s1",
			"thread_id": thread, "ledger_expected": true, "ledger_found": false,
			"backfilled": true, "ledger_join": "chain", "ledger_miss": "no-ledger",
		})
	}
	writeLedgerTranscript(t, cfg.LogFile(), rows...)
	harvests := []resolve.Harvest{Harvest(root)}
	now := func() float64 { return float64(stamp.Unix() + 86400) }
	preview := resolve.Backfill(cfg, harvests, resolve.BackfillOptions{Reresolve: true}, io.Discard, now)
	if preview.Err != nil || preview.Changed != 1 {
		t.Fatalf("preview = %+v, want one recoverable child row", preview)
	}
	opts := resolve.BackfillOptions{Apply: true, Reresolve: true}
	result := resolve.Backfill(cfg, harvests, opts, io.Discard, now)
	if result.Err != nil || result.Changed != 1 {
		t.Fatalf("backfill = %+v", result)
	}
	raw, err := os.ReadFile(cfg.LogFile())
	if err != nil {
		t.Fatal(err)
	}
	for i, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var row map[string]any
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatal(err)
		}
		if i == 1 {
			if row["ledger_found"] != true || row["self_reported_rejected"] != float64(1) ||
				row["ledger_ts"] != float64(stamp.Unix()) || row["session_id"] != "s1" || row["thread_id"] != "child1" {
				t.Fatalf("child adjudication not recovered with original provenance: %v", row)
			}
		} else if row["ledger_found"] != false {
			t.Fatalf("child adjudication claimed another thread: %v", row)
		}
	}
	if again := resolve.Backfill(cfg, harvests, opts, io.Discard, now); again.Err != nil || again.Changed != 0 {
		t.Fatalf("repeated backfill = %+v, want no changes", again)
	}
}
