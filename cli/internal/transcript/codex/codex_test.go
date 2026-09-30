package codex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/steer-sh/steer-cli/internal/harness"
	"github.com/steer-sh/steer-cli/internal/transcript"
)

// Every fixture below reproduces a shape read off this machine's rollouts,
// with the content replaced.

const rootRollout = `{"timestamp":"2026-08-29T20:35:20.427Z","ordinal":0,"type":"session_meta","payload":{"session_id":"root-1","id":"root-1","cwd":"/repo/alpha","originator":"codex-tui","thread_source":"user"}}
{"timestamp":"2026-08-29T20:35:20.725Z","ordinal":5,"type":"response_item","payload":{"type":"message","id":"msg_env","role":"user","content":[{"type":"input_text","text":"<environment_context>\n  <cwd>/repo/alpha</cwd>\n</environment_context>"}],"internal_chat_message_metadata_passthrough":{"content_item_kinds":["environments.environment_context"]}}}
{"timestamp":"2026-08-29T20:35:20.742Z","ordinal":8,"type":"response_item","payload":{"type":"message","id":"msg_1","role":"user","content":[{"type":"input_text","text":"stop adding docstrings"}],"internal_chat_message_metadata_passthrough":{"content_item_kinds":["user.text"]}}}
{"timestamp":"2026-08-29T20:35:21.000Z","ordinal":9,"type":"response_item","payload":{"type":"message","id":"msg_2","role":"assistant","content":[{"type":"output_text","text":"ok"}],"internal_chat_message_metadata_passthrough":{"content_item_kinds":["unknown"]}}}
{"timestamp":"2026-08-29T20:35:22.000Z","ordinal":10,"type":"response_item","payload":{"type":"function_call","name":"exec_command","arguments":"{\"cmd\":\"go test ./...\",\"workdir\":\"/repo/alpha\"}","call_id":"call_a"}}
{"timestamp":"2026-08-29T20:35:29.000Z","ordinal":11,"type":"response_item","payload":{"type":"function_call_output","call_id":"call_a","output":"Command: /bin/zsh -lc 'go test ./...'\nProcess exited with code 1\nOutput:\nFAIL: alpha_test.go:14"}}
{"timestamp":"2026-08-29T20:35:30.000Z","ordinal":12,"type":"response_item","payload":{"type":"custom_tool_call","name":"exec","call_id":"call_b","input":"const r = await tools.exec_command({\"cmd\":\"git status --short\",\"workdir\":\"/repo/alpha\"}); text(r.output);\n"}}
{"timestamp":"2026-08-29T20:35:31.000Z","ordinal":13,"type":"response_item","payload":{"type":"custom_tool_call_output","call_id":"call_b","output":[{"type":"input_text","text":"Script completed"},{"type":"input_text","text":"Process exited with code 0"}]}}
{"timestamp":"2026-08-29T20:35:32.000Z","ordinal":14,"type":"response_item","payload":{"type":"custom_tool_call","name":"apply_patch","call_id":"call_c","input":"*** Begin Patch\n*** Update File: alpha/main.go\n@@\n-old\n+new\n*** End Patch"}}
{"timestamp":"2026-08-29T20:35:33.000Z","ordinal":15,"type":"event_msg","payload":{"type":"token_count","info":{}}}
`

// childRollout forks rootRollout at ordinal 12: everything below that ordinal
// is the parent's history replayed verbatim into this file.
const childRollout = `{"timestamp":"2026-08-29T21:06:23.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"root-1","id":"thread-9","parent_thread_id":"root-1","cwd":"/repo/alpha","thread_source":"subagent","subagent_history_start_ordinal":12}}
{"timestamp":"2026-08-29T20:35:20.742Z","ordinal":8,"type":"response_item","payload":{"type":"message","id":"msg_1","role":"user","content":[{"type":"input_text","text":"stop adding docstrings"}],"internal_chat_message_metadata_passthrough":{"content_item_kinds":["user.text"]}}}
{"timestamp":"2026-08-29T21:06:24.000Z","ordinal":12,"type":"response_item","payload":{"type":"message","id":"msg_3","role":"user","content":[{"type":"input_text","text":"why do you keep using sed"}],"internal_chat_message_metadata_passthrough":{"content_item_kinds":["user.text"]}}}
{"timestamp":"2026-08-29T21:06:25.000Z","ordinal":13,"type":"response_item","payload":{"type":"function_call","name":"exec_command","arguments":"{\"cmd\":\"ls\"}","call_id":"call_d"}}
`

// legacyRollout predates content item kinds: its human turns exist only as
// user_message events, and its session id lives in the header's id field.
const legacyRollout = `{"timestamp":"2026-05-08T01:42:17.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":null,"id":"legacy-7","cwd":"/repo/beta"}}
{"timestamp":"2026-05-08T01:42:45.000Z","ordinal":1,"type":"event_msg","payload":{"type":"user_message","message":"don't use bare python"}}
{"timestamp":"2026-05-08T01:42:46.000Z","ordinal":2,"type":"response_item","payload":{"type":"message","id":"m","role":"user","content":[{"type":"input_text","text":"<environment_context>\n</environment_context>"}]}}
{"timestamp":"2026-05-08T01:42:47.000Z","ordinal":3,"type":"response_item","payload":{"type":"message","id":"m2","role":"user","content":[{"type":"input_text","text":"don't use bare python"}]}}
`

func writeRollout(t *testing.T, root, day, name, body string) string {
	t.Helper()
	dir := filepath.Join(root, day)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func collect(t *testing.T, s transcript.Session, f transcript.Filter) ([]transcript.Event, transcript.Counts) {
	t.Helper()
	var events []transcript.Event
	counts, err := New().Stream(s, f, func(e transcript.Event) error {
		events = append(events, e)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return events, counts
}

func TestDiscoverWalksTheDateTreeAndReadsProject(t *testing.T) {
	root := t.TempDir()
	writeRollout(t, root, "2026/08/29", "rollout-alpha.jsonl", rootRollout)
	writeRollout(t, root, "2026/05/08", "rollout-beta.jsonl", legacyRollout)
	writeRollout(t, root, "2026/08/29", "notes.txt", "ignored")

	a := New()
	all, err := a.Discover(root, transcript.Filter{AllProjects: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 rollouts, got %+v", all)
	}
	for _, s := range all {
		if s.Harness != harness.Codex {
			t.Errorf("session harness = %q; want %q", s.Harness, harness.Codex)
		}
	}

	scoped, err := a.Discover(root, transcript.Filter{Projects: []string{"/repo/alpha"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(scoped) != 1 || scoped[0].Project != "/repo/alpha" {
		t.Fatalf("expected only /repo/alpha, got %+v", scoped)
	}
	// The header cwd is the launch directory, which a repository scope resolves.
	if scoped[0].Cwd != "/repo/alpha" {
		t.Errorf("Cwd = %q; want the header cwd", scoped[0].Cwd)
	}

	future, err := a.Discover(root, transcript.Filter{Since: time.Now().Add(time.Hour), AllProjects: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(future) != 0 {
		t.Fatalf("expected the window to exclude every file, got %+v", future)
	}
}

func TestDiscoverOnMissingRootYieldsNothing(t *testing.T) {
	sessions, err := New().Discover(filepath.Join(t.TempDir(), "absent"), transcript.Filter{AllProjects: true})
	if err != nil {
		t.Fatalf("missing root should not error: %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("expected no sessions, got %+v", sessions)
	}
}

func TestStreamNormalizesEachEntryKind(t *testing.T) {
	root := t.TempDir()
	path := writeRollout(t, root, "2026/08/29", "rollout-alpha.jsonl", rootRollout)

	events, counts := collect(t, transcript.Session{Harness: harness.Codex, Path: path}, transcript.Filter{})

	var turns, calls, results []transcript.Event
	for _, e := range events {
		switch e.Kind {
		case transcript.KindHumanTurn:
			turns = append(turns, e)
		case transcript.KindToolCall:
			calls = append(calls, e)
		case transcript.KindToolResult:
			results = append(results, e)
		}
	}

	// The environment_context message wears the user role but nobody typed it.
	if len(turns) != 1 || turns[0].Text != "stop adding docstrings" {
		t.Fatalf("human turns = %+v", turns)
	}
	if turns[0].SessionID != "root-1" || turns[0].ThreadID != "" {
		t.Errorf("root event identity = %q/%q", turns[0].SessionID, turns[0].ThreadID)
	}

	if len(calls) != 3 {
		t.Fatalf("tool calls = %+v", calls)
	}
	want := []struct {
		tool, target string
		shell        bool
	}{
		{"exec_command", "go test ./...", true},
		{"exec", "git status --short", true},
		{"apply_patch", "alpha/main.go", false},
	}
	for i, w := range want {
		if calls[i].Tool != w.tool || calls[i].Target != w.target || calls[i].Shell != w.shell {
			t.Errorf("call %d = %q/%q/shell=%v; want %q/%q/shell=%v",
				i, calls[i].Tool, calls[i].Target, calls[i].Shell, w.tool, w.target, w.shell)
		}
	}

	if len(results) != 2 {
		t.Fatalf("tool results = %+v", results)
	}
	if !results[0].IsError || results[0].ToolUseID != "call_a" {
		t.Errorf("nonzero exit should be an error: %+v", results[0])
	}
	if results[1].IsError {
		t.Errorf("exit code 0 should not be an error: %+v", results[1])
	}
	if !strings.Contains(results[1].Text, "Script completed") {
		t.Errorf("block-list output should flatten to text: %q", results[1].Text)
	}

	if counts.Entries != 6 || counts.Bytes == 0 {
		t.Errorf("counts = %+v", counts)
	}
}

func TestStreamDropsTheParentHistoryAChildReplays(t *testing.T) {
	root := t.TempDir()
	path := writeRollout(t, root, "2026/08/29", "rollout-child.jsonl", childRollout)

	events, _ := collect(t, transcript.Session{Harness: harness.Codex, Path: path}, transcript.Filter{})

	var turns []string
	for _, e := range events {
		if e.Kind == transcript.KindHumanTurn {
			turns = append(turns, e.Text)
		}
		// A thread is provenance under its root, never a session of its own.
		if e.SessionID != "root-1" || e.ThreadID != "thread-9" {
			t.Fatalf("child event identity = %q/%q", e.SessionID, e.ThreadID)
		}
	}
	if len(turns) != 1 || turns[0] != "why do you keep using sed" {
		t.Fatalf("the replayed parent turn must not count twice: %+v", turns)
	}
}

func TestStreamFallsBackToLegacyUserMessages(t *testing.T) {
	root := t.TempDir()
	path := writeRollout(t, root, "2026/05/08", "rollout-legacy.jsonl", legacyRollout)

	events, counts := collect(t, transcript.Session{Harness: harness.Codex, Path: path}, transcript.Filter{})

	if len(events) != 1 {
		t.Fatalf("expected one human turn, got %+v", events)
	}
	if events[0].Text != "don't use bare python" {
		t.Errorf("turn text = %q", events[0].Text)
	}
	// A legacy rollout carries its session id in the header's id field.
	if events[0].SessionID != "legacy-7" || events[0].ThreadID != "" {
		t.Errorf("legacy identity = %q/%q", events[0].SessionID, events[0].ThreadID)
	}
	if counts.Entries != 1 {
		t.Errorf("counts = %+v", counts)
	}
}

func TestStreamPrefersPrimaryTurnsOverLegacyEvents(t *testing.T) {
	root := t.TempDir()
	// A rollout recording a turn both ways must yield it once.
	both := rootRollout +
		`{"timestamp":"2026-08-29T20:35:34.000Z","ordinal":16,"type":"event_msg","payload":{"type":"user_message","message":"stop adding docstrings"}}` + "\n"
	path := writeRollout(t, root, "2026/08/29", "rollout-both.jsonl", both)

	events, _ := collect(t, transcript.Session{Harness: harness.Codex, Path: path}, transcript.Filter{})

	turns := 0
	for _, e := range events {
		if e.Kind == transcript.KindHumanTurn {
			turns++
		}
	}
	if turns != 1 {
		t.Fatalf("expected 1 human turn, got %d", turns)
	}
}

// A legacy turn is emitted at the end of the file, after every tool event, so
// it is marked deferred; a primary turn keeps its place and is not.
func TestStreamMarksLegacyTurnsDeferred(t *testing.T) {
	root := t.TempDir()
	interleaved := legacyRollout +
		`{"timestamp":"2026-05-08T01:42:50.000Z","ordinal":4,"type":"response_item","payload":{"type":"function_call","name":"exec_command","arguments":"{\"cmd\":\"ls\"}","call_id":"call_l"}}` + "\n" +
		`{"timestamp":"2026-05-08T01:42:55.000Z","ordinal":5,"type":"event_msg","payload":{"type":"user_message","message":"why did you list"}}` + "\n"
	legacy := writeRollout(t, root, "2026/05/08", "rollout-interleaved.jsonl", interleaved)
	primary := writeRollout(t, root, "2026/08/29", "rollout-alpha.jsonl", rootRollout)

	events, _ := collect(t, transcript.Session{Harness: harness.Codex, Path: legacy}, transcript.Filter{})
	var kinds []string
	for _, e := range events {
		kinds = append(kinds, string(e.Kind))
		if e.Kind == transcript.KindHumanTurn && !e.DeferredHumanTurn {
			t.Errorf("legacy turn %q is not marked deferred", e.Text)
		}
		if e.Kind != transcript.KindHumanTurn && e.DeferredHumanTurn {
			t.Errorf("tool event marked deferred: %+v", e)
		}
	}
	if strings.Join(kinds, ",") != "tool_call,human_turn,human_turn" {
		t.Fatalf("legacy emission order = %v", kinds)
	}

	events, _ = collect(t, transcript.Session{Harness: harness.Codex, Path: primary}, transcript.Filter{})
	for _, e := range events {
		if e.DeferredHumanTurn {
			t.Errorf("primary event marked deferred: %+v", e)
		}
	}
}

func TestStreamAppliesTheTimeWindow(t *testing.T) {
	root := t.TempDir()
	path := writeRollout(t, root, "2026/08/29", "rollout-alpha.jsonl", rootRollout)
	since, err := time.Parse(time.RFC3339, "2026-08-29T20:35:30Z")
	if err != nil {
		t.Fatal(err)
	}

	events, _ := collect(t, transcript.Session{Harness: harness.Codex, Path: path}, transcript.Filter{Since: since})

	for _, e := range events {
		if e.Timestamp < "2026-08-29T20:35:30" {
			t.Fatalf("event predates the window: %+v", e)
		}
	}
	if len(events) != 3 {
		t.Fatalf("expected the tail of the rollout, got %+v", events)
	}
}

func TestStreamCountsUnparsableLinesAsSkipped(t *testing.T) {
	root := t.TempDir()
	body := `{"timestamp":"2026-08-29T20:35:20.427Z","ordinal":0,"type":"session_meta","payload":{"session_id":"root-2","id":"root-2","cwd":"/repo/alpha"}}
not json at all
{"type":"response_item"}
{"timestamp":"2026-08-29T20:35:22.000Z","ordinal":2,"type":"response_item","payload":{"type":"reasoning","summary":[]}}
`
	path := writeRollout(t, root, "2026/08/29", "rollout-broken.jsonl", body)

	events, counts := collect(t, transcript.Session{Harness: harness.Codex, Path: path}, transcript.Filter{})

	if len(events) != 0 {
		t.Fatalf("expected no events, got %+v", events)
	}
	if counts.Entries != 0 || counts.Skipped != 4 {
		t.Errorf("counts = %+v", counts)
	}
}

func TestStreamOnMissingFileReportsTheError(t *testing.T) {
	_, err := New().Stream(
		transcript.Session{Harness: harness.Codex, Path: filepath.Join(t.TempDir(), "absent.jsonl")},
		transcript.Filter{},
		func(transcript.Event) error { return nil },
	)
	if err == nil {
		t.Fatal("expected an error for a missing rollout")
	}
}

func TestFailedExitReadsTheHeaderNotTheCommandOutput(t *testing.T) {
	cases := []struct {
		name string
		text string
		want bool
	}{
		{"clean run", "Command: /bin/zsh -lc 'ls'\nProcess exited with code 0\nOutput:\nfile.go", false},
		{"failed run", "Command: /bin/zsh -lc 'go test'\nProcess exited with code 1\nOutput:\nFAIL", true},
		// A command printing the marker as part of its own output does not make
		// the command itself a failure.
		{"marker inside output", "Command: /bin/zsh -lc 'cat log'\nProcess exited with code 0\nOutput:\nProcess exited with code 1", false},
		// A marker that never starts a line is text, not a status line.
		{"marker mid-line", "Command: /bin/zsh -lc 'echo'\nOutput:\nsaw Process exited with code 3 in the log", false},
		{"no marker", "Script completed\nWall time 1.3 seconds", false},
		// A hook block, an apply_patch rejection and a wrapper syntax error all
		// reach no process, so Codex writes this header and no exit code.
		{"blocked call", "Script failed\nWall time 0.1 seconds\nOutput:\nScript error:\nCommand blocked by PreToolUse hook: use python3", true},
		{"failed marker mid-line", "Script completed\nOutput:\nthe log says Script failed once", false},
		// A command that prints the marker is quoting it, not failing: the header
		// opens the result, so a later line carrying it belongs to the output.
		{"failed marker opens an output line", "Command: /bin/zsh -lc 'cat log'\nProcess exited with code 0\nOutput:\nScript failed", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resultFailed(tc.text); got != tc.want {
				t.Errorf("resultFailed = %v; want %v", got, tc.want)
			}
		})
	}
}

func TestEventsCarryTheHeadersWorkingDirectory(t *testing.T) {
	root := t.TempDir()
	path := writeRollout(t, root, "2026/08/29", "rollout-alpha.jsonl", rootRollout)

	events, _ := collect(t, transcript.Session{Harness: harness.Codex, Path: path}, transcript.Filter{})
	if len(events) == 0 {
		t.Fatal("the rollout emitted no events")
	}
	for i, e := range events {
		if e.Cwd != "/repo/alpha" {
			t.Errorf("event %d Cwd = %q; want %q", i, e.Cwd, "/repo/alpha")
		}
	}
}

// TestStreamMarksAnExecRunsTurnsMeta: a `codex exec` rollout's user turns are a
// program's prompt, so they are Meta; an interactive rollout's are not.
func TestStreamMarksAnExecRunsTurnsMeta(t *testing.T) {
	root := t.TempDir()
	exec := strings.Replace(rootRollout, `"originator":"codex-tui"`, `"originator":"codex_exec","source":"exec"`, 1)
	for name, c := range map[string]struct {
		body string
		meta bool
	}{"exec": {exec, true}, "interactive": {rootRollout, false}, "legacy": {legacyRollout, false}} {
		path := writeRollout(t, root, "2026/08/29", "rollout-"+name+".jsonl", c.body)
		events, _ := collect(t, transcript.Session{Harness: harness.Codex, Path: path}, transcript.Filter{})
		turns := 0
		for _, e := range events {
			if e.Kind == transcript.KindHumanTurn {
				turns++
				if e.Meta != c.meta {
					t.Errorf("%s: turn %q Meta = %v, want %v", name, e.Text, e.Meta, c.meta)
				}
			} else if e.Meta {
				t.Errorf("%s: %s event marked Meta", name, e.Kind)
			}
		}
		if turns == 0 {
			t.Errorf("%s: no human turn read", name)
		}
	}
}
