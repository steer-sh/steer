package cmd

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/steer-sh/steer-cli/internal/checks"
	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/gate/mine"
	"github.com/steer-sh/steer-cli/internal/harness"
	"github.com/steer-sh/steer-cli/internal/status"
)

// cliUnderTestEnv makes the test binary run the steer command line, so a
// check.sh started by a test reaches the real `steer check exec` through
// STEER_BIN.
const cliUnderTestEnv = "STEER_CMD_TEST_CLI"

func TestMain(m *testing.M) {
	if os.Getenv(cliUnderTestEnv) == "1" {
		if err := Execute(); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// The ineffective detector greps the wrong file, so it passes whether or not
// the response carries the field it names.
const ineffectiveDetector = `#!/usr/bin/env bash
set -euo pipefail
if ! grep -q '"id"' src/response.schema; then
  echo "response is missing field id" >&2
  exit 1
fi
`

const effectiveDetector = `#!/usr/bin/env bash
set -euo pipefail
if ! grep -q '"id"' src/response.json; then
  echo "response is missing field id" >&2
  exit 1
fi
`

const (
	fixedResponse  = `{"id": 1, "name": "a"}` + "\n"
	brokenResponse = `{"name": "a"}` + "\n"
	responseSchema = `{"required": ["id"]}` + "\n"
)

func writeRepoFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func responseCases(t *testing.T, root string) {
	t.Helper()
	zero, one := 0, 1
	files := func(response string) map[string]string {
		return map[string]string{"src/response.json": response, "src/response.schema": responseSchema}
	}
	data, err := json.Marshal(checks.CaseFile{Version: checks.CasesVersion, Cases: []checks.Case{
		{Name: "fixed", ExitCode: &zero, Provenance: "planted", Files: files(fixedResponse)},
		{Name: "omits-id", ExitCode: &one, Diagnostic: "missing field id", Provenance: "planted",
			Files: files(brokenResponse)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	writeRepoFile(t, root, ".steer/checks/cases/response-id.json", string(data))
}

func stopPayload(t *testing.T, root string) []byte {
	t.Helper()
	raw, err := json.Marshal(checks.StopPayload{HookEventName: "Stop", SessionID: "session-a", CWD: root})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// snapshot reads the status snapshot the way `steer status` composes it, over
// an empty review ledger.
func snapshot(t *testing.T, stateDir, ledger, root string) *status.SystemStatus {
	t.Helper()
	s, err := status.Collect(status.Options{GateStateDir: stateDir, Ledger: ledger, RepoRoot: root})
	if err != nil {
		t.Fatalf("status.Collect: %v", err)
	}
	return s
}

func onlyRow(t *testing.T, s *status.SystemStatus) status.CheckRow {
	t.Helper()
	if len(s.Checks.Checks) != 1 {
		t.Fatalf("check rows %+v; want response-id alone", s.Checks.Checks)
	}
	return s.Checks.Checks[0]
}

// A planted ineffective check runs the whole tier: replay refuses it, the
// corrected detector validates, adoption and fix registration bind it, and a
// Stop run blocks on a planted defect with per-check evidence. The replay and
// adoption runs never read as live execution, a check failure never counts as
// recurrence of the fix, and an edited detector keeps running with its proof
// labeled stale and no queue step raised.
func TestAcceptancePlantedIneffectiveCheckThroughStop(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("the check tier needs bash")
	}
	fakeHomes(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(cliUnderTestEnv, "1")
	t.Setenv("STEER_BIN", "")

	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	t.Chdir(root)
	identity, err := checks.RepoIdentity(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := checks.Init(root); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if _, err := checks.WriteScaffold(root); err != nil {
		t.Fatalf("WriteScaffold: %v", err)
	}
	writeRepoFile(t, root, "src/response.json", fixedResponse)
	writeRepoFile(t, root, "src/response.schema", responseSchema)
	responseCases(t, root)
	decl := checks.Declaration{ID: "response-id", Kind: checks.KindScript, Script: ".steer/checks/response-id.sh"}

	// Replay validation refuses the ineffective detector: its defect case passes.
	writeRepoFile(t, root, decl.Script, ineffectiveDetector)
	if run, err := checks.Add(root, decl); !errors.Is(err, checks.ErrNotValidated) || run.Verdict != checks.VerdictFailed {
		t.Fatalf("the ineffective detector: verdict %s err %v; want a failed replay", run.Verdict, err)
	}
	writeRepoFile(t, root, decl.Script, effectiveDetector)
	if run, err := checks.Add(root, decl); err != nil {
		t.Fatalf("the corrected detector: %v (cases %+v)", err, run.Cases)
	}

	// Explicit adoption reuses the receipt and runs check.sh in isolation.
	res, err := checks.Adopt(root, checks.AdoptOptions{SteerBin: exe, Timeout: time.Minute})
	if err != nil {
		t.Fatalf("Adopt: %v (oracle %+v)", err, res.Oracle)
	}
	if len(res.Executions) != 1 || res.Executions[0].Outcome != checks.OutcomePassed {
		t.Fatalf("adopt run records %+v; want response-id passed through the real executor", res.Executions)
	}
	stateDir := t.TempDir()
	ledger := filepath.Join(stateDir, "adjudication.jsonl")
	writeRepoFile(t, stateDir, "adjudication.jsonl", "")
	s := snapshot(t, stateDir, ledger, root)
	row := onlyRow(t, s)
	if !s.Checks.Adopted || !row.Validated() || row.Execution != status.ExecutionUnobserved || s.Checks.LatestRun != nil {
		t.Fatalf("after adoption: tier %+v row %+v; want validated with no live execution or run", s.Checks, row)
	}
	if len(s.Attention.Checks) != 0 {
		t.Fatalf("after adoption the queue holds %+v; want no check step", s.Attention.Checks)
	}

	// Fix registration binds the check to its repository and installation.
	if _, err := registerCheckRunner(harness.ClaudeCode, claude.GlobalScope); err != nil {
		t.Fatalf("registerCheckRunner: %v", err)
	}
	facts, err := bindCheckTier("check", decl.ID, []string{"claude-code"})
	if err != nil {
		t.Fatalf("bindCheckTier: %v", err)
	}
	if err := mine.Register(io.Discard, stateDir, mine.RegisterOpts{
		Ledger: ledger, Class: "response omits id", Match: "missing field id", Tier: "check",
		Target: decl.Script, CheckID: decl.ID, CheckFacts: facts, Force: true,
		Prose: mine.Prose{
			Problem:      "A response is written without the id field that every client reads.",
			Intervention: "A repository check fails the turn when the response omits the id field.",
		},
		ScopeHarness: []string{"claude-code"}, ScopeRepoRoot: identity,
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	records, _ := mine.Load(stateDir)
	if fix, bound := mine.CheckFixOf(records[0]); len(records) != 1 || !bound || fix.Repo != identity {
		t.Fatalf("records %+v; want one fix bound to %s", records, identity)
	}

	// A Stop run blocks on the planted defect and the evidence names the check.
	writeRepoFile(t, root, "src/response.json", brokenResponse)
	blocked, reply := checks.StopHook("claude-code", stopPayload(t, root), checks.StopHookOptions{SteerBin: exe, Timeout: time.Minute})
	if blocked.Disposition != checks.DispositionBlock || reply == nil || !strings.Contains(reply.Reason, "missing field id") {
		t.Fatalf("Stop %+v reply %+v; want a block carrying the detector's diagnostic", blocked, reply)
	}
	s = snapshot(t, stateDir, ledger, root)
	row = onlyRow(t, s)
	if row.Execution != checks.OutcomeFailed || row.ExecutionRun != blocked.Run.RunID {
		t.Fatalf("row %+v; want response-id failed under run %s", row, blocked.Run.RunID)
	}
	if r := s.Checks.LatestRun; r == nil || r.Source != checks.SourceStop || r.Disposition != checks.DispositionBlock {
		t.Fatalf("latest run %+v; want the blocking Stop run", r)
	}
	for _, a := range s.Checks.Runner {
		if a.Harness == "claude-code" && a.Scope == claude.GlobalScope && a.Observed == 0 {
			t.Errorf("runner row %+v; want the Stop run observed", a)
		}
	}
	if phrase := status.CheckPhrase(row); !strings.Contains(phrase, "tested and unchanged since") ||
		!strings.Contains(phrase, "last "+checks.OutcomeFailed) {
		t.Errorf("phrase %q; want the proof and the failed execution named apart", phrase)
	}
	if len(s.Interventions) != 1 || s.Interventions[0].After.Hits != 0 {
		t.Fatalf("interventions %+v; a check failure is not recurrence of the class", s.Interventions)
	}

	// An edited detector keeps running; its proof is labeled stale, and the
	// queue asks for nothing.
	writeRepoFile(t, root, decl.Script, effectiveDetector+"# edited\n")
	again, _ := checks.StopHook("claude-code", stopPayload(t, root), checks.StopHookOptions{SteerBin: exe, Timeout: time.Minute})
	if again.Run == nil || again.Run.RunID == blocked.Run.RunID {
		t.Fatalf("Stop %+v; want a new run of the edited detector", again)
	}
	s = snapshot(t, stateDir, ledger, root)
	row = onlyRow(t, s)
	if row.Validation != status.CheckStale || row.Execution != checks.OutcomeFailed || row.ExecutionRun != again.Run.RunID {
		t.Fatalf("row %+v; want a stale proof and the new run's failure", row)
	}
	if len(s.Attention.Checks) != 0 {
		t.Fatalf("a stale proof raised queue steps %+v", s.Attention.Checks)
	}
}
