package pi

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/steer-sh/steer-cli/internal/gate"
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

// harvestFile reads one session file. A Pi session file is a tree, so a ledger
// written on an abandoned branch is not a report the operator ever read, and a
// ledger copied in by a /fork belongs to the session it was written in. Both
// selections come from the transcript adapter, so the resolver and the session
// corpus read one definition of which entries are live.
func harvestFile(path string) []resolve.LedgerMsg {
	active, err := transcriptpi.ActiveBranch(path)
	if err != nil {
		return nil
	}
	replayed := transcriptpi.ReplayedIDs(path)

	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()

	sessionID := ""
	var out []resolve.LedgerMsg
	scanner := bufio.NewScanner(file)
	buffer := make([]byte, 64*1024)
	scanner.Buffer(buffer, 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if sessionID == "" {
			var header struct {
				Type string `json:"type"`
				ID   string `json:"id"`
			}
			if json.Unmarshal(line, &header) == nil && header.Type == "session" {
				sessionID = header.ID
			}
			continue
		}
		if !strings.Contains(string(line), gate.LedgerHeader) {
			continue
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
		if json.Unmarshal(line, &entry) != nil || entry.Type != "message" ||
			entry.Message.Role != "assistant" {
			continue
		}
		if active != nil {
			if _, ok := active[entry.ID]; !ok {
				continue
			}
		}
		if _, ok := replayed[entry.ID]; ok {
			continue
		}
		when, err := time.Parse(time.RFC3339Nano, entry.Timestamp)
		if err != nil {
			continue
		}
		for _, text := range messageText(entry.Message.Content) {
			if strings.Contains(text, gate.LedgerHeader) && gate.HasLedgerLine(text) {
				out = append(out, resolve.LedgerMsg{
					When: float64(when.UnixNano()) / 1e9, Key: sessionID, Path: path, Text: text,
				})
			}
		}
	}
	return out
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
