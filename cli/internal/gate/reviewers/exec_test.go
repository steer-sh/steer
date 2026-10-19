package reviewers

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/steer-sh/steer-cli/internal/gate"
)

// Codex writes its banner and the echoed prompt to stderr before its error, so a
// failed run's detail must keep the error a head cut would drop.
func TestFailedRunDetailKeepsTheErrorAfterTheEcho(t *testing.T) {
	script := `for i in $(seq 1 60); do echo "user: echoed prompt line $i"; done >&2
echo "ERROR: The 'no-such-model' model is not supported." >&2
exit 1`
	_, _, raw, failed := runProcess(context.Background(), t.TempDir(), []string{"sh", "-c", script}, "")
	if !failed || raw.Outcome != gate.OutcomeExit {
		t.Fatalf("run = %#v, failed = %v", raw, failed)
	}
	if !strings.HasPrefix(raw.Detail, "rc=1 …") || !strings.HasSuffix(raw.Detail, "ERROR: The 'no-such-model' model is not supported.") {
		t.Fatalf("detail = %q", raw.Detail)
	}
	row := gate.ReviewerReport(gate.Defaults(), []gate.ReviewRun{gate.Classify(raw)})
	errs, _ := row["reviewer_errors"].([]string)
	if len(errs) != 1 || !strings.Contains(errs[0], "not supported") {
		t.Fatalf("the ledger detail lost the error: %q", errs)
	}
}

// The cut drops only a partial first line: a window that opens on a line start
// keeps that line, and one long line keeps its end.
func TestStderrTailCutsOnlyAPartialLine(t *testing.T) {
	whole := strings.Repeat("x", stderrTailChars-6) + "\nshort"
	if got := stderrTail("banner\n" + whole); got != "…"+whole {
		t.Errorf("a window opening on a line start = %q", got)
	}
	long := strings.Repeat("é", 200) + "ERROR: end"
	got := stderrTail(long)
	if !strings.HasSuffix(got, "ERROR: end") || !utf8.ValidString(got) {
		t.Errorf("one long line = %q", got)
	}
}

// Claude Code answers a missing login on stdout as a JSON reply and exits 1 with an
// empty stderr, so the failed run's detail must carry the reply's result text.
func TestClaudeCodeExitDetailNamesTheReplyResult(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "claude")
	script := "#!/bin/sh\ncat >/dev/null\necho '{\"is_error\":true,\"subtype\":\"success\",\"result\":\"Not logged in · Please run /login\"}'\nexit 1\n"
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	reviewer := NewClaudeCode("", "")
	reviewer.Binary = binary
	reviewer.UserSettings = filepath.Join(t.TempDir(), "absent.json")
	raw := reviewer.Review(context.Background(), "PROMPT", t.TempDir())
	if raw.Outcome != gate.OutcomeExit || raw.Detail != "rc=1 Not logged in · Please run /login" {
		t.Fatalf("run = %#v", raw)
	}
	line := gate.ReviewerReport(gate.Defaults(), []gate.ReviewRun{gate.Classify(raw)})
	errs, _ := line["reviewer_errors"].([]string)
	if len(errs) != 1 || !strings.Contains(errs[0], "Not logged in") {
		t.Fatalf("the ledger detail lost the login error: %q", errs)
	}
}
