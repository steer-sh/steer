package pi

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/gate"
)

// TestAuthorFieldsReadsTheTaskFromTheActiveBranch reads a session whose abandoned
// branch, Steer's own custom message and a resolved skill's body are no person's
// words: the task is the arguments typed after the last skill command, and the prefix
// is the whole session file.
func TestAuthorFieldsReadsTheTaskFromTheActiveBranch(t *testing.T) {
	body := `{"type":"session","version":3,"id":"pi-session","cwd":"/repo"}
{"type":"message","id":"m1","parentId":null,"timestamp":"2026-04-19T03:17:20.000Z","message":{"role":"user","content":[{"type":"text","text":"first task"}]}}
{"type":"message","id":"m2","parentId":"m1","timestamp":"2026-04-19T03:17:21.000Z","message":{"role":"assistant","content":[],"usage":{"input":10,"cacheRead":20,"cacheWrite":0}}}
{"type":"message","id":"m3","parentId":"m2","timestamp":"2026-04-19T03:17:22.000Z","message":{"role":"user","content":[{"type":"text","text":"abandoned task"}]}}
{"type":"message","id":"m4","parentId":"m2","timestamp":"2026-04-19T03:17:23.000Z","message":{"role":"user","content":[{"type":"text","text":"<skill name=\"steer\" location=\"/p/SKILL.md\">\nReferences are relative to /p.\n\nbody\n</skill>"}]}}
{"type":"custom_message","id":"m5","parentId":"m4","timestamp":"2026-04-19T03:17:24.000Z","customType":"steer-commit-gate","content":"[commit-gate] review passed","display":true}
{"type":"message","id":"m6","parentId":"m5","timestamp":"2026-04-19T03:17:25.000Z","message":{"role":"user","content":[{"type":"text","text":"<skill name=\"steer\" location=\"/p/SKILL.md\">\nReferences are relative to /p.\n\nbody\n</skill>\n\nnever use var"}]}}
{"type":"message","id":"m7","parentId":"m6","timestamp":"2026-04-19T03:17:26.000Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"c1","name":"bash","arguments":{"command":"git commit -m x"}}],"usage":{"input":1,"cacheRead":2,"cacheWrite":3}}}
`
	path := filepath.Join(t.TempDir(), "pi.jsonl")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := gate.Defaults()
	cfg.StateDir = t.TempDir()
	fields := AuthorFields(gate.NewLedger(cfg), cfg, path, "pi-session", "model-pi")

	if fields["task"] != "never use var" || fields["task_turns"] != 2 ||
		fields["author_model"] != "model-pi" || fields["context_tokens"] != 6 {
		t.Fatalf("fields = %#v", fields)
	}
	if fields["prefix_lines"] != strings.Count(body, "\n") || fields["prefix_sha"] == nil {
		t.Fatalf("prefix fields = %#v", fields)
	}
	file, err := os.Open(filepath.Join(cfg.TaskDir(), fields["task_file"].(string)))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var turns []string
	for scanner := bufio.NewScanner(file); scanner.Scan(); {
		turns = append(turns, scanner.Text())
	}
	if len(turns) != 2 || !strings.Contains(turns[0], `"first task"`) ||
		!strings.Contains(turns[1], `"never use var"`) {
		t.Fatalf("task file = %q", turns)
	}
}
