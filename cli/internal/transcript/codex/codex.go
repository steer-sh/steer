// Package codex reads Codex rollout transcripts into normalized events.
package codex

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/steer-sh/steer-cli/internal/harness"
	"github.com/steer-sh/steer-cli/internal/transcript"
)

// Adapter reads $CODEX_HOME/sessions.
type Adapter struct{}

func New() Adapter { return Adapter{} }

func (Adapter) Harness() harness.ID { return harness.Codex }

// instrument is this adapter's normalization identity, pinned by
// TestInstrumentMatchesFixtureReplay. Bump it when that test fails.
const instrument = "codex:1e3d4014"

func (Adapter) Instrument() string { return instrument }

// Root returns the rollout root under the Codex config directory.
func (Adapter) Root() (string, error) {
	h, ok := harness.Get(harness.Codex)
	if !ok {
		return "", os.ErrNotExist
	}
	dir, err := h.ConfigDir(harness.GlobalScope, "")
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "sessions"), nil
}

// Discover walks the date-partitioned rollout tree. A subagent thread writes
// its own rollout file, so one root session can appear as several files here;
// they rejoin through the session id the events carry.
func (a Adapter) Discover(root string, f transcript.Filter) ([]transcript.Session, error) {
	keep := f.Selector()

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
		// The header cwd is both the project key and the launch directory: a
		// rollout records the directory once, at the top of the file.
		project := readProject(path)
		session := transcript.Session{
			Harness: harness.Codex,
			Path:    path,
			Project: project,
			Cwd:     project,
		}
		if !keep(session) {
			return nil
		}
		sessions = append(sessions, session)
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

// readProject returns the working directory a rollout ran in, read from its
// session_meta header. An unreadable header yields "", which matches no
// project filter and leaves the file out of a scoped scan.
func readProject(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()

	reader := bufio.NewReaderSize(file, 256*1024)
	line, err := reader.ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return ""
	}
	var r row
	if err := json.Unmarshal(line, &r); err != nil || r.Type != typeSessionMeta || r.Payload == nil {
		return ""
	}
	var meta sessionMeta
	if err := json.Unmarshal(r.Payload, &meta); err != nil {
		return ""
	}
	return meta.Cwd
}

const (
	typeSessionMeta  = "session_meta"
	typeResponseItem = "response_item"
	typeEventMsg     = "event_msg"
)

// row is the rollout envelope: every line carries a type and a typed payload.
type row struct {
	Type      string          `json:"type"`
	Timestamp string          `json:"timestamp"`
	Ordinal   int             `json:"ordinal"`
	Payload   json.RawMessage `json:"payload"`
}

// sessionMeta is the rollout header. SessionID names the root session and is
// absent on legacy rollouts, where ID carries it instead. On a subagent
// rollout the two differ: SessionID is the parent's and ID is the thread's.
type sessionMeta struct {
	SessionID string `json:"session_id"`
	ID        string `json:"id"`
	Cwd       string `json:"cwd"`
	// SubagentHistoryStart is the first ordinal belonging to this thread. A
	// subagent forks its parent's history, so every earlier entry in this file
	// is a verbatim replay of entries the parent's own rollout already holds.
	SubagentHistoryStart int `json:"subagent_history_start_ordinal"`
}

// payload is the union of the response_item and event_msg fields this adapter
// reads. Codex distinguishes them by the type field.
type payload struct {
	Type    string          `json:"type"`
	Role    string          `json:"role"`
	Name    string          `json:"name"`
	CallID  string          `json:"call_id"`
	Args    string          `json:"arguments"`
	Input   string          `json:"input"`
	Content []contentItem   `json:"content"`
	Output  json.RawMessage `json:"output"`
	Message string          `json:"message"`
	Meta    *chatMeta       `json:"internal_chat_message_metadata_passthrough"`
}

type chatMeta struct {
	// ContentItemKinds names what produced a message. Codex writes several
	// user-role messages the person never typed — environment context, agent
	// instructions — and only "user.text" is a human turn.
	ContentItemKinds []string `json:"content_item_kinds"`
}

type contentItem struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// exitMarker heads a Codex shell result, on its own line above the command's
// output. It is a header, not a trailer: matching it anywhere in the text would
// read a line of the command's own output as the process's exit status.
const exitMarker = "Process exited with code "

// failedMarker opens a result for a call that never reached a process: a hook
// block, an apply_patch rejection, a syntax error in the exec wrapper. Codex
// writes no exit-code line for those, so the exit code alone reads them as
// successes. It is matched at the very start of the text, not at the start of
// any line: a command that prints it is quoting, not failing.
const failedMarker = "Script failed"

// Stream normalizes one rollout file.
func (a Adapter) Stream(s transcript.Session, f transcript.Filter, emit func(transcript.Event) error) (transcript.Counts, error) {
	st := &streamState{filter: f, emit: emit}

	file, err := os.Open(s.Path)
	if err != nil {
		return st.counts, err
	}
	defer file.Close()

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
			return st.counts, st.flush()
		}
		if readErr != nil {
			return st.counts, readErr
		}
	}
}

// streamState carries what one rollout's normalization needs across lines: the
// session identity from its header, and the legacy human turns held back until
// the file proves it records none the primary way.
type streamState struct {
	filter transcript.Filter
	emit   func(transcript.Event) error
	counts transcript.Counts

	sessionID    string
	threadID     string
	cwd          string
	historyStart int

	sawPrimaryTurn bool
	legacyTurns    []transcript.Event
}

func (st *streamState) normalize(line []byte) error {
	var r row
	if err := json.Unmarshal(line, &r); err != nil || r.Payload == nil {
		st.counts.Skipped++
		return nil
	}

	if r.Type == typeSessionMeta {
		st.readMeta(r.Payload)
		st.counts.Skipped++
		return nil
	}
	// Entries below the fork point are the parent's, replayed verbatim into
	// this child file. Counting them here would double every one of them.
	if st.threadID != "" && r.Ordinal < st.historyStart {
		st.counts.Skipped++
		return nil
	}
	if !inWindow(r.Timestamp, st.filter.Since) {
		st.counts.Skipped++
		return nil
	}

	var p payload
	if err := json.Unmarshal(r.Payload, &p); err != nil {
		st.counts.Skipped++
		return nil
	}

	base := transcript.Event{
		SessionID: st.sessionID,
		ThreadID:  st.threadID,
		Timestamp: r.Timestamp,
		Cwd:       st.cwd,
	}

	switch {
	case r.Type == typeResponseItem && p.Type == "message" && p.Role == "user":
		return st.humanTurn(p, base)
	case r.Type == typeResponseItem && (p.Type == "function_call" || p.Type == "custom_tool_call"):
		st.counts.Entries++
		return st.toolCall(p, base)
	case r.Type == typeResponseItem && strings.HasSuffix(p.Type, "_call_output"):
		st.counts.Entries++
		return st.toolResult(p, base)
	case r.Type == typeEventMsg && p.Type == "user_message":
		st.legacyTurn(p, base)
		return nil
	}
	st.counts.Skipped++
	return nil
}

func (st *streamState) readMeta(raw json.RawMessage) {
	var meta sessionMeta
	if err := json.Unmarshal(raw, &meta); err != nil {
		return
	}
	st.sessionID = meta.SessionID
	if st.sessionID == "" {
		st.sessionID = meta.ID
	}
	st.cwd = meta.Cwd
	if meta.ID != "" && meta.ID != st.sessionID {
		st.threadID = meta.ID
		st.historyStart = meta.SubagentHistoryStart
	}
}

func (st *streamState) humanTurn(p payload, base transcript.Event) error {
	if p.Meta == nil || !contains(p.Meta.ContentItemKinds, "user.text") {
		st.counts.Skipped++
		return nil
	}
	st.counts.Entries++
	st.sawPrimaryTurn = true
	base.Kind, base.Text = transcript.KindHumanTurn, itemText(p.Content)
	return st.emit(base)
}

// legacyTurn holds a user_message event, the human-turn source of rollouts
// written before Codex tagged messages with content item kinds. The two
// sources overlap when both are present, so these are emitted only if the file
// yields no primary turn at all.
func (st *streamState) legacyTurn(p payload, base transcript.Event) {
	base.Kind, base.Text = transcript.KindHumanTurn, p.Message
	st.legacyTurns = append(st.legacyTurns, base)
}

func (st *streamState) flush() error {
	if st.sawPrimaryTurn {
		st.counts.Skipped += len(st.legacyTurns)
		return nil
	}
	for _, e := range st.legacyTurns {
		st.counts.Entries++
		if err := st.emit(e); err != nil {
			return err
		}
	}
	return nil
}

func (st *streamState) toolCall(p payload, base transcript.Event) error {
	base.Kind, base.Tool, base.ToolUseID = transcript.KindToolCall, p.Name, p.CallID
	base.Target, base.Shell = callTarget(p)
	return st.emit(base)
}

func (st *streamState) toolResult(p payload, base transcript.Event) error {
	base.Kind, base.ToolUseID = transcript.KindToolResult, p.CallID
	base.Text = outputText(p.Output)
	base.IsError = resultFailed(base.Text)
	return st.emit(base)
}

// callTarget extracts what a tool call acted on, and whether it ran a shell
// command. Codex spells a shell call three ways: a function call taking a JSON
// argument string, a custom "exec" call whose input is JavaScript wrapping the
// same JSON object, and write_stdin against an already-running command.
func callTarget(p payload) (string, bool) {
	switch p.Name {
	case "exec_command", "write_stdin":
		return argField(p.Args), true
	case "exec":
		return argField(firstJSONObject(p.Input)), true
	case "apply_patch":
		return patchFile(p.Input), false
	}
	return argField(p.Args), false
}

// argField pulls the subject out of a tool's JSON arguments: the command for a
// shell call, the path for a file call.
func argField(raw string) string {
	var args struct {
		Cmd      string `json:"cmd"`
		Command  string `json:"command"`
		Path     string `json:"path"`
		FilePath string `json:"file_path"`
		Chars    string `json:"chars"`
	}
	if raw == "" || json.Unmarshal([]byte(raw), &args) != nil {
		return ""
	}
	for _, v := range []string{args.Cmd, args.Command, args.Path, args.FilePath, args.Chars} {
		if v != "" {
			return v
		}
	}
	return ""
}

// firstJSONObject returns the first complete JSON object embedded in s. The
// "exec" tool's input is a JavaScript snippet whose only object literal is the
// call's arguments.
func firstJSONObject(s string) string {
	i := strings.IndexByte(s, '{')
	if i < 0 {
		return ""
	}
	dec := json.NewDecoder(strings.NewReader(s[i:]))
	var raw json.RawMessage
	if err := dec.Decode(&raw); err != nil {
		return ""
	}
	return string(raw)
}

// patchFile returns the first file an apply_patch touches. A patch may span
// several files; the rest are provenance the guard runtime reads, not the
// clustering key.
func patchFile(input string) string {
	for _, line := range strings.Split(input, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "*** ") {
			continue
		}
		for _, verb := range []string{"Update File: ", "Add File: ", "Delete File: ", "Move to: "} {
			if rest, ok := strings.CutPrefix(trimmed[4:], verb); ok {
				return strings.TrimSpace(rest)
			}
		}
	}
	return ""
}

// outputText flattens a tool result, which is either a plain string or a list
// of content items.
func outputText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	var items []contentItem
	if err := json.Unmarshal(raw, &items); err != nil {
		return ""
	}
	return itemText(items)
}

func itemText(items []contentItem) string {
	var parts []string
	for _, item := range items {
		if item.Text != "" {
			parts = append(parts, item.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// resultFailed reads the headers Codex writes above a tool result: an exit code
// for a call that reached a process, and failedMarker for one that did not.
func resultFailed(text string) bool {
	if strings.HasPrefix(text, failedMarker) {
		return true
	}
	i := lineIndex(text, exitMarker)
	if i < 0 {
		return false
	}
	rest := text[i+len(exitMarker):]
	end := 0
	for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
		end++
	}
	code, err := strconv.Atoi(rest[:end])
	return err == nil && code != 0
}

// lineIndex finds prefix at the start of a line, so a match inside a command's
// output cannot stand in for the header Codex writes above it.
func lineIndex(text, prefix string) int {
	for offset := 0; offset < len(text); {
		if strings.HasPrefix(text[offset:], prefix) {
			return offset
		}
		next := strings.IndexByte(text[offset:], '\n')
		if next < 0 {
			return -1
		}
		offset += next + 1
	}
	return -1
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
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
