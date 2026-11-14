package checks

import (
	"os"
	"path/filepath"
	"testing"
)

// A stream that ends in half a line, as a crash mid-append leaves it, must still
// read the next appended line.
func TestAppendJSONLAfterATornLineIsRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runs.jsonl")
	if err := os.WriteFile(path, []byte(`{"run_id":"to`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := appendJSONL(path, map[string]string{"run_id": "whole"}); err != nil {
		t.Fatal(err)
	}
	var whole int
	if err := readJSONL(path, func(line []byte) {
		if string(line) == `{"run_id":"whole"}` {
			whole++
		}
	}); err != nil || whole != 1 {
		data, _ := os.ReadFile(path)
		t.Errorf("read the appended line %d times (%v):\n%s", whole, err, data)
	}
}
