package cmd

import (
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/gate/reviewers"
	"github.com/steer-sh/steer-cli/internal/status"
)

// A fix prints whole on one line however narrow the terminal is, because a line break
// inside a command breaks the copy.
func TestRenderHealthKeepsAFixOnOneLine(t *testing.T) {
	fix := "/Users/jeremy/.codex/plugins/cache/steer-marketplace/steer/0.1.4/scripts/steer bin install"
	out := renderHealth(status.Health{Verdict: status.VerdictAttention, Headline: "steer dev — needs attention",
		Issues: []status.Issue{{Severity: status.SeverityAttention, Title: "Hooks are behind the Codex plugin", Fix: fix}}}, 40)
	if !strings.Contains(out, "  "+fix+"\n") {
		t.Errorf("the fix was broken up:\n%s", out)
	}
}

// A healthy view is the verdict, what it covers and a pointer, with no issue heading.
func TestRenderHealthyView(t *testing.T) {
	out := renderHealth(status.Health{Verdict: status.VerdictHealthy, Headline: "steer 0.1.4 — healthy",
		Ready: []string{"Commit review is ready."}}, 80)
	for _, want := range []string{"steer 0.1.4 — healthy", "Commit review is ready.", "Details: steer dashboard"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}

// The text view leaves the loop's other sections to the dashboard and to --json.
func TestStatusPrintsTheVerdictAndNothingElse(t *testing.T) {
	s := &status.SystemStatus{Posture: status.Posture{
		CommitGate: []status.ScopeArming{{Harness: "claude-code", Scope: "global"}},
		Masters:    []status.MasterRuntime{{Harness: "claude-code", Scope: "global", State: status.MasterCurrent}},
	}, Summary: status.VerdictSummary{Total: 2}}
	out := captureStdout(t, func() { renderStatus(s, reviewers.Probe{}) })
	for _, want := range []string{"needs attention", "steer dashboard"} {
		if !strings.Contains(out, want) {
			t.Errorf("status is missing %q:\n%s", want, out)
		}
	}
	for _, gone := range []string{"Arming", "Guard runner", "Problems", "Plugin copies", "Trust unverified", "recorded problems\n"} {
		if strings.Contains(out, gone) {
			t.Errorf("status still prints %q:\n%s", gone, out)
		}
	}
}
