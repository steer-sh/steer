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
const instrument = "claude-code:672cb59e"

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

	keep := f.Selector()

	var sessions []transcript.Session
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		// A project scope reads the directory name alone, so a whole directory
		// is skipped before any file in it is opened. A repository scope cannot:
		// the name is munged, and only the file carries the launch directory.
		if !f.ScopesRepo() && !keep(transcript.Session{Project: entry.Name()}) {
			continue
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
			path := filepath.Join(dir, item.Name())
			session := transcript.Session{
				Harness: harness.ClaudeCode,
				Path:    path,
				Project: entry.Name(),
				Cwd:     readLaunchCwd(path),
			}
			if !keep(session) {
				continue
			}
			sessions = append(sessions, session)
		}
	}
	return sessions, nil
}

// readLaunchCwd returns the working directory the session started in: the cwd
// of the first entry that records one. It reads no time window, because a
// session whose early entries fall outside the window was still launched where
// its first entry says. An unreadable file yields "".
func readLaunchCwd(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()

	reader := bufio.NewReaderSize(file, 256*1024)
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 {
			var r row
			if err := json.Unmarshal(line, &r); err == nil && !r.IsSidechain && r.Cwd != "" {
				return r.Cwd
			}
		}
		if readErr != nil {
			return ""
		}
	}
}

// row is the subset of a transcript line this adapter reads.
type row struct {
	Type        string   `json:"type"`
	IsMeta      bool     `json:"isMeta"`
	IsSidechain bool     `json:"isSidechain"`
	Timestamp   string   `json:"timestamp"`
	SessionID   string   `json:"sessionId"`
	Cwd         string   `json:"cwd"`
	Entrypoint  string   `json:"entrypoint"`
	Message     *message `json:"message"`
}

// headlessEntrypoint is the entrypoint of a `claude -p` run. Its user turns are the
// prompt the calling program passed, so no person typed them.
const headlessEntrypoint = "sdk-cli"

type message struct {
	Content json.RawMessage `json:"content"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
	// Thinking is the assistant's reasoning. Stream normalizes no thinking
	// block; the continuation walk reads it, because the narrative after a
	// block is what says whether the task carried on.
	Thinking  string          `json:"thinking"`
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

	// Claude Code records the live shell working directory on each row, so Cwd
	// moves within one session as the agent changes directory.
	base := transcript.Event{SessionID: r.SessionID, Timestamp: r.Timestamp, Meta: r.IsMeta, Cwd: r.Cwd}

	if r.Type == "assistant" {
		return a.emitToolCalls(r.Message.Content, base, emit)
	}

	// A user entry is either plain text — a human turn — or tool_result blocks.
	human := base
	human.Kind, human.Meta = transcript.KindHumanTurn, r.IsMeta || r.Entrypoint == headlessEntrypoint
	var text string
	if err := json.Unmarshal(r.Message.Content, &text); err == nil {
		human.Text = text
		return emit(human)
	}

	var blocks []contentBlock
	if err := json.Unmarshal(r.Message.Content, &blocks); err != nil {
		return nil
	}
	for _, b := range blocks {
		switch b.Type {
		case "text":
			event := human
			event.Text = b.Text
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
