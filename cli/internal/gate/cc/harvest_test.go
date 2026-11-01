package cc

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/steer-sh/steer-cli/internal/gate"
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
	user := map[string]any{"type": "user", "sessionId": "s1", "timestamp": "2026-10-09T10:00:00.000Z",
		"message": map[string]any{"role": "user", "content": ledgerText}}
	lines := []string{
		transcriptLine(t, assistantBlocks(map[string]any{"type": "thinking", "thinking": ledgerText})),
		transcriptLine(t, sidechain),
		transcriptLine(t, user),
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
	if err != nil || len(msgs) != 1 || msgs[0].Key != "s1" {
		t.Fatalf("ledgers = %+v, err %v; want the one text block", msgs, err)
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
