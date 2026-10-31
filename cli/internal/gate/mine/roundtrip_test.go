package mine

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/statestore"
)

// TestInterventionsRoundTrip pins the byte-identity the tracked interventions.jsonl
// depends on: parse every live line, re-emit it through the Go writer, and assert the
// bytes are unchanged. A reformat here is a whole-file diff on the next repo sync.
// It reads through statestore.Resolve, the one resolver, so a migrated machine is
// checked over the corpus its writers actually append to rather than a legacy copy.
func TestInterventionsRoundTrip(t *testing.T) {
	path := InterventionsPath(statestore.Resolve("commit-review"))
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
