package cc

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/steer-sh/steer-cli/internal/harness"
	"github.com/steer-sh/steer-cli/internal/transcript"
)

func TestDiscoverMatchesProject(t *testing.T) {
	root := t.TempDir()
	match := filepath.Join(root, "-Users-jeremy-my-proj")
	other := filepath.Join(root, "-Users-jeremy-other")
	for _, d := range []string{match, other} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "a.jsonl"), []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	a := New()

	files, err := a.Discover(root, transcript.Filter{Projects: []string{"/Users/jeremy/my.proj"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Project != "-Users-jeremy-my-proj" {
		t.Fatalf("expected only matching project, got %+v", files)
	}
	if files[0].Harness != harness.ClaudeCode {
		t.Errorf("session harness = %q; want %q", files[0].Harness, harness.ClaudeCode)
	}

	// A symlinked cwd matches via its resolved alias.
	aliased, err := a.Discover(root, transcript.Filter{Projects: []string{"/Volumes/Ext/my.proj", "/Users/jeremy/my.proj"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(aliased) != 1 {
		t.Fatalf("expected alias path to match, got %+v", aliased)
	}

	all, err := a.Discover(root, transcript.Filter{Projects: []string{"/Users/jeremy/my.proj"}, AllProjects: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 files with AllProjects, got %d", len(all))
	}

	future := time.Now().Add(time.Hour)
	none, err := a.Discover(root, transcript.Filter{Since: future, AllProjects: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 {
		t.Fatalf("expected mtime filter to exclude all files, got %d", len(none))
	}
}

func TestDiscoverOnMissingRootYieldsNothing(t *testing.T) {
	files, err := New().Discover(filepath.Join(t.TempDir(), "absent"), transcript.Filter{AllProjects: true})
	if err != nil || files != nil {
		t.Fatalf("Discover on a missing root = %+v, %v; want nil, nil", files, err)
	}
}

func write(t *testing.T, lines []string) transcript.Session {
	t.Helper()
	path := filepath.Join(t.TempDir(), "s.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return transcript.Session{Harness: harness.ClaudeCode, Path: path}
}

func line(v map[string]any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func stream(t *testing.T, s transcript.Session, f transcript.Filter) ([]transcript.Event, transcript.Counts) {
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

func TestStreamNormalizesEachEntryKind(t *testing.T) {
	session := write(t, []string{
		line(map[string]any{"type": "user", "sessionId": "s1", "timestamp": "2026-07-10T00:00:00Z",
			"message": map[string]any{"content": "stop using var"}}),
		line(map[string]any{"type": "assistant", "sessionId": "s1", "timestamp": "2026-07-10T00:00:01Z",
			"message": map[string]any{"content": []any{
				map[string]any{"type": "tool_use", "id": "t1", "name": "Bash", "input": map[string]any{"command": "go test ./..."}},
				map[string]any{"type": "tool_use", "id": "t2", "name": "Write", "input": map[string]any{"file_path": "/tmp/a.go"}},
			}}}),
		line(map[string]any{"type": "user", "sessionId": "s1", "timestamp": "2026-07-10T00:00:02Z",
			"message": map[string]any{"content": []any{
				map[string]any{"type": "tool_result", "tool_use_id": "t1", "is_error": true,
					"content": []any{map[string]any{"type": "text", "text": "FAIL: boom"}}},
			}}}),
	})

	events, counts := stream(t, session, transcript.Filter{})
	if counts.Entries != 3 || counts.Skipped != 0 {
		t.Fatalf("counts = %+v; want 3 entries, 0 skipped", counts)
	}
	if counts.Bytes == 0 {
		t.Error("Bytes was not accumulated")
	}
	if len(events) != 4 {
		t.Fatalf("expected 4 events (1 turn, 2 calls, 1 result), got %d: %+v", len(events), events)
	}

	if events[0].Kind != transcript.KindHumanTurn || events[0].Text != "stop using var" {
		t.Errorf("first event = %+v; want the human turn", events[0])
	}
	// A tool_use whose input names a file, not a command, still yields a target.
	if events[2].Kind != transcript.KindToolCall || events[2].Tool != "Write" || events[2].Target != "/tmp/a.go" {
		t.Errorf("second call = %+v; want the Write call targeting the file path", events[2])
	}
	// A block-list tool_result flattens to plain text and keeps its join key.
	last := events[3]
	if last.Kind != transcript.KindToolResult || last.Text != "FAIL: boom" || last.ToolUseID != "t1" || !last.IsError {
		t.Errorf("result event = %+v; want the flattened failing result for t1", last)
	}
}

// Every entry the adapter refuses is counted, so a consumer's coverage stats
// stay honest about what the corpus contained.
func TestStreamCountsEverySkipReason(t *testing.T) {
	session := write(t, []string{
		"not json at all",
		line(map[string]any{"type": "summary", "sessionId": "s1"}),
		line(map[string]any{"type": "user", "sessionId": "s1", "isSidechain": true,
			"message": map[string]any{"content": "sub-agent turn"}}),
		line(map[string]any{"type": "user", "sessionId": "s1", "timestamp": "2026-07-10T00:00:00Z"}),
		line(map[string]any{"type": "user", "sessionId": "s1", "timestamp": "2020-01-01T00:00:00Z",
			"message": map[string]any{"content": "too old"}}),
		line(map[string]any{"type": "user", "sessionId": "s1", "timestamp": "2026-07-10T00:00:00Z",
			"message": map[string]any{"content": "kept"}}),
	})

	since, _ := time.Parse(time.RFC3339, "2026-01-01T00:00:00Z")
	events, counts := stream(t, session, transcript.Filter{Since: since})
	if counts.Entries != 1 || counts.Skipped != 5 {
		t.Fatalf("counts = %+v; want 1 entry, 5 skipped", counts)
	}
	if len(events) != 1 || events[0].Text != "kept" {
		t.Fatalf("events = %+v; want only the in-window turn", events)
	}
}

// An entry with no timestamp, or one the adapter cannot parse, is kept: dropping
// it would narrow the corpus silently on a format change.
func TestStreamKeepsUnreadableTimestamps(t *testing.T) {
	session := write(t, []string{
		line(map[string]any{"type": "user", "sessionId": "s1", "message": map[string]any{"content": "no stamp"}}),
		line(map[string]any{"type": "user", "sessionId": "s1", "timestamp": "yesterday",
			"message": map[string]any{"content": "bad stamp"}}),
	})
	since, _ := time.Parse(time.RFC3339, "2026-01-01T00:00:00Z")
	events, counts := stream(t, session, transcript.Filter{Since: since})
	if counts.Entries != 2 || len(events) != 2 {
		t.Fatalf("counts = %+v, events = %d; want both entries kept", counts, len(events))
	}
}

func TestStreamCarriesMetaAndSessionID(t *testing.T) {
	session := write(t, []string{
		line(map[string]any{"type": "user", "sessionId": "s9", "isMeta": true, "timestamp": "2026-07-10T00:00:00Z",
			"message": map[string]any{"content": "Caveat: resumed"}}),
	})
	events, _ := stream(t, session, transcript.Filter{})
	if len(events) != 1 || !events[0].Meta || events[0].SessionID != "s9" {
		t.Fatalf("events = %+v; want one meta event on session s9", events)
	}
}

func TestStreamOnMissingFileReportsTheError(t *testing.T) {
	_, err := New().Stream(transcript.Session{Path: filepath.Join(t.TempDir(), "gone.jsonl")},
		transcript.Filter{}, func(transcript.Event) error { return nil })
	if err == nil {
		t.Error("Stream on a missing transcript returned no error")
	}
}
