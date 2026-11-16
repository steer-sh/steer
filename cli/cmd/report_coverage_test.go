package cmd

import (
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/harness"
	"github.com/steer-sh/steer-cli/internal/report"
)

// Pi is unlisted: its coverage line appears only when the scan read a Pi file.
func TestCoverageLinesNameAnUnlistedHarnessOnlyWithEvidence(t *testing.T) {
	empty := []report.HarnessCoverage{{Harness: harness.ClaudeCode}, {Harness: harness.Codex}, {Harness: harness.Pi}}
	got := strings.Join(coverageLines(empty), "\n")
	if strings.Contains(got, "pi:") || !strings.Contains(got, "claude-code: 0 file(s)") || !strings.Contains(got, "codex: 0 file(s)") {
		t.Errorf("empty scan lines:\n%s", got)
	}
	withPi := []report.HarnessCoverage{{Harness: harness.Pi, FilesScanned: 2, SessionsScanned: 1}}
	if got := coverageLines(withPi); len(got) != 1 || got[0] != "pi: 2 file(s), 1 session(s), 0 thread(s)." {
		t.Errorf("Pi evidence lines = %q", got)
	}
}
