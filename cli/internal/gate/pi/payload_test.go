package pi

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

type stubReviewer struct{}

func (stubReviewer) Name() string       { return "stub" }
func (stubReviewer) ModelLabel() string { return "stub-model" }
func (stubReviewer) Review(context.Context, string, string) gate.RawRun {
	return gate.RawRun{Response: "[1] SEVERITY: BLOCKING\n    FILE: a.go\n    CLAIM: x.\n    FAILS WHEN: y.\n", Status: "SUCCESS"}
}

func TestRenderUsesPiBlockContract(t *testing.T) {
	out, ok := Render(gate.Decision{Kind: gate.Deny, Reason: "fix it", SystemMessage: "fired"})
	if !ok || !out.Block || out.Reason != "fix it" || out.SystemMessage != "fired" {
		t.Fatalf("render = %#v, %v", out, ok)
	}
	raw, _ := json.Marshal(out)
	if strings.Contains(string(raw), "permissionDecision") || strings.Contains(string(raw), `"decision"`) {
		t.Fatalf("foreign hook contract emitted: %s", raw)
	}
}

func TestRenderKeepsNonBlockingContextSeparateFromBlockReason(t *testing.T) {
	out, ok := Render(gate.Decision{Kind: gate.ContextOnly, Reason: "staged tree is stale"})
	if !ok || out.Block || out.Reason != "" || out.AdditionalContext != "staged tree is stale" {
		t.Fatalf("render = %#v, %v", out, ok)
	}
}

func TestRunReviewsPiPayloadWithoutAWorkdirField(t *testing.T) {
	repo := stagedRepo(t)
	cfg := gate.Defaults()
	cfg.StateDir = t.TempDir()
	cfg.ReviewerRuns = 1
	payload := `{"hook_event_name":"PreToolUse","tool_name":"Bash",` +
		`"session_id":"pi-session","model":"model-pi","transcript_path":"/sessions/pi.jsonl",` +
		`"cwd":` + strconvJSON(repo) + `,"tool_input":{"command":"git commit -m \"add a\""}}`
	var stdout bytes.Buffer
	Run(context.Background(), cfg, stubReviewer{}, strings.NewReader(payload), &stdout)
	var out Output
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil || !out.Block {
		t.Fatalf("output = %q, err = %v", stdout.String(), err)
	}
	raw, err := os.ReadFile(cfg.LogFile())
	if err != nil {
		t.Fatal(err)
	}
	var row map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(raw), &row); err != nil {
		t.Fatal(err)
	}
	if row["harness"] != "pi" || row["client"] != "pi" || row["session_id"] != "pi-session" ||
		row["author_model"] != "model-pi" || row["transcript"] != "/sessions/pi.jsonl" {
		t.Fatalf("row = %#v", row)
	}
}

func TestRunRefusesLooseOnlyCommitWithoutReviewOrLedger(t *testing.T) {
	cfg := gate.Defaults()
	cfg.StateDir = t.TempDir()
	payload := `{"hook_event_name":"PreToolUse","tool_name":"Bash",` +
		`"session_id":"pi-session","cwd":"/repo",` +
		`"tool_input":{"command":"echo 'release git commit'"}}`
	var stdout bytes.Buffer
	Run(context.Background(), cfg, stubReviewer{}, strings.NewReader(payload), &stdout)
	var out Output
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil || !out.Block ||
		!strings.Contains(out.Reason, "possible commit") {
		t.Fatalf("output = %q, err = %v", stdout.String(), err)
	}
	if _, err := os.Stat(cfg.LogFile()); !os.IsNotExist(err) {
		t.Fatalf("ambiguous refusal wrote a ledger: %v", err)
	}
}

func stagedRepo(t *testing.T) string {
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
	body := strings.Repeat("a sufficiently large changed line for gate review\n", 200)
	if err := os.WriteFile(filepath.Join(repo, "a.go"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	return repo
}

func strconvJSON(value string) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}

func TestRunStampsTheContextDepthOfTheActiveBranch(t *testing.T) {
	repo := stagedRepo(t)
	cfg := gate.Defaults()
	cfg.StateDir = t.TempDir()
	cfg.ReviewerRuns = 1
	transcript := filepath.Join(t.TempDir(), "pi.jsonl")
	body := `{"type":"session","version":3,"id":"pi-session","cwd":"/repo"}
{"type":"message","id":"a1","parentId":null,"timestamp":"2026-04-19T03:17:26.000Z","message":{"role":"assistant","content":[],"usage":{"input":168,"cacheRead":2241,"cacheWrite":0}}}
{"type":"message","id":"a2","parentId":null,"timestamp":"2026-04-19T03:17:27.000Z","message":{"role":"assistant","content":[],"usage":{"input":9,"cacheRead":99990,"cacheWrite":0}}}
{"type":"branch_summary","id":"a3","parentId":"a1","timestamp":"2026-04-19T03:17:28.000Z","fromId":"a2","summary":"abandoned"}
`
	if err := os.WriteFile(transcript, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	payload := `{"hook_event_name":"PreToolUse","tool_name":"Bash",` +
		`"session_id":"pi-session","model":"model-pi","transcript_path":` + strconvJSON(transcript) +
		`,"cwd":` + strconvJSON(repo) + `,"tool_input":{"command":"git commit -m \"add a\""}}`
	var stdout bytes.Buffer
	Run(context.Background(), cfg, stubReviewer{}, strings.NewReader(payload), &stdout)

	raw, err := os.ReadFile(cfg.LogFile())
	if err != nil {
		t.Fatal(err)
	}
	var row map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(raw), &row); err != nil {
		t.Fatal(err)
	}
	if row["context_tokens"] != float64(2409) {
		t.Fatalf("context_tokens = %v", row["context_tokens"])
	}
}
