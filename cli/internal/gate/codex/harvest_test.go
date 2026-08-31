package codex

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/steer-sh/steer-cli/internal/gate"
)

const ledgerText = "**" + gate.LedgerHeader + " — chain ledger, add charge helper**\\n\\n" +
	"**Round 1/4** — 3 reviewers.\\n\\n- **ACCEPTED** — a concern → a fix\\n"

func metaLine(sessionID, id string, forkAt int) string {
	return fmt.Sprintf(`{"timestamp":"2026-08-31T02:00:00.000Z","ordinal":0,"type":"session_meta",`+
		`"payload":{"session_id":%q,"id":%q,"subagent_history_start_ordinal":%d,"cwd":"/repo"}}`,
		sessionID, id, forkAt)
}

func assistantLine(ordinal int, ts, text string) string {
	return fmt.Sprintf(`{"timestamp":%q,"ordinal":%d,"type":"response_item",`+
		`"payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"%s"}]}}`,
		ts, ordinal, text)
}

func writeSessions(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "2026", "08", "31")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestHarvestKeysARootRolloutOnItsSession(t *testing.T) {
	root := writeSessions(t, map[string]string{
		"rollout-root.jsonl": metaLine("session-1", "session-1", 0) + "\n" +
			assistantLine(4, "2026-08-31T02:05:00.000Z", ledgerText) + "\n",
	})
	msgs := Harvest(root)()
	if len(msgs) != 1 {
		t.Fatalf("harvested %d ledgers, want 1: %#v", len(msgs), msgs)
	}
	if msgs[0].Key != "session-1" || msgs[0].When == 0 {
		t.Fatalf("ledger = %#v", msgs[0])
	}
	if !gate.LedgerRounds(msgs[0].Text)[1] {
		t.Fatalf("round 1 not read from %q", msgs[0].Text)
	}
}

func TestHarvestKeysASubagentRolloutOnItsThread(t *testing.T) {
	root := writeSessions(t, map[string]string{
		"rollout-agent.jsonl": metaLine("session-1", "agent-9", 3) + "\n" +
			assistantLine(5, "2026-08-31T02:06:00.000Z", ledgerText) + "\n",
	})
	msgs := Harvest(root)()
	if len(msgs) != 1 || msgs[0].Key != "agent-9" {
		t.Fatalf("ledgers = %#v, want one keyed on the thread", msgs)
	}
}

func TestHarvestDropsAReplayedParentLedger(t *testing.T) {
	// The parent's own ledger is replayed verbatim into the child below the fork point.
	// Harvesting it there would offer the parent's ledger under the thread's key, and
	// the parent's own rollout already carries it.
	root := writeSessions(t, map[string]string{
		"rollout-agent.jsonl": metaLine("session-1", "agent-9", 4) + "\n" +
			assistantLine(2, "2026-08-31T02:01:00.000Z", ledgerText) + "\n" +
			assistantLine(6, "2026-08-31T02:07:00.000Z", ledgerText) + "\n",
	})
	msgs := Harvest(root)()
	if len(msgs) != 1 {
		t.Fatalf("harvested %d ledgers, want the thread's own only", len(msgs))
	}
}

func TestHarvestIgnoresNonLedgerText(t *testing.T) {
	root := writeSessions(t, map[string]string{
		// A message naming the header without verdict bullets is prose about the gate.
		"rollout-a.jsonl": metaLine("session-1", "session-1", 0) + "\n" +
			assistantLine(2, "2026-08-31T02:02:00.000Z",
				"I read how the "+gate.LedgerHeader+" report works.") + "\n",
		// A user message carrying a real ledger is not a report to the operator.
		"rollout-b.jsonl": metaLine("session-2", "session-2", 0) + "\n" +
			`{"timestamp":"2026-08-31T02:03:00.000Z","ordinal":2,"type":"response_item",` +
			`"payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"` +
			ledgerText + `"}]}}` + "\n",
	})
	if msgs := Harvest(root)(); len(msgs) != 0 {
		t.Fatalf("harvested %#v, want nothing", msgs)
	}
}

func TestHarvestOfAMissingTreeIsEmpty(t *testing.T) {
	if msgs := Harvest(filepath.Join(t.TempDir(), "gone"))(); len(msgs) != 0 {
		t.Fatalf("harvest = %#v", msgs)
	}
}
