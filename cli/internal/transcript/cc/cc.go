// Package cc reads Claude Code session transcripts into normalized events.
package cc

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/steer-sh/steer-cli/internal/harness"
	"github.com/steer-sh/steer-cli/internal/transcript"
)

// Adapter reads ~/.claude/projects.
type Adapter struct{}

func New() Adapter { return Adapter{} }

func (Adapter) Harness() harness.ID { return harness.ClaudeCode }

// instrument is this adapter's normalization identity, pinned by
// TestInstrumentMatchesFixtureReplay. Bump it when that test fails.
const instrument = "claude-code:4b7c1a39"

func (Adapter) Instrument() string { return instrument }

// Root returns the transcript root, ~/.claude/projects.
func (Adapter) Root() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to resolve home directory: %w", err)
	}
	return filepath.Join(home, ".claude", "projects"), nil
}

// Discover lists transcript files modified at or after f.Since. Callers pass
// both the literal working directory and its symlink-resolved form: Claude Code
// names transcript directories after whichever path the session was launched
// from.
func (a Adapter) Discover(root string, f transcript.Filter) ([]transcript.Session, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read %s: %w", root, err)
	}

	want := map[string]struct{}{}
	for _, p := range f.Projects {
		want[transcript.NormalizeProject(p)] = struct{}{}
	}

	var sessions []transcript.Session
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if !f.AllProjects {
			if _, ok := want[transcript.NormalizeProject(entry.Name())]; !ok {
				continue
			}
		}

		dir := filepath.Join(root, entry.Name())
		items, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, item := range items {
			if item.IsDir() || !strings.HasSuffix(item.Name(), ".jsonl") {
				continue
			}
			info, err := item.Info()
			if err != nil {
				continue
			}
			if info.ModTime().Before(f.Since) {
				continue
			}
			sessions = append(sessions, transcript.Session{
				Harness: harness.ClaudeCode,
				Path:    filepath.Join(dir, item.Name()),
				Project: entry.Name(),
			})
		}
	}
	return sessions, nil
}

// row is the subset of a transcript line this adapter reads.
type row struct {
	Type        string   `json:"type"`
	IsMeta      bool     `json:"isMeta"`
	IsSidechain bool     `json:"isSidechain"`
	Timestamp   string   `json:"timestamp"`
	SessionID   string   `json:"sessionId"`
	Message     *message `json:"message"`
}

type message struct {
	Content json.RawMessage `json:"content"`
}

type contentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
	ToolUseID string          `json:"tool_use_id"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
}

type toolUseInput struct {
	Command  string `json:"command"`
	FilePath string `json:"file_path"`
}

// Stream normalizes one transcript. An unparsable line is skipped, never fatal:
// a partially-written transcript must not sink the whole scan.
func (a Adapter) Stream(s transcript.Session, f transcript.Filter, emit func(transcript.Event) error) (transcript.Counts, error) {
	var counts transcript.Counts
	file, err := os.Open(s.Path)
	if err != nil {
		return counts, err
	}
	defer file.Close()

	reader := bufio.NewReaderSize(file, 256*1024)
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 {
			counts.Bytes += int64(len(line))
			if err := a.normalize(line, f, &counts, emit); err != nil {
				return counts, err
			}
		}
		if readErr == io.EOF {
			return counts, nil
		}
		if readErr != nil {
			return counts, readErr
		}
	}
}

func (a Adapter) normalize(line []byte, f transcript.Filter, counts *transcript.Counts, emit func(transcript.Event) error) error {
	var r row
	if err := json.Unmarshal(line, &r); err != nil {
		counts.Skipped++
		return nil
	}
	if r.Type != "user" && r.Type != "assistant" {
		counts.Skipped++
		return nil
	}
	if r.IsSidechain || r.Message == nil {
		counts.Skipped++
		return nil
	}
	if !inWindow(r.Timestamp, f.Since) {
		counts.Skipped++
		return nil
	}
	counts.Entries++

	base := transcript.Event{SessionID: r.SessionID, Timestamp: r.Timestamp, Meta: r.IsMeta}

	if r.Type == "assistant" {
		return a.emitToolCalls(r.Message.Content, base, emit)
	}

	// A user entry is either plain text — a human turn — or tool_result blocks.
	var text string
	if err := json.Unmarshal(r.Message.Content, &text); err == nil {
		event := base
		event.Kind, event.Text = transcript.KindHumanTurn, text
		return emit(event)
	}

	var blocks []contentBlock
	if err := json.Unmarshal(r.Message.Content, &blocks); err != nil {
		return nil
	}
	for _, b := range blocks {
		switch b.Type {
		case "text":
			event := base
			event.Kind, event.Text = transcript.KindHumanTurn, b.Text
			if err := emit(event); err != nil {
				return err
			}
		case "tool_result":
			event := base
			event.Kind = transcript.KindToolResult
			event.Text = resultText(b)
			event.ToolUseID = b.ToolUseID
			event.IsError = b.IsError
			if err := emit(event); err != nil {
				return err
			}
		}
	}
	return nil
}

func (a Adapter) emitToolCalls(raw json.RawMessage, base transcript.Event, emit func(transcript.Event) error) error {
	var blocks []contentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil
	}
	for _, b := range blocks {
		if b.Type != "tool_use" || b.ID == "" {
			continue
		}
		var input toolUseInput
		_ = json.Unmarshal(b.Input, &input)
		target := input.Command
		if target == "" {
			target = input.FilePath
		}
		event := base
		event.Kind, event.Tool, event.Target, event.ToolUseID = transcript.KindToolCall, b.Name, target, b.ID
		event.Shell = b.Name == "Bash"
		if err := emit(event); err != nil {
			return err
		}
	}
	return nil
}

// resultText flattens a tool_result content payload, which is either a string
// or a list of blocks.
func resultText(b contentBlock) string {
	var text string
	if err := json.Unmarshal(b.Content, &text); err == nil {
		return text
	}
	var blocks []contentBlock
	if err := json.Unmarshal(b.Content, &blocks); err != nil {
		return ""
	}
	var parts []string
	for _, inner := range blocks {
		if inner.Type == "text" && inner.Text != "" {
			parts = append(parts, inner.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// inWindow keeps an entry whose timestamp is missing or unparsable: dropping it
// would silently narrow the corpus on a format change.
func inWindow(timestamp string, since time.Time) bool {
	if timestamp == "" || since.IsZero() {
		return true
	}
	t, err := time.Parse(time.RFC3339, timestamp)
	if err != nil {
		return true
	}
	return !t.Before(since)
}
