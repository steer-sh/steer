package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/gate"
)

type stubReviewer struct{ reply string }

func (stubReviewer) Name() string       { return "stub" }
func (stubReviewer) ModelLabel() string { return "stub-model" }

func (s stubReviewer) Review(context.Context, string, string) gate.RawRun {
	return gate.RawRun{Response: s.reply, Status: "SUCCESS"}
}

const oneFinding = "[1] SEVERITY: BLOCKING\n    FILE: a.py\n    CLAIM: x.\n    FAILS WHEN: y.\n"

// buildRepo stages a file large enough to clear the gate's minimum diff size.
func buildRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "t@t")
	run("config", "user.name", "t")
	var body strings.Builder
	for i := range 200 {
		body.WriteString("line ")
		body.WriteString(strings.Repeat("x", 20))
		body.WriteByte(byte('0' + i%10))
		body.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(repo, "a.py"), []byte(body.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	return repo
}

// readRows returns the adjudication rows the run appended.
func readRows(t *testing.T, cfg gate.Config) []map[string]any {
	t.Helper()
	raw, err := os.ReadFile(cfg.LogFile())
	if err != nil {
		t.Fatalf("no ledger: %v", err)
	}
	var rows []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		row := map[string]any{}
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatalf("row %q: %v", line, err)
		}
		rows = append(rows, row)
	}
	return rows
}

func runGate(t *testing.T, cfg gate.Config, payload string) Output {
	t.Helper()
	var stdout bytes.Buffer
	Run(context.Background(), cfg, stubReviewer{reply: oneFinding}, strings.NewReader(payload), &stdout)
	var out Output
	if stdout.Len() == 0 {
		return out
	}
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("decode %q: %v", stdout.String(), err)
	}
	return out
}

func TestRunDeniesAndStampsCodexEvidence(t *testing.T) {
	cfg := testConfig(t)
	cfg.ReviewerRuns = 1
	repo := buildRepo(t)
	transcript := writeRollout(t, rollout)
	payload := `{"hook_event_name":"PreToolUse","tool_name":"Bash","session_id":"session-1",` +
		`"turn_id":"turn-1","model":"gpt-5.6-sol","cwd":"` + repo + `",` +
		`"transcript_path":"` + transcript + `","tool_input":{"workdir":"` + repo + `",` +
		`"command":"git commit -m \"add a\""}}`

	out := runGate(t, cfg, payload)
	if out.HookSpecificOutput == nil || out.HookSpecificOutput.PermissionDecision != "deny" {
		t.Fatalf("output = %#v", out)
	}
	// Codex prints the reason and hides the context, so the whole reply the model has to
	// act on rides in the hidden field.
	if !strings.Contains(out.HookSpecificOutput.AdditionalContext, gate.LedgerHeader) {
		t.Fatalf("the review did not reach the model: %#v", *out.HookSpecificOutput)
	}
	if out.HookSpecificOutput.PermissionDecisionReason != DenyReason {
		t.Fatalf("reason = %q", out.HookSpecificOutput.PermissionDecisionReason)
	}

	rows := readRows(t, cfg)
	row := rows[len(rows)-1]
	if row["phase"] != "deny" || row["harness"] != "codex" || row["client"] != "codex" {
		t.Fatalf("row = %#v", row)
	}
	if row["author_model"] != "gpt-5.6-sol" || row["transcript"] != transcript ||
		row["task"] != "second task" || row["session_id"] != "session-1" {
		t.Fatalf("evidence missing: %#v", row)
	}
	if _, ok := row["thread_id"]; ok {
		t.Fatalf("root thread carried a thread id: %#v", row)
	}
}

func TestRunKeysASubagentChainOnItsThread(t *testing.T) {
	cfg := testConfig(t)
	cfg.ReviewerRuns = 1
	repo := buildRepo(t)
	payload := `{"hook_event_name":"PreToolUse","tool_name":"Bash","session_id":"session-1",` +
		`"agent_id":"agent-9","agent_type":"default","cwd":"` + repo + `",` +
		`"tool_input":{"workdir":"` + repo + `","command":"git commit -m \"add a\""}}`

	if out := runGate(t, cfg, payload); out.HookSpecificOutput == nil {
		t.Fatalf("subagent commit was not ruled on: %#v", out)
	}
	row := readRows(t, cfg)[0]
	if row["thread_id"] != "agent-9" || row["agent_type"] != "default" {
		t.Fatalf("thread provenance missing: %#v", row)
	}
	// The root session is the statistical unit, so the row files under it.
	if row["session_id"] != "session-1" {
		t.Fatalf("session_id = %v, want the parent session", row["session_id"])
	}
}

func TestRunIgnoresANonCommitCommand(t *testing.T) {
	cfg := testConfig(t)
	payload := `{"hook_event_name":"PreToolUse","tool_name":"Bash","session_id":"s",` +
		`"tool_input":{"command":"git status"}}`
	if out := runGate(t, cfg, payload); out.HookSpecificOutput != nil || out.SystemMessage != "" {
		t.Fatalf("output = %#v", out)
	}
	if _, err := os.Stat(cfg.LogFile()); err == nil {
		t.Fatal("a non-commit command wrote a row")
	}
}
