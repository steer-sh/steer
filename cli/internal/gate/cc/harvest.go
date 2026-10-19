package cc

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/steer-sh/steer-cli/internal/gate"
	"github.com/steer-sh/steer-cli/internal/gate/resolve"
)

// ErrUnreadableLine reports a transcript line that does not parse: a line naming the
// ledger header, or a last line the harness had not finished writing.
var ErrUnreadableLine = errors.New("a transcript line does not parse")

// ErrUndatedLedger reports a ledger row whose timestamp does not parse, which the
// resolver cannot place in time.
var ErrUndatedLedger = errors.New("a ledger row carries no readable timestamp")

// Harvest returns every real ledger under a ~/.claude/projects tree. A sidechain's
// text never reached the operator, so it is not a report to them whatever it contains.
func Harvest(projectsDir string) resolve.Harvest {
	return func() []resolve.LedgerMsg {
		var out []resolve.LedgerMsg
		matches, _ := filepath.Glob(filepath.Join(projectsDir, "*", "*.jsonl"))
		for _, p := range matches {
			// An unreadable file or line narrows the harvest; what was read still counts.
			msgs, _ := ReadLedgers(p)
			out = append(out, msgs...)
		}
		resolve.SortLedgers(out)
		return out
	}
}

// ReadLedgers returns the ledgers in one transcript's main-thread assistant text, one
// per text block. The error reports a file it could not read, a ledger line it could
// not parse or date, or a last line that does not parse; the ledgers read before or
// around it are still returned.
func ReadLedgers(path string) ([]resolve.LedgerMsg, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var out []resolve.LedgerMsg
	var lineErr error
	var last []byte
	reader := bufio.NewReaderSize(file, 256*1024)
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 {
			msgs, err := ledgersInLine(path, line)
			out = append(out, msgs...)
			if err != nil && lineErr == nil {
				lineErr = err
			}
		}
		if trimmed := bytes.TrimSpace(line); len(trimmed) > 0 {
			last = trimmed
		}
		if readErr != nil {
			if readErr != io.EOF {
				return out, readErr
			}
			if len(last) > 0 && !json.Valid(last) && lineErr == nil {
				lineErr = ErrUnreadableLine
			}
			return out, lineErr
		}
	}
}

func ledgersInLine(path string, line []byte) ([]resolve.LedgerMsg, error) {
	// Cheap prefilter: skip the JSON parse for the rows that cannot match.
	if !strings.Contains(string(line), gate.LedgerHeader) {
		return nil, nil
	}
	var row struct {
		Type        string `json:"type"`
		IsSidechain bool   `json:"isSidechain"`
		SessionID   string `json:"sessionId"`
		Timestamp   string `json:"timestamp"`
		Message     struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(line, &row); err != nil {
		return nil, ErrUnreadableLine
	}
	if row.Type != "assistant" || row.IsSidechain {
		return nil, nil
	}
	var texts []string
	for _, text := range textBlocks(row.Message.Content) {
		if strings.Contains(text, gate.LedgerHeader) && gate.HasLedgerLine(text) {
			texts = append(texts, text)
		}
	}
	when := isoEpoch(row.Timestamp)
	if when == nil {
		if len(texts) > 0 {
			return nil, ErrUndatedLedger
		}
		return nil, nil
	}
	out := make([]resolve.LedgerMsg, 0, len(texts))
	for _, text := range texts {
		out = append(out, resolve.LedgerMsg{When: *when, Key: row.SessionID, Path: path, Text: text})
	}
	return out, nil
}
