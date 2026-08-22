package denials

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func withTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("STEER_DENIALS_DIR", dir)
	t.Setenv("STEER_DENIALS_HOST", "Test-Host.local")
	return dir
}

func TestLogFileIsPerHost(t *testing.T) {
	dir := withTempDir(t)
	want := filepath.Join(dir, "denials.test-host.jsonl")
	if got := LogFile(); got != want {
		t.Fatalf("LogFile() = %q, want %q", got, want)
	}
}

func TestRecordAppendsOneRowFromTheHookPayload(t *testing.T) {
	withTempDir(t)
	payload := `{
		"session_id": "s1",
		"cwd": "/repo",
		"tool_name": "Bash",
		"tool_use_id": "toolu_1",
		"reason": "Blocked by classifier",
		"tool_input": {"command": "security dump-keychain"}
	}`
	row, err := Record(strings.NewReader(payload), time.Unix(0, 0))
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if row.ToolName != "Bash" || row.ToolUseID != "toolu_1" {
		t.Fatalf("row lost identity fields: %+v", row)
	}
	if row.Snippet != "security dump-keychain" {
		t.Fatalf("snippet = %q", row.Snippet)
	}
	if row.Enforcement != EnforcementClassifier {
		t.Fatalf("enforcement = %q, want %q", row.Enforcement, EnforcementClassifier)
	}
	rows, err := Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(rows) != 1 || rows[0].ToolUseID != "toolu_1" {
		t.Fatalf("Read() = %+v", rows)
	}
}

func TestRecordRejectsAMalformedPayloadWithoutWriting(t *testing.T) {
	withTempDir(t)
	if _, err := Record(strings.NewReader("not json"), time.Unix(0, 0)); err == nil {
		t.Fatal("expected an error for a malformed payload")
	}
	if _, err := os.Stat(LogFile()); !os.IsNotExist(err) {
		t.Fatalf("a malformed payload created the shard: %v", err)
	}
}

func TestRowFromPrefersFilePathAndTruncatesTheSnippet(t *testing.T) {
	withTempDir(t)
	long := strings.Repeat("x", snippetLimit+50)
	row := RowFrom(Payload{
		ToolName:  "Edit",
		ToolInput: map[string]any{"file_path": "/repo/a.go", "new_string": long},
	}, time.Unix(0, 0))
	if row.FilePath != "/repo/a.go" {
		t.Fatalf("file_path = %q", row.FilePath)
	}
	if len(row.Snippet) != snippetLimit {
		t.Fatalf("snippet length = %d, want %d", len(row.Snippet), snippetLimit)
	}
}

func TestReadSkipsACorruptLine(t *testing.T) {
	withTempDir(t)
	if err := Append(Row{Timestamp: "t1", ToolName: "Bash"}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	f, err := os.OpenFile(LogFile(), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	f.WriteString("{ truncated\n")
	f.Close()
	if err := Append(Row{Timestamp: "t2", ToolName: "Edit"}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	rows, err := Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(rows) != 2 || rows[1].ToolName != "Edit" {
		t.Fatalf("Read() = %+v", rows)
	}
}

func TestRowOmitsEmptyOptionalFields(t *testing.T) {
	withTempDir(t)
	data, err := json.Marshal(RowFrom(Payload{ToolName: "Bash"}, time.Unix(0, 0)))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, key := range []string{"file_path", "snippet", "reason", "cwd", "session_id"} {
		if strings.Contains(string(data), key) {
			t.Fatalf("empty %s survived into the row: %s", key, data)
		}
	}
}

func TestHostIDFallsBackToTheCommitGateOverride(t *testing.T) {
	t.Setenv("STEER_DENIALS_HOST", "")
	t.Setenv("COMMIT_REVIEW_HOST", "Fleet-Box.example.com")
	if got := HostID(); got != "fleet-box" {
		t.Fatalf("HostID() = %q, want the commit gate's slug %q", got, "fleet-box")
	}
	t.Setenv("STEER_DENIALS_HOST", "Local-Name")
	if got := HostID(); got != "local-name" {
		t.Fatalf("HostID() = %q, want the denials override to win", got)
	}
}
