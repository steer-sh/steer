package cmd

import (
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/gate"
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
	// The write proves no reviewer answers, so the ready box names the command that does.
	if !strings.Contains(out, "steer commit-gate test-reviewer") {
		t.Errorf("output = %q; want the pointer to the reviewer test", out)
	}
}

// TestTrialFailureSaysCommitsGoUnreviewed: a reviewer that did not answer fails the
// command and says what that costs, with the outcome that names the cause.
func TestTrialFailureSaysCommitsGoUnreviewed(t *testing.T) {
	cfg := gate.Defaults()
	out, ok := renderTrial(gate.TrialResult{Run: gate.ReviewRun{
		Outcome: gate.OutcomeExit, Detail: "rc=1 not logged in"}}, cfg)
	if ok {
		t.Fatal("ok = true for a reviewer that did not answer")
	}
	for _, want := range []string{"did not answer", "exit: rc=1 not logged in", "unreviewed"} {
		if !strings.Contains(out, want) {
			t.Errorf("output = %q; missing %q", out, want)
		}
	}
	out, ok = renderTrial(gate.TrialResult{Run: gate.ReviewRun{Text: "NO FINDINGS", Outcome: gate.OutcomeOK}}, cfg)
	if !ok || !strings.Contains(out, "no findings") {
		t.Errorf("ok = %v, output = %q; want an answer with no findings", ok, out)
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
