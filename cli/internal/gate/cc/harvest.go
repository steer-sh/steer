package cc

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/steer-sh/steer-cli/internal/gate"
	"github.com/steer-sh/steer-cli/internal/gate/resolve"
)

// Harvest returns every real ledger under a ~/.claude/projects tree. A sidechain's
// text never reached the operator, so it is not a report to them whatever it contains.
func Harvest(projectsDir string) resolve.Harvest {
	return func() []resolve.LedgerMsg {
		var out []resolve.LedgerMsg
		matches, _ := filepath.Glob(filepath.Join(projectsDir, "*", "*.jsonl"))
		for _, p := range matches {
			out = append(out, harvestFile(p)...)
		}
		resolve.SortLedgers(out)
		return out
	}
}

func harvestFile(path string) []resolve.LedgerMsg {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []resolve.LedgerMsg
	for _, line := range strings.Split(string(data), "\n") {
		// Cheap prefilter: skip the JSON parse for the rows that cannot match.
		if !strings.Contains(line, gate.LedgerHeader) {
			continue
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
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			continue
		}
		if row.Type != "assistant" || row.IsSidechain {
			continue
		}
		when := isoEpoch(row.Timestamp)
		if when == nil {
			continue
		}
		for _, text := range textBlocks(row.Message.Content) {
			if strings.Contains(text, gate.LedgerHeader) && gate.HasLedgerLine(text) {
				out = append(out, resolve.LedgerMsg{
					When: *when, Key: row.SessionID, Path: path, Text: text})
			}
		}
	}
	return out
}
