package cc

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
	"github.com/steer-sh/steer-cli/internal/testhome"
)

// TestRunKeysASubagentChainOnItsThread commits from the root thread and then from a
// subagent of the same session in one repository. The subagent opens its own chain, and
// its retry takes no ledger from the main thread's transcript.
func TestRunKeysASubagentChainOnItsThread(t *testing.T) {
	testhome.Set(t, t.TempDir())
	repo := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-q")
	body := "package probe\n" + strings.Repeat("// staged payload line\n", 80)
	if err := os.WriteFile(filepath.Join(repo, "a.go"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "a.go")

	cfg := gate.Defaults()
	cfg.StateDir = t.TempDir()
	cfg.ReviewerRuns = 1
	transcript := writeTranscript(t, transcriptRowJSON(t, realLedger, "2099-01-01T00:00:00.000Z", "assistant", false))
	gateCall := func(agent string) {
		t.Helper()
		payload := map[string]any{
			"tool_name": "Bash", "session_id": "session-1", "cwd": repo, "prompt_id": "prompt-1",
			"transcript_path": transcript,
			"tool_input":      map[string]string{"command": "git " + c + " -m probe"},
		}
		if agent != "" {
			payload["agent_id"] = agent
		}
		raw, _ := json.Marshal(payload)
		var stdout bytes.Buffer
		Run(context.Background(), cfg, &initialCommitReviewer{}, bytes.NewReader(raw), &stdout)
		if stdout.Len() == 0 {
			t.Fatalf("agent %q: the gate did not rule", agent)
		}
	}
	gateCall("")
	gateCall("agent-7")
	gateCall("agent-7")
	gateCall("")

	raw, err := os.ReadFile(cfg.LogFile())
	if err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		row := map[string]any{}
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatal(err)
		}
		if row["phase"] == "deny" || row["phase"] == "retry" {
			rows = append(rows, row)
		}
	}
	if len(rows) != 4 {
		t.Fatalf("review rows = %#v, want a deny and a retry per thread", rows)
	}
	root, sub := rows[0], rows[1]
	if _, ok := root["thread_id"]; ok {
		t.Fatalf("root thread carried a thread id: %#v", root)
	}
	if sub["phase"] != "deny" || sub["firing"] != float64(1) {
		t.Fatalf("subagent row = %#v, want a first-round deny of its own chain", sub)
	}
	if sub["thread_id"] != "agent-7" || sub["session_id"] != "session-1" {
		t.Fatalf("subagent row = %#v, want thread agent-7 under session-1", sub)
	}
	if sub["chain_id"] == root["chain_id"] {
		t.Fatalf("subagent shares the root chain %v", root["chain_id"])
	}
	if subRetry := rows[2]; subRetry["phase"] != "retry" || subRetry["ledger_found"] == true {
		t.Fatalf("subagent retry = %#v, want a retry with no main-thread ledger", subRetry)
	}
	if rootRetry := rows[3]; rootRetry["phase"] != "retry" || rootRetry["ledger_found"] != true {
		t.Fatalf("root retry = %#v, want the main-thread ledger", rootRetry)
	}
}
