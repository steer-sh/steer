// Package denials records the tool calls Claude Code's auto mode classifier
// blocked. The PermissionDenied hook fires after that decision, so a row here
// is an observation and never a verdict: the recorder cannot clear a denial,
// and returns no retry grant.
package denials

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/steer-sh/steer-cli/internal/steerpath"
)

// snippetLimit matches the block-event audit log, so a reader that knows one
// field width knows both.
const snippetLimit = 200

// Payload is the PermissionDenied hook input. Claude Code sends tool_name,
// tool_input, tool_use_id and reason beside the fields every hook event carries.
type Payload struct {
	SessionID string         `json:"session_id"`
	CWD       string         `json:"cwd"`
	ToolName  string         `json:"tool_name"`
	ToolInput map[string]any `json:"tool_input"`
	ToolUseID string         `json:"tool_use_id"`
	Reason    string         `json:"reason"`
}

// Row is one denial. The field names it shares with the block-event audit log
// (enforcement, file_path, tool_name, snippet) carry the same meaning there.
type Row struct {
	Timestamp   string `json:"timestamp"`
	Host        string `json:"host"`
	Enforcement string `json:"enforcement"`
	SessionID   string `json:"session_id,omitempty"`
	CWD         string `json:"cwd,omitempty"`
	ToolName    string `json:"tool_name"`
	ToolUseID   string `json:"tool_use_id,omitempty"`
	Reason      string `json:"reason,omitempty"`
	FilePath    string `json:"file_path,omitempty"`
	Snippet     string `json:"snippet,omitempty"`
}

// EnforcementClassifier distinguishes a classifier denial from the "block" rows a
// steer hook writes. Both describe a tool call that did not run; only one of them
// came from a rule the user wrote.
const EnforcementClassifier = "classifier-block"

var hostSlugRe = regexp.MustCompile(`[^a-z0-9]+`)

// HostID names this machine for the shard. It reads the commit gate's own override
// as a fallback, so an operator who renames a host for one ledger renames it for
// both: two slugs for one machine would break the join the shared vocabulary buys.
func HostID() string {
	raw := os.Getenv("STEER_DENIALS_HOST")
	if raw == "" {
		raw = os.Getenv("COMMIT_REVIEW_HOST")
	}
	if raw == "" {
		raw, _ = os.Hostname()
	}
	first, _, _ := strings.Cut(strings.ToLower(raw), ".")
	slug := strings.Trim(hostSlugRe.ReplaceAllString(first, "-"), "-")
	if slug == "" {
		return "unknown"
	}
	return slug
}

// StateDir is the directory holding the shards.
func StateDir() string {
	if dir := os.Getenv("STEER_DENIALS_DIR"); dir != "" {
		return dir
	}
	return steerpath.StateDir("steer-denials")
}

// LogFile is the per-machine shard, so two hosts never append to the same file.
func LogFile() string {
	return filepath.Join(StateDir(), "denials."+HostID()+".jsonl")
}

// RowFrom converts a hook payload into the row to append.
func RowFrom(p Payload, now time.Time) Row {
	row := Row{
		Timestamp:   now.UTC().Format(time.RFC3339),
		Host:        HostID(),
		Enforcement: EnforcementClassifier,
		SessionID:   p.SessionID,
		CWD:         p.CWD,
		ToolName:    p.ToolName,
		ToolUseID:   p.ToolUseID,
		Reason:      p.Reason,
	}
	if path, ok := p.ToolInput["file_path"].(string); ok {
		row.FilePath = path
	}
	for _, key := range []string{"command", "content", "new_string", "prompt"} {
		value, ok := p.ToolInput[key].(string)
		if ok && strings.TrimSpace(value) != "" {
			row.Snippet = truncate(value, snippetLimit)
			break
		}
	}
	return row
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// Record reads one hook payload and appends its row. A malformed payload is an
// error: the caller decides what a recorder failure costs, and for the hook that
// cost is a message on stderr and exit 0.
func Record(in io.Reader, now time.Time) (Row, error) {
	data, err := io.ReadAll(in)
	if err != nil {
		return Row{}, fmt.Errorf("read payload: %w", err)
	}
	var payload Payload
	if err := json.Unmarshal(data, &payload); err != nil {
		return Row{}, fmt.Errorf("decode payload: %w", err)
	}
	row := RowFrom(payload, now)
	return row, Append(row)
}

// Append writes one row to the shard, creating the state directory if needed.
func Append(row Row) error {
	if err := os.MkdirAll(StateDir(), 0o755); err != nil {
		return fmt.Errorf("state dir: %w", err)
	}
	line, err := json.Marshal(row)
	if err != nil {
		return fmt.Errorf("encode row: %w", err)
	}
	f, err := os.OpenFile(LogFile(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open shard: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("append shard: %w", err)
	}
	return nil
}

// Read returns every row in this machine's shard, oldest first. A line that does
// not decode is skipped: one corrupt append must not hide the rest of the corpus.
func Read() ([]Row, error) {
	data, err := os.ReadFile(LogFile())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var rows []Row
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var row Row
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			continue
		}
		rows = append(rows, row)
	}
	return rows, nil
}
