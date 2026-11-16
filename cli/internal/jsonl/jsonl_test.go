package jsonl

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSeparatedStartsANewLineOnlyAfterATornRow(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name, content string
		exists        bool
		want          string
	}{
		{"absent", "", false, "{}\n"},
		{"empty", "", true, "{}\n"},
		{"whole rows", "{\"a\":1}\n", true, "{}\n"},
		{"torn row", "{\"a\":1}\n{\"b\":", true, "\n{}\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, tc.name+".jsonl")
			if tc.exists {
				if err := os.WriteFile(path, []byte(tc.content), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if got := string(Separated(path, []byte("{}\n"))); got != tc.want {
				t.Errorf("Separated = %q, want %q", got, tc.want)
			}
		})
	}
}
