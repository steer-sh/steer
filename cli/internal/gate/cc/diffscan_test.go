package cc

import (
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/shellsyntax"
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

// matchedBehindWideBoundary reports a commit span whose `git` follows a byte the Python
// boundary did not accept (a quote, a backslash, a slash); only such a span may add a
// Go match. A span behind whitespace or an operator starts at that byte, not at `git`.
func matchedBehindWideBoundary(cmd string) bool {
	for _, span := range commitSpans(cmd) {
		if span.Start > 0 && strings.HasPrefix(cmd[span.Start:], "git") && !isSeparator(cmd[span.Start-1]) {
			return true
		}
	}
	return false
}

// exemptedByText reports a command the Python detector exempted for holding `--dry-run`
// anywhere; Go exempts only a parsed dry-run call, so it may add a match there.
func exemptedByText(cmd string) bool {
	return strings.Contains(cmd, "--dry-run")
}

// hiddenByShell reports a call whose commit only the shell's expansion can show, which
// Python did not look for; Go may add a match there.
func hiddenByShell(cmd string) bool {
	program, err := shellsyntax.Parse(cmd)
	return err == nil && slices.ContainsFunc(program.Calls, hiddenCommit)
}

func TestLooseCommitDetectorMatchesPython(t *testing.T) {
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
		got := IsCommitCommand(tc.Cmd)
		if got == tc.IsCommit || (got && (matchedBehindWideBoundary(tc.Cmd) || exemptedByText(tc.Cmd) || hiddenByShell(tc.Cmd))) {
			continue
		}
		t.Errorf("IsCommitCommand(%q) = %v, python %v", tc.Cmd, got, tc.IsCommit)
	}
	t.Logf("%d loose-detector cases replayed", len(cases))
}
