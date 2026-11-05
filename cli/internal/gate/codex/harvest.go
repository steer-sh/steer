package codex

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/steer-sh/steer-cli/internal/gate"
	"github.com/steer-sh/steer-cli/internal/gate/resolve"
	"github.com/steer-sh/steer-cli/internal/harness"
)

// SessionsRoot is the rollout tree under the Codex config directory.
func SessionsRoot() (string, error) {
	entry, ok := harness.Get(harness.Codex)
	if !ok {
		return "", os.ErrNotExist
	}
	dir, err := entry.ConfigDir(harness.GlobalScope, "")
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "sessions"), nil
}

// Harvest returns every real ledger in the rollouts under a sessions tree.
func Harvest(sessionsRoot string) resolve.Harvest {
	return func() []resolve.LedgerMsg {
		var out []resolve.LedgerMsg
		_ = filepath.WalkDir(sessionsRoot, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil // an unreadable subtree narrows the scan, never ends it
			}
			if !d.IsDir() && strings.HasSuffix(d.Name(), ".jsonl") {
				// An unreadable file or line narrows the harvest; what was read still counts.
				rollout, _ := ReadRollout(path)
				out = append(out, rollout.Ledgers...)
			}
			return nil
		})
		resolve.SortLedgers(out)
		return out
	}
}

// rolloutRow is the rollout envelope this harvest reads.
type rolloutRow struct {
	Type      string          `json:"type"`
	Timestamp string          `json:"timestamp"`
	Ordinal   int             `json:"ordinal"`
	Payload   json.RawMessage `json:"payload"`
}

type rolloutMeta struct {
	SessionID            string `json:"session_id"`
	ID                   string `json:"id"`
	SubagentHistoryStart int    `json:"subagent_history_start_ordinal"`
}

type rolloutMessage struct {
	Type    string `json:"type"`
	Role    string `json:"role"`
	Phase   string `json:"phase"`
	Channel string `json:"channel"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

// replyItem reports whether a content item of an assistant message is reply text:
// output_text, or the untyped and plain text items of older rollouts.
func replyItem(itemType string) bool {
	return itemType == "output_text" || itemType == "text" || itemType == ""
}

// hiddenChannel reports whether a message's phase or channel marks model reasoning,
// which the operator never saw as a reply.
func hiddenChannel(m rolloutMessage) bool {
	for _, v := range []string{m.Phase, m.Channel} {
		if v == "analysis" || v == "reasoning" {
			return true
		}
	}
	return false
}

// ErrUnreadableLine reports a rollout line that does not parse, such as a line Codex had
// not finished writing.
var ErrUnreadableLine = errors.New("a rollout line does not parse")

// ErrUndatedLedger reports a ledger message whose timestamp does not parse, which the
// resolver cannot place in time.
var ErrUndatedLedger = errors.New("a ledger message carries no readable timestamp")

// Rollout is what one rollout file holds for the ledger readers.
type Rollout struct {
	// Key is the thread the file belongs to: its own id on a subagent rollout, the
	// session id on a root one. Empty when the file carries no session_meta.
	Key string
	// Threaded says the file is a subagent rollout.
	Threaded bool
	// Ledgers are the ledgers in the thread's own assistant text, one per text item.
	Ledgers []resolve.LedgerMsg
}

// scan carries one rollout's identity across its lines: the thread the file belongs
// to, and where this file's own entries begin.
type scan struct {
	path string
	// key is the thread's own id on a subagent rollout, the session id on a root one.
	key string
	// historyStart is the first ordinal belonging to this thread, and threaded says
	// the file has one at all.
	historyStart int
	threaded     bool
	out          []resolve.LedgerMsg
	// err is the first line that did not parse, or the first undated ledger.
	err error
}

// ReadRollout reads one rollout. The error reports a file it could not read or a ledger
// line it could not parse; what was read before or around it is still returned.
func ReadRollout(path string) (Rollout, error) {
	file, err := os.Open(path)
	if err != nil {
		return Rollout{}, err
	}
	defer file.Close()

	s := &scan{path: path}
	reader := bufio.NewReaderSize(file, 256*1024)
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 {
			s.read(line)
		}
		if readErr != nil {
			out := Rollout{Key: s.key, Threaded: s.threaded, Ledgers: s.out}
			if readErr != io.EOF {
				return out, readErr
			}
			return out, s.err
		}
	}
}

func (s *scan) read(line []byte) {
	var row rolloutRow
	if err := json.Unmarshal(line, &row); err != nil || row.Payload == nil {
		if err != nil && s.err == nil && strings.TrimSpace(string(line)) != "" {
			s.err = ErrUnreadableLine
		}
		return
	}
	if row.Type == "session_meta" {
		s.readMeta(row.Payload)
		return
	}
	// A subagent rollout replays its parent's history verbatim below the fork point.
	// Harvesting those lines would offer the parent's ledgers under the thread's key.
	if s.threaded && row.Ordinal < s.historyStart {
		return
	}
	// Cheap prefilter: skip the payload parse for the lines that cannot match.
	if row.Type != "response_item" || !strings.Contains(string(line), gate.LedgerHeader) {
		return
	}
	var message rolloutMessage
	if err := json.Unmarshal(row.Payload, &message); err != nil ||
		message.Type != "message" || message.Role != "assistant" || hiddenChannel(message) {
		return
	}
	var texts []string
	for _, item := range message.Content {
		if replyItem(item.Type) && strings.Contains(item.Text, gate.LedgerHeader) && gate.HasLedgerLine(item.Text) {
			texts = append(texts, item.Text)
		}
	}
	when := epoch(row.Timestamp)
	if when == nil {
		if len(texts) > 0 && s.err == nil {
			s.err = ErrUndatedLedger
		}
		return
	}
	for _, text := range texts {
		s.out = append(s.out, resolve.LedgerMsg{When: *when, Key: s.key, Path: s.path, Text: text})
	}
}

func (s *scan) readMeta(raw json.RawMessage) {
	var meta rolloutMeta
	if err := json.Unmarshal(raw, &meta); err != nil {
		return
	}
	s.key = meta.SessionID
	if s.key == "" {
		s.key = meta.ID
	}
	if meta.ID != "" && meta.ID != s.key {
		s.key = meta.ID
		s.historyStart = meta.SubagentHistoryStart
		s.threaded = true
	}
}
