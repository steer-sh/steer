package codex

import (
	"bufio"
	"encoding/json"
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
				out = append(out, harvestRollout(path)...)
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
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
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
}

func harvestRollout(path string) []resolve.LedgerMsg {
	file, err := os.Open(path)
	if err != nil {
		return nil
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
			return s.out
		}
	}
}

func (s *scan) read(line []byte) {
	var row rolloutRow
	if err := json.Unmarshal(line, &row); err != nil || row.Payload == nil {
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
		message.Type != "message" || message.Role != "assistant" {
		return
	}
	when := epoch(row.Timestamp)
	if when == nil {
		return
	}
	for _, item := range message.Content {
		if !strings.Contains(item.Text, gate.LedgerHeader) || !gate.HasLedgerLine(item.Text) {
			continue
		}
		s.out = append(s.out, resolve.LedgerMsg{
			When: *when, Key: s.key, Path: s.path, Text: item.Text})
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
