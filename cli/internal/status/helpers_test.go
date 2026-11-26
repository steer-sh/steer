package status

import (
	"os"
	"path/filepath"
	"testing"
)

// The firing count telemetry reports counts blocks: a sleeping guard's pass is
// left out, and a line it cannot read still counts, as every line did before.
func TestCountBlockRowsLeavesOutASleepingGuardsPasses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "steer-audit.jsonl")
	rows := `{"hook_name":"g","enforcement":"block"}` + "\n" +
		`{"hook_name":"g","enforcement":"slept"}` + "\n" +
		`{"hook_name":"g"}` + "\n" + "not json\n\n"
	if err := os.WriteFile(path, []byte(rows), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := countBlockRows(path); got != 3 {
		t.Errorf("countBlockRows = %d, want 3", got)
	}
}
