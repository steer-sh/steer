package pi

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/steer-sh/steer-cli/internal/gate"
	"github.com/steer-sh/steer-cli/internal/gate/cc"
	"github.com/steer-sh/steer-cli/internal/gate/resolve"
	transcriptpi "github.com/steer-sh/steer-cli/internal/transcript/pi"
)

// Harvest returns every assistant-authored ledger under a Pi sessions tree.
func Harvest(sessionsRoot string) resolve.Harvest {
	return func() []resolve.LedgerMsg {
		var out []resolve.LedgerMsg
		_ = filepath.WalkDir(sessionsRoot, func(path string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() && strings.HasSuffix(d.Name(), ".jsonl") {
				out = append(out, harvestFile(path)...)
			}
			return nil
		})
		resolve.SortLedgers(out)
		return out
	}
}

// harvestFile reads one session file for the resolver, which takes every ledger the
// read reached even when part of the file could not be read.
func harvestFile(path string) []resolve.LedgerMsg {
	_, msgs, _ := ReadLedgers(path)
	return msgs
}

// ReadLedgers returns a Pi session file's header session ID and the ledgers in the
// assistant text of its active branch, one per text block, leaving out entries a
// /fork copied in (both selections are the transcript adapter's). The error reports a
// file it could not read, a ledger line it could not parse or date, or a last line
// that does not parse; the ledgers read around it are still returned.
func ReadLedgers(path string) (string, []resolve.LedgerMsg, error) {
	active, err := transcriptpi.ActiveBranch(path)
	if err != nil {
		return "", nil, err
	}
	replayed := transcriptpi.ReplayedIDs(path)

	file, err := os.Open(path)
	if err != nil {
		return "", nil, err
	}
	defer file.Close()

	sessionID := ""
	var out []resolve.LedgerMsg
	var lineErr error
	var last []byte
	// A capped scanner drops the rest of the file at the first oversized row, and a
	// Pi tool result is not bounded. Read with the reader every other harvest uses.
	reader := bufio.NewReaderSize(file, 256*1024)
	for {
		line, readErr := reader.ReadBytes('\n')
		if trimmed := bytes.TrimSpace(line); len(trimmed) > 0 {
			last = trimmed
		}
		if sessionID == "" && len(line) > 0 {
			var header struct {
				Type string `json:"type"`
				ID   string `json:"id"`
			}
			if json.Unmarshal(line, &header) == nil && header.Type == "session" {
				sessionID = header.ID
			}
		} else if len(line) > 0 {
			msgs, err := ledgersInLine(line, sessionID, path, active, replayed)
			out = append(out, msgs...)
			if err != nil && lineErr == nil {
				lineErr = err
			}
		}
		if readErr != nil {
			if readErr != io.EOF {
				return sessionID, out, readErr
			}
			if len(last) > 0 && !json.Valid(last) && lineErr == nil {
				lineErr = cc.ErrUnreadableLine
			}
			return sessionID, out, lineErr
		}
	}
}

// ledgersInLine returns the ledgers in one session entry: an assistant message on the
// active branch that no /fork copied in.
func ledgersInLine(line []byte, sessionID, path string, active, replayed map[string]struct{}) ([]resolve.LedgerMsg, error) {
	if !strings.Contains(string(line), gate.LedgerHeader) {
		return nil, nil
	}
	var entry struct {
		Type      string `json:"type"`
		ID        string `json:"id"`
		Timestamp string `json:"timestamp"`
		Message   struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &entry) != nil {
		return nil, cc.ErrUnreadableLine
	}
	if entry.Type != "message" || entry.Message.Role != "assistant" {
		return nil, nil
	}
	if active != nil {
		if _, ok := active[entry.ID]; !ok {
			return nil, nil
		}
	}
	if _, ok := replayed[entry.ID]; ok {
		return nil, nil
	}
	var texts []string
	for _, text := range messageText(entry.Message.Content) {
		if strings.Contains(text, gate.LedgerHeader) && gate.HasLedgerLine(text) {
			texts = append(texts, text)
		}
	}
	if len(texts) == 0 {
		return nil, nil
	}
	when, err := time.Parse(time.RFC3339Nano, entry.Timestamp)
	if err != nil {
		return nil, cc.ErrUndatedLedger
	}
	out := make([]resolve.LedgerMsg, 0, len(texts))
	for _, text := range texts {
		out = append(out, resolve.LedgerMsg{
			When: float64(when.UnixNano()) / 1e9, Key: sessionID, Path: path, Text: text,
		})
	}
	return out, nil
}

func messageText(raw json.RawMessage) []string {
	var bare string
	if json.Unmarshal(raw, &bare) == nil {
		return []string{bare}
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return nil
	}
	var out []string
	for _, block := range blocks {
		if block.Type == "text" {
			out = append(out, block.Text)
		}
	}
	return out
}
