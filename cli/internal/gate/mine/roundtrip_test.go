package mine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestInterventionsRoundTrip pins the byte-identity the tracked interventions.jsonl
// depends on: parse every live line, re-emit it through the Go writer, and assert the
// bytes are unchanged. A reformat here is a whole-file diff on the next repo sync.
func TestInterventionsRoundTrip(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	path := filepath.Join(home, ".claude", "state", "commit-review", "interventions.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("no live interventions.jsonl: %v", err)
	}
	for i, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		dec := json.NewDecoder(strings.NewReader(line))
		dec.UseNumber()
		var rec map[string]any
		if err := dec.Decode(&rec); err != nil {
			t.Fatalf("line %d: parse: %v", i+1, err)
		}
		if got := pyDumps(rec, 0); got != line {
			t.Errorf("line %d not byte-identical:\n python: %s\n     go: %s", i+1, line, got)
		}
	}
}
