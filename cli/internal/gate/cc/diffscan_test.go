package cc

import (
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// A differential replay of the hand-rolled scanners against the Python originals, whose
// answers are recorded in testdata. COMMIT_GATE_SCAN_CORPUS points the test at a freshly
// generated corpus instead.
type scanCase struct {
	Cmd      string `json:"cmd"`
	IsCommit bool   `json:"is_commit"`
	RepoDir  string `json:"repo_dir"`
	Sources  []bool `json:"sources"`
	Writes   bool   `json:"writes"`
	Message  string `json:"message"`
	Span     []int  `json:"span"`
}

func TestScannersMatchPython(t *testing.T) {
	var raw []byte
	if path := os.Getenv("COMMIT_GATE_SCAN_CORPUS"); path != "" {
		var err error
		if raw, err = os.ReadFile(path); err != nil {
			t.Fatal(err)
		}
	} else {
		f, err := os.Open(filepath.Join("testdata", "scanner_parity.json.gz"))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		zr, err := gzip.NewReader(f)
		if err != nil {
			t.Fatal(err)
		}
		defer zr.Close()
		if raw, err = io.ReadAll(zr); err != nil {
			t.Fatal(err)
		}
	}
	var cases []scanCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		if got := IsCommitCommand(tc.Cmd); got != tc.IsCommit {
			t.Errorf("IsCommitCommand(%q) = %v, python %v", tc.Cmd, got, tc.IsCommit)
		}
		if got := RepoDir(tc.Cmd, "/cwd"); got != tc.RepoDir {
			t.Errorf("RepoDir(%q) = %q, python %q", tc.Cmd, got, tc.RepoDir)
		}
		staged, untracked := DiffSources(tc.Cmd)
		if staged != tc.Sources[0] || untracked != tc.Sources[1] {
			t.Errorf("DiffSources(%q) = (%v,%v), python (%v,%v)", tc.Cmd, staged, untracked, tc.Sources[0], tc.Sources[1])
		}
		if got := WritesAFile(tc.Cmd); got != tc.Writes {
			t.Errorf("WritesAFile(%q) = %v, python %v", tc.Cmd, got, tc.Writes)
		}
		if got := ExtractMessage(tc.Cmd, "/nonexistent-dir"); got != tc.Message {
			t.Errorf("ExtractMessage(%q) = %q, python %q", tc.Cmd, got, tc.Message)
		}
		span := CommitSpan(tc.Cmd)
		if span.Start != tc.Span[0] || span.End != tc.Span[1] {
			t.Errorf("CommitSpan(%q) = (%d,%d), python (%d,%d)", tc.Cmd, span.Start, span.End, tc.Span[0], tc.Span[1])
		}
	}
	t.Logf("%d differential cases replayed", len(cases))
}
