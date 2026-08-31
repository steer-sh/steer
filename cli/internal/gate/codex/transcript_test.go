package codex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/gate"
)

// rollout is a two-turn root rollout in Codex's own line shapes.
const rollout = `{"timestamp":"2026-08-31T02:54:18.942Z","ordinal":0,"type":"session_meta","payload":{"session_id":"session-1","id":"session-1","cwd":"/repo"}}
{"timestamp":"2026-08-31T02:54:19.000Z","ordinal":1,"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"first task"}],"internal_chat_message_metadata_passthrough":{"content_item_kinds":["user.text"]}}}
{"timestamp":"2026-08-31T02:54:20.000Z","ordinal":2,"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"working"}]}}
{"timestamp":"2026-08-31T02:54:21.000Z","ordinal":3,"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"second task"}],"internal_chat_message_metadata_passthrough":{"content_item_kinds":["user.text"]}}}
`

func writeRollout(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func testConfig(t *testing.T) gate.Config {
	t.Helper()
	cfg := gate.Load()
	cfg.StateDir = t.TempDir()
	return cfg
}

func TestAuthorFieldsReadsTaskAndModel(t *testing.T) {
	cfg := testConfig(t)
	path := writeRollout(t, rollout)
	fields := AuthorFields(gate.NewLedger(cfg), cfg, path, "session-1", "gpt-5.6-sol")

	if fields["author_model"] != "gpt-5.6-sol" || fields["transcript"] != path {
		t.Fatalf("fields = %#v", fields)
	}
	if fields["task"] != "second task" || fields["task_turns"] != 2 {
		t.Fatalf("task fields = %#v", fields)
	}
	if fields["prefix_sha"] == nil || fields["task_sha"] == nil {
		t.Fatalf("sidecars not written: %#v", fields)
	}
}

func TestAuthorFieldsCapsTheTask(t *testing.T) {
	cfg := testConfig(t)
	cfg.MaxTaskChars = 4
	fields := AuthorFields(gate.NewLedger(cfg), cfg, writeRollout(t, rollout), "session-1", "")
	if fields["task"] != "seco" {
		t.Fatalf("task = %v", fields["task"])
	}
	if _, ok := fields["author_model"]; ok {
		t.Fatalf("empty model was stamped: %#v", fields)
	}
}

func TestAuthorFieldsSurvivesAMissingRollout(t *testing.T) {
	cfg := testConfig(t)
	fields := AuthorFields(gate.NewLedger(cfg), cfg, filepath.Join(t.TempDir(), "gone.jsonl"), "s", "m")
	if fields["task"] != nil || fields["author_model"] != "m" {
		t.Fatalf("fields = %#v", fields)
	}
}

func TestAuthorFieldsDropsAReplayedParentHistory(t *testing.T) {
	// A subagent rollout replays its parent's history verbatim below the fork ordinal.
	// Counting those turns here would file the parent's task onto the thread's row.
	body := strings.Replace(rollout,
		`"payload":{"session_id":"session-1","id":"session-1","cwd":"/repo"}`,
		`"payload":{"session_id":"session-1","id":"agent-9","cwd":"/repo","subagent_history_start_ordinal":3}`, 1)
	cfg := testConfig(t)
	fields := AuthorFields(gate.NewLedger(cfg), cfg, writeRollout(t, body), "agent-9", "")
	if fields["task"] != "second task" || fields["task_turns"] != 1 {
		t.Fatalf("fields = %#v", fields)
	}
}
