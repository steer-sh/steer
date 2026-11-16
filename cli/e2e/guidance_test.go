//go:build e2e

package e2e

import (
	"encoding/json"
	"strings"
	"testing"
)

// guidanceMarker is a phrase of the commit-shape guidance.
const guidanceMarker = "Commit in this form"

// TestCommitShapeGuidanceReachesArmedClaudeSessions runs both hook entry points: they
// stay silent until the Claude Code gate is armed and outside Claude Code, and once
// armed they give the guidance to the model's context and never to the operator line.
//
// pins existing behaviour
func TestCommitShapeGuidanceReachesArmedClaudeSessions(t *testing.T) {
	t.Parallel()
	s := NewSandbox(t)
	claudeCode := []string{"CLAUDECODE=1"}
	run := func(env []string, args ...string) Result {
		t.Helper()
		r := s.Run(s.Repo, "", env, "steer", args...)
		if r.Code != 0 {
			t.Fatalf("steer %q exited %d:\n%s", args, r.Code, r.Output())
		}
		return r
	}
	decode := func(r Result) (event, system, context string) {
		t.Helper()
		var doc struct {
			SystemMessage string `json:"systemMessage"`
			Hook          struct {
				Event   string `json:"hookEventName"`
				Context string `json:"additionalContext"`
			} `json:"hookSpecificOutput"`
		}
		if err := json.Unmarshal([]byte(r.Stdout), &doc); err != nil {
			t.Fatalf("hook reply is not JSON: %v\n%s", err, r.Stdout)
		}
		return doc.Hook.Event, doc.SystemMessage, doc.Hook.Context
	}

	if r := run(claudeCode, "commit-gate", "subagent-start"); r.Stdout != "" {
		t.Errorf("subagent-start spoke before the gate was armed: %q", r.Stdout)
	}

	s.Reviewer("claude-code", "sonnet", "", 3, 3)
	s.MustSteer("commit-gate", "install", "--harness", "claude-code")

	event, system, context := decode(run(claudeCode, "commit-gate", "subagent-start"))
	if event != "SubagentStart" || !strings.Contains(context, guidanceMarker) || system != "" {
		t.Errorf("armed subagent-start: event %q, systemMessage %q, context %q", event, system, context)
	}
	for _, env := range [][]string{nil, {"CLAUDECODE=1", "CODEX_SESSION_ID=x"}} {
		if r := run(env, "commit-gate", "subagent-start"); r.Stdout != "" {
			t.Errorf("subagent-start spoke outside a Claude Code session (env %q): %q", env, r.Stdout)
		}
	}

	event, system, context = decode(run(claudeCode, "report", "cadence"))
	s.WaitForRefresh()
	if event != "SessionStart" || !strings.Contains(context, guidanceMarker) {
		t.Errorf("armed cadence: event %q, context %q", event, context)
	}
	if strings.Contains(system, guidanceMarker) {
		t.Errorf("the guidance reached the operator line: %q", system)
	}
}

// TestCodexCommitGuidance checks both context entry points with Codex registration.
// pins existing behaviour
func TestCodexCommitGuidance(t *testing.T) {
	t.Parallel()
	s := NewSandbox(t)
	env := []string{"CODEX_SESSION_ID=test"}
	run := func(args ...string) Result { return s.Run(s.Repo, "", env, "steer", args...) }
	if r := run("commit-gate", "subagent-start"); r.Code != 0 || r.Stdout != "" {
		t.Fatalf("unarmed: %s", r.Output())
	}
	s.Reviewer("claude-code", "sonnet", "", 3, 3)
	s.MustSteer("commit-gate", "install", "--harness", "codex")
	for _, args := range [][]string{{"commit-gate", "subagent-start"}, {"report", "cadence"}} {
		r := run(args...)
		var doc struct {
			System string `json:"systemMessage"`
			Hook   struct {
				Context string `json:"additionalContext"`
			} `json:"hookSpecificOutput"`
		}
		if r.Code != 0 {
			t.Fatalf("hook: %s", r.Output())
		}
		if err := json.Unmarshal([]byte(r.Stdout), &doc); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(doc.Hook.Context, "git -C /absolute/path/to/repository commit -F -") || strings.Contains(doc.System, guidanceMarker) {
			t.Fatalf("delivery: %s", r.Stdout)
		}
	}
	s.WaitForRefresh()
}
