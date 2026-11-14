package jsonl

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEveryAppendingWriterSeparatesATornRow requires each production Go file to call
// Separated at least once per file it opens for append. The e2e directory holds test
// tooling, not a Steer log.
//
// pins existing behaviour
func TestEveryAppendingWriterSeparatesATornRow(t *testing.T) {
	root := filepath.Join("..", "..")
	var unseparated []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "e2e" || d.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		src := string(data)
		if strings.Count(src, "O_APPEND") > strings.Count(src, "jsonl.Separated(") {
			rel, _ := filepath.Rel(root, path)
			unseparated = append(unseparated, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(unseparated) > 0 {
		t.Errorf("these files append without jsonl.Separated, so a torn row swallows the next one: %v", unseparated)
	}
}
