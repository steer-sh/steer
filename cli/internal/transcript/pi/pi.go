// Package pi reads Pi session transcripts into normalized events.
package pi

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/steer-sh/steer-cli/internal/harness"
	"github.com/steer-sh/steer-cli/internal/transcript"
)

// Adapter reads the Pi session tree.
type Adapter struct{}

func New() Adapter { return Adapter{} }

func (Adapter) Harness() harness.ID { return harness.Pi }

// SessionDirEnv overrides the session root, ahead of the config directory Pi
// otherwise stores sessions under.
const SessionDirEnv = "PI_CODING_AGENT_SESSION_DIR"

// Root returns the session root: the environment override, else sessions under
// the Pi config directory.
func (Adapter) Root() (string, error) {
	if configured := strings.TrimSpace(os.Getenv(SessionDirEnv)); configured != "" {
		return configured, nil
	}
	h, ok := harness.Get(harness.Pi)
	if !ok {
		return "", os.ErrNotExist
	}
	dir, err := h.ConfigDir(harness.GlobalScope, "")
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "sessions"), nil
}

// Discover walks the per-project session directories. The project comes from
// each file's header, never from the directory name: Pi mungs only '/' to '-'
// and wraps the result in '--', while NormalizeProject replaces every
// non-alphanumeric byte, so the two forms do not compare equal.
func (a Adapter) Discover(root string, f transcript.Filter) ([]transcript.Session, error) {
	want := map[string]struct{}{}
	for _, p := range f.Projects {
		want[transcript.NormalizeProject(p)] = struct{}{}
	}

	var sessions []transcript.Session
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // an unreadable subtree narrows the scan, never ends it
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.ModTime().Before(f.Since) {
			return nil
		}
		project := readHeader(path).Cwd
		if !f.AllProjects {
			if _, ok := want[transcript.NormalizeProject(project)]; !ok {
				return nil
			}
		}
		sessions = append(sessions, transcript.Session{
			Harness: harness.Pi,
			Path:    path,
			Project: project,
		})
		return nil
	})
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return sessions, nil
}

const (
	typeSession = "session"
	typeMessage = "message"

	roleUser       = "user"
	roleAssistant  = "assistant"
	roleToolResult = "toolResult"
	blockText      = "text"
	blockToolCall  = "toolCall"
)

// header is the session file's first line. It is metadata, not part of the
// entry tree, so it carries none of the tree's id/parentId shape.
type header struct {
	Type string `json:"type"`
	ID   string `json:"id"`
	Cwd  string `json:"cwd"`
	// ParentSession names the file a /fork or /clone came from. Pi's
	// createBranchedSession COPIES that file's branch into this one, keeping
	// every copied entry's id and rewriting only its parentId, so the copy is
	// what the ids identify.
	ParentSession string `json:"parentSession"`
}

// entry is one line of the tree. Every entry type shares id/parentId; only a
// message carries a payload this adapter emits.
type entry struct {
	Type      string   `json:"type"`
	ID        string   `json:"id"`
	ParentID  string   `json:"parentId"`
	Timestamp string   `json:"timestamp"`
	Message   *message `json:"message"`
}

type message struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content"`
	ToolCallID string          `json:"toolCallId"`
	ToolName   string          `json:"toolName"`
	IsError    bool            `json:"isError"`
}

type block struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

func readHeader(path string) header {
	file, err := os.Open(path)
	if err != nil {
		return header{}
	}
	defer file.Close()

	reader := bufio.NewReaderSize(file, 256*1024)
	line, err := reader.ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return header{}
	}
	var h header
	if err := json.Unmarshal(line, &h); err != nil || h.Type != typeSession {
		return header{}
	}
	return h
}

// Stream normalizes one session file. It reads the file twice, because the file
// is a tree rather than a log: an /undo, a /tree switch or a retried prompt
// leaves the abandoned branch in place, and a linear pass would emit its human
// turns and orphaned tool calls into the same timeline as the live ones.
//
// Pass one links every entry to its parent and names the leaf, which is the
// last entry in file order. Pass two walks parentId back from that leaf, then
// emits, in file order, only the entries on that path.
func (a Adapter) Stream(s transcript.Session, f transcript.Filter, emit func(transcript.Event) error) (transcript.Counts, error) {
	var counts transcript.Counts

	active, err := activeBranch(s.Path)
	if err != nil {
		return counts, err
	}

	file, err := os.Open(s.Path)
	if err != nil {
		return counts, err
	}
	defer file.Close()

	st := &streamState{filter: f, emit: emit, active: active, replayed: replayedIDs(s.Path)}
	reader := bufio.NewReaderSize(file, 256*1024)
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 {
			st.counts.Bytes += int64(len(line))
			if err := st.normalize(line); err != nil {
				return st.counts, err
			}
		}
		if readErr == io.EOF {
			return st.counts, nil
		}
		if readErr != nil {
			return st.counts, readErr
		}
	}
}

// activeBranch returns the ids on the path from the leaf to the root. A nil set
// means the file records no tree at all, and every entry then reads as active:
// a file Steer cannot link is better over-counted than silently emptied.
func activeBranch(path string) (map[string]struct{}, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	parents := map[string]string{}
	leaf := ""
	linked := false
	reader := bufio.NewReaderSize(file, 256*1024)
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 {
			var e entry
			if err := json.Unmarshal(line, &e); err == nil && e.ID != "" &&
				e.Type != "" && e.Type != typeSession {
				if _, ok := parents[e.ParentID]; ok {
					linked = true
				}
				parents[e.ID] = e.ParentID
				leaf = e.ID
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, readErr
		}
	}
	// A file recording no link at all records no tree, and every entry then
	// reads as active: a session Steer cannot link is better over-counted than
	// silently emptied down to its last row.
	if leaf == "" || (!linked && len(parents) > 1) {
		return nil, nil
	}

	active := map[string]struct{}{}
	for id := leaf; id != ""; {
		if _, seen := active[id]; seen {
			break // a cycle cannot happen in a written tree, and must not hang a read
		}
		active[id] = struct{}{}
		parent, ok := parents[id]
		if !ok {
			break
		}
		id = parent
	}
	return active, nil
}

// streamState carries what one file's normalization needs across lines: the
// session identity from its header, and the active branch from pass one.
type streamState struct {
	filter transcript.Filter
	emit   func(transcript.Event) error
	counts transcript.Counts
	active map[string]struct{}
	// replayed holds the entry ids this file copied from the session it was
	// forked out of. They belong to that session's count, not to this one's.
	replayed map[string]struct{}

	sessionID string
}

// normalize reads one line. Skipped is reserved for a line Steer cannot read at
// all — a JSON parse failure, or an entry naming no type. A row Steer reads and
// chooses not to emit is counted as an entry: reporting it as skipped would
// make every Pi session look lossy, and Pi records several entry types that
// carry no evidence by design.
func (st *streamState) normalize(line []byte) error {
	if len(strings.TrimSpace(string(line))) == 0 {
		return nil
	}
	var e entry
	if err := json.Unmarshal(line, &e); err != nil || e.Type == "" {
		st.counts.Skipped++
		return nil
	}
	st.counts.Entries++

	if e.Type == typeSession {
		var h header
		if err := json.Unmarshal(line, &h); err == nil {
			st.sessionID = h.ID
		}
		return nil
	}
	if e.Type != typeMessage || e.Message == nil {
		return nil
	}
	if st.active != nil {
		if _, ok := st.active[e.ID]; !ok {
			return nil
		}
	}
	if _, ok := st.replayed[e.ID]; ok {
		return nil
	}
	if !inWindow(e.Timestamp, st.filter.Since) {
		return nil
	}

	// ThreadID stays empty: Pi's id/parentId tree is in-file branching, not a
	// subagent thread, and a fork writes its own file with its own header id.
	base := transcript.Event{SessionID: st.sessionID, Timestamp: e.Timestamp}
	switch e.Message.Role {
	case roleUser:
		base.Kind, base.Text = transcript.KindHumanTurn, contentText(e.Message.Content)
		return st.emit(base)
	case roleAssistant:
		return st.toolCalls(e.Message.Content, base)
	case roleToolResult:
		base.Kind = transcript.KindToolResult
		base.Tool, base.ToolUseID = e.Message.ToolName, e.Message.ToolCallID
		base.IsError, base.Text = e.Message.IsError, contentText(e.Message.Content)
		return st.emit(base)
	}
	// Every other role is read and dropped. bashExecution is the one that
	// matters: it records a command the PERSON ran with ! or !!, which carries
	// no toolCallId and never was a model tool call, so emitting it would
	// attribute a person's own shell failure to the model.
	return nil
}

func (st *streamState) toolCalls(content json.RawMessage, base transcript.Event) error {
	var blocks []block
	if len(content) == 0 || json.Unmarshal(content, &blocks) != nil {
		return nil
	}
	for _, b := range blocks {
		if b.Type != blockToolCall {
			continue
		}
		e := base
		e.Kind, e.Tool, e.ToolUseID = transcript.KindToolCall, b.Name, b.ID
		e.Shell = isShell(b.Name)
		e.Target = callTarget(b.Name, b.Arguments)
		if err := st.emit(e); err != nil {
			return err
		}
	}
	return nil
}

// isShell names the Pi tools that run a shell command. The Shell flag is what a
// classifier reads instead of a tool name, so it must be true for these alone.
func isShell(name string) bool { return name == "bash" || name == "powershell" }

// callTarget extracts what a tool call acted on: the command for a shell call,
// the pattern for a search, the path for a file call. A search takes both a
// path and a pattern, and the pattern is the half that names what was looked
// for. An unrecognized tool falls through the same three fields, so a Pi
// release adding a tool still yields a clustering key.
func callTarget(name string, raw json.RawMessage) string {
	var args struct {
		Command string `json:"command"`
		Path    string `json:"path"`
		Pattern string `json:"pattern"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &args) != nil {
		return ""
	}
	order := []string{args.Path, args.Pattern, args.Command}
	switch {
	case isShell(name):
		order = []string{args.Command}
	case isSearch(name):
		order = []string{args.Pattern, args.Path, args.Command}
	}
	for _, v := range order {
		if v != "" {
			return v
		}
	}
	return ""
}

// isSearch names the Pi tools whose subject is a pattern rather than a path.
func isSearch(name string) bool { return name == "grep" || name == "find" }

// replayedIDs returns the entry ids a forked file copied from its parent. Pi's
// /fork over a chosen entry writes the parent's whole branch into the new file,
// keeping each copied entry's id, so both files hold those turns. Reading the
// parent is what separates them; when the parent is gone the ids are dropped
// from no corpus and counting them here is right.
func replayedIDs(path string) map[string]struct{} {
	parent := strings.TrimSpace(readHeader(path).ParentSession)
	if parent == "" {
		return nil
	}
	file, err := os.Open(parent)
	if err != nil {
		return nil
	}
	defer file.Close()

	ids := map[string]struct{}{}
	reader := bufio.NewReaderSize(file, 256*1024)
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 {
			var e entry
			if err := json.Unmarshal(line, &e); err == nil && e.ID != "" && e.Type != typeSession {
				ids[e.ID] = struct{}{}
			}
		}
		if readErr != nil {
			return ids
		}
	}
}

// contentText flattens a message body, which the format defines as either a
// bare string or a list of typed blocks. Only text blocks carry text; an image
// block is dropped.
func contentText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	var blocks []block
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == blockText && b.Text != "" {
			parts = append(parts, b.Text)
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
