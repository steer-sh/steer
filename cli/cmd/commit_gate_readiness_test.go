package cmd

import (
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/gate"
	"github.com/steer-sh/steer-cli/internal/gate/reviewers"
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

// TestTrialFailureOfAnAPIReviewerNamesItsSettings: an API reviewer has no CLI to
// sign in, so its hint names the base URL, the model and the key source.
func TestTrialFailureOfAnAPIReviewerNamesItsSettings(t *testing.T) {
	cfg := gate.Defaults()
	cfg.ReviewerProvider = reviewers.APIProviderID
	out, _ := renderTrial(gate.TrialResult{Run: gate.ReviewRun{
		Outcome: gate.OutcomeExit, Detail: "HTTP 404"}}, cfg)
	flat := strings.Join(strings.Fields(out), " ")
	for _, want := range []string{"reviewer.api.base_url", "steer reviewer key status"} {
		if !strings.Contains(flat, want) {
			t.Errorf("output = %q; missing %q", flat, want)
		}
	}
	if strings.Contains(flat, "signed in") {
		t.Errorf("output = %q; names a CLI sign-in", flat)
	}
}

// TestTrialRateLimitOfAnAPIReviewerSaysToWait: a rate limit is not a setting to
// check, so its hint names waiting or another model.
func TestTrialRateLimitOfAnAPIReviewerSaysToWait(t *testing.T) {
	cfg := gate.Defaults()
	cfg.ReviewerProvider = reviewers.APIProviderID
	out, _ := renderTrial(gate.TrialResult{Run: gate.ReviewRun{
		Outcome: gate.OutcomeRateLimited, Detail: "HTTP 429"}}, cfg)
	flat := strings.Join(strings.Fields(out), " ")
	if !strings.Contains(flat, "another model") || strings.Contains(flat, "base_url") {
		t.Errorf("output = %q; want the rate-limit hint", flat)
	}
}

// TestTrialUnreachableAPIReviewerNamesTheServer: no reply at all points at the URL
// and the server, not at the key.
func TestTrialUnreachableAPIReviewerNamesTheServer(t *testing.T) {
	cfg := gate.Defaults()
	cfg.ReviewerProvider = reviewers.APIProviderID
	out, _ := renderTrial(gate.TrialResult{Run: gate.ReviewRun{
		Outcome: gate.OutcomeUnreachable, Detail: "request failed: connection refused"}}, cfg)
	flat := strings.Join(strings.Fields(out), " ")
	if !strings.Contains(flat, "server runs") || strings.Contains(flat, "key status") {
		t.Errorf("output = %q; want the unreachable hint", flat)
	}
}

// TestTrialFailureInCodexSandboxNamesTheNetwork: a trial run from a Codex sandbox
// with the network off names that sandbox beside the sign-in and model causes.
func TestTrialFailureInCodexSandboxNamesTheNetwork(t *testing.T) {
	t.Setenv(codexNoNetworkEnv, "1")
	out, ok := renderTrial(gate.TrialResult{Run: gate.ReviewRun{
		Outcome: gate.OutcomeExit, Detail: "rc=1 workspace routing discovery failed"}}, gate.Defaults())
	if ok {
		t.Fatal("ok = true for a reviewer that did not answer")
	}
	flat := strings.Join(strings.Fields(out), " ")
	for _, want := range []string{"network off", "escalated permissions", "signed in"} {
		if !strings.Contains(flat, want) {
			t.Errorf("output = %q; missing %q", flat, want)
		}
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
