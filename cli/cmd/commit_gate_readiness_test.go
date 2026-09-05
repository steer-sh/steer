package cmd

import (
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/status"
)

// The old install printed a success box on the registration write alone. A box that
// says "armed" above an open step is the failure this rendering exists to end: the
// headline must lead with what remains.
func TestInstallReadinessLeadsWithTheOpenSteps(t *testing.T) {
	steps := []status.Step{{Title: "the reviewer CLI does not resolve", Fix: "install agy"}}
	out := renderInstallReadiness(steps, "Registered: the Codex commit gate on PreToolUse")
	head := strings.TrimSpace(strings.Split(strings.TrimSpace(stripBox(out)), "\n")[0])
	if !strings.Contains(head, "Not reviewing yet") {
		t.Errorf("headline = %q; want the open steps first", head)
	}
	for _, want := range []string{"the reviewer CLI does not resolve", "run: install agy", "Registered:"} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q", want)
		}
	}
}

func TestInstallReadinessReportsReadyOnlyWithNoSteps(t *testing.T) {
	out := renderInstallReadiness(nil, "Registered: the Claude Code commit gate on PreToolUse")
	if !strings.Contains(out, "Ready") || strings.Contains(out, "step(s) remain") {
		t.Errorf("output = %q; want the ready headline", out)
	}
}

// stripBox drops the border lipgloss draws, so a test reads the first line of the
// message rather than the top edge of the frame.
func stripBox(out string) string {
	var kept []string
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.Trim(line, " │╭╮╰╯─")
		if strings.TrimSpace(trimmed) != "" {
			kept = append(kept, trimmed)
		}
	}
	return strings.Join(kept, "\n")
}
