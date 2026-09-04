package pi

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/gate"
)

func TestHarvestReadsAssistantLedgerUnderTheHeaderSession(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "--repo--")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	ledger := "**" + gate.LedgerHeader + " — chain ledger**\n\n" +
		"**Round 1/4**\n\n- **ACCEPTED** — concern → fix\n"
	content, _ := json.Marshal([]map[string]any{{"type": "text", "text": ledger}})
	body := `{"type":"session","id":"pi-session","cwd":"/repo"}` + "\n" +
		`{"type":"message","id":"e1","timestamp":"2026-09-03T12:00:00Z",` +
		`"message":{"role":"assistant","content":` + string(content) + `}}` + "\n"
	path := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	msgs := Harvest(root)()
	if len(msgs) != 1 || msgs[0].Key != "pi-session" || msgs[0].Path != path || msgs[0].When == 0 {
		t.Fatalf("harvest = %#v", msgs)
	}
	if !gate.LedgerRounds(msgs[0].Text)[1] {
		t.Fatalf("ledger round not read from %q", msgs[0].Text)
	}
}

func TestHarvestIgnoresUserLedgerAndOrdinaryAssistantText(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "session.jsonl")
	body := `{"type":"session","id":"pi-session"}` + "\n" +
		`{"type":"message","timestamp":"2026-09-03T12:00:00Z","message":{"role":"user",` +
		`"content":"` + gate.LedgerHeader + ` - **ACCEPTED**"}}` + "\n" +
		`{"type":"message","timestamp":"2026-09-03T12:01:00Z","message":{"role":"assistant",` +
		`"content":"ordinary text"}}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if msgs := Harvest(root)(); len(msgs) != 0 {
		t.Fatalf("harvest = %#v", msgs)
	}
}

// ledgerEntry renders one assistant ledger entry of the session tree.
func ledgerEntry(id, parent, stamp, subject string) string {
	ledger := "**" + gate.LedgerHeader + " — chain ledger, " + subject + "**\n\n" +
		"**Round 1/4**\n\n- **ACCEPTED** — concern → fix\n"
	content, _ := json.Marshal([]map[string]any{{"type": "text", "text": ledger}})
	row, _ := json.Marshal(map[string]any{
		"type": "message", "id": id, "parentId": parent, "timestamp": stamp,
		"message": json.RawMessage(`{"role":"assistant","content":` + string(content) + `}`),
	})
	return string(row) + "\n"
}

func TestHarvestSkipsALedgerOnAnAbandonedBranch(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "session.jsonl")
	body := `{"type":"session","id":"pi-session"}` + "\n" +
		`{"type":"message","id":"e1","parentId":"","timestamp":"2026-09-03T12:00:00Z",` +
		`"message":{"role":"user","content":"commit it"}}` + "\n" +
		ledgerEntry("e2", "e1", "2026-09-03T12:01:00Z", "the undone round") +
		ledgerEntry("e3", "e1", "2026-09-03T12:02:00Z", "the live round")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	msgs := Harvest(root)()
	if len(msgs) != 1 || !strings.Contains(msgs[0].Text, "the live round") {
		t.Fatalf("harvest = %#v", msgs)
	}
}

func TestHarvestCountsAForkedLedgerOnItsOwnSessionOnly(t *testing.T) {
	root := t.TempDir()
	parentPath := filepath.Join(root, "parent.jsonl")
	parent := `{"type":"session","id":"parent-session"}` + "\n" +
		ledgerEntry("p1", "", "2026-09-03T12:00:00Z", "the parent round")
	if err := os.WriteFile(parentPath, []byte(parent), 0o644); err != nil {
		t.Fatal(err)
	}
	forkHeader, _ := json.Marshal(map[string]any{
		"type": "session", "id": "fork-session", "parentSession": parentPath,
	})
	fork := string(forkHeader) + "\n" +
		ledgerEntry("p1", "", "2026-09-03T12:00:00Z", "the parent round") +
		ledgerEntry("f1", "p1", "2026-09-03T12:03:00Z", "the fork round")
	if err := os.WriteFile(filepath.Join(root, "fork.jsonl"), []byte(fork), 0o644); err != nil {
		t.Fatal(err)
	}

	msgs := Harvest(root)()
	if len(msgs) != 2 {
		t.Fatalf("harvest = %#v", msgs)
	}
	keys := map[string]string{}
	for _, m := range msgs {
		for _, subject := range []string{"the parent round", "the fork round"} {
			if strings.Contains(m.Text, subject) {
				keys[subject] = m.Key
			}
		}
	}
	if keys["the parent round"] != "parent-session" || keys["the fork round"] != "fork-session" {
		t.Fatalf("keys = %#v", keys)
	}
}
