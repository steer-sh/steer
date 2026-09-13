package checks

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// testExecutorEnv makes the test binary act as `steer check exec`, so an
// oracle run in a test reaches the real executor through STEER_BIN.
const testExecutorEnv = "STEER_CHECKS_TEST_EXECUTOR"

func TestMain(m *testing.M) {
	if os.Getenv(testExecutorEnv) == "1" {
		os.Exit(testExecutor(os.Args[1:]))
	}
	if identity := os.Getenv(testLockHolderEnv); identity != "" {
		os.Exit(testLockHolder(identity))
	}
	os.Exit(m.Run())
}

func testExecutor(args []string) int {
	if len(args) < 3 || args[0] != "check" || args[1] != "exec" || args[2] != "--all" {
		fmt.Fprintln(os.Stderr, "test executor: unsupported arguments", args)
		return 2
	}
	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	inv, err := InvocationFromEnv()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	records, err := Exec(root, ExecOptions{Stdout: os.Stdout, Stderr: os.Stderr, Invocation: inv, Timeout: DefaultExecTimeout})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	for _, r := range records {
		if !r.Passed() {
			return 1
		}
	}
	return 0
}

// oracleRepo makes a repository with the given script checks and check.sh,
// and returns run options whose STEER_BIN is the test executor.
func oracleRepo(t *testing.T, scripts map[string]string, oracle string) (string, RunOptions) {
	t.Helper()
	root := execRepo(t, scripts)
	if oracle == "" {
		oracle = string(Scaffold(root))
	}
	writeFile(t, root, OracleName, oracle)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(testExecutorEnv, "1")
	return root, RunOptions{SteerBin: exe, Timeout: time.Minute}
}

// A run through the scaffold records its own outcome under a new run ID and
// finds each executor record under that ID; the aggregate stays out of the
// execution stream and validation history.
func TestRunOracleRecordsTheAggregateApartFromEachCheck(t *testing.T) {
	root, opts := oracleRepo(t, map[string]string{
		"a-fails":  "echo 'literal count found' >&2\nexit 1\n",
		"b-passes": "echo fine\n",
	}, "")
	var stdout, stderr bytes.Buffer
	opts.Stdout, opts.Stderr = &stdout, &stderr
	run, records, err := RunOracle(root, opts)
	if err != nil {
		t.Fatalf("RunOracle: %v", err)
	}
	if run.Outcome != OutcomeFailed || run.Passed() || run.Source != SourceRun || len(run.Missing) != 0 {
		t.Fatalf("run %+v; want a failed run from source run with nothing missing", run)
	}
	got := byCheck(records)
	if len(records) != 2 || got["a-fails"].Outcome != OutcomeFailed || got["b-passes"].Outcome != OutcomePassed {
		t.Fatalf("records %+v; want a-fails failed and b-passes passed", records)
	}
	for _, r := range records {
		if r.RunID != run.RunID || r.Executor != ExecutorScript || r.Source != SourceRun {
			t.Errorf("record %s: run %q executor %q source %q; want the executor's record under run %q", r.CheckID, r.RunID, r.Executor, r.Source, run.RunID)
		}
	}
	if !strings.Contains(stdout.String(), "fine") || !strings.Contains(run.Excerpt, "literal count found") {
		t.Fatalf("stdout %q excerpt %q; want check output streamed and kept", stdout.String(), run.Excerpt)
	}
	runs, err := OracleRuns(root)
	if err != nil || len(runs) != 1 || runs[0].RunID != run.RunID {
		t.Fatalf("oracle runs %+v (err %v); want the one run", runs, err)
	}
	if stored, _ := Executions(root); len(stored) != 2 {
		t.Fatalf("execution stream holds %d records; want the 2 check records only", len(stored))
	}
	if history, _ := History(root); len(history) != 0 {
		t.Fatalf("an oracle run wrote %d validation runs", len(history))
	}
}

// A declared check that no executor reported is recorded as not executed.
// After a passing check.sh it is an integration failure; after a failing one
// it is a check the run never reached.
func TestAnUnreportedCheckIsNotExecutedAndFailsIntegrationOnlyAfterAPass(t *testing.T) {
	for _, tc := range []struct {
		oracle      string
		outcome     string
		integration bool
		reason      string
	}{
		{"exit 0\n", OutcomePassed, true, "does not call its executor"},
		{"exit 3\n", OutcomeFailed, false, "stopped before"},
	} {
		root, opts := oracleRepo(t, map[string]string{"a": "exit 0\n"}, tc.oracle)
		run, records, err := RunOracle(root, opts)
		if err != nil {
			t.Fatalf("RunOracle: %v", err)
		}
		if run.Outcome != tc.outcome || run.IntegrationFailure != tc.integration || run.Passed() {
			t.Fatalf("%q: run %+v; want outcome %s, integration failure %v, not passed", tc.oracle, run, tc.outcome, tc.integration)
		}
		if len(run.Missing) != 1 || run.Missing[0] != "a" {
			t.Fatalf("%q: missing %v; want [a]", tc.oracle, run.Missing)
		}
		if len(records) != 1 || records[0].Outcome != OutcomeNotExecuted || records[0].Executor != ExecutorOracle ||
			!strings.Contains(records[0].Reason, tc.reason) || records[0].DetectorDigest == "" {
			t.Fatalf("%q: records %+v; want one not-executed parent record naming %q", tc.oracle, records, tc.reason)
		}
		if latest, _ := LatestExecutions(root); latest["a"].RunID != run.RunID {
			t.Fatalf("%q: latest execution %+v; want the parent's record", tc.oracle, latest["a"])
		}
	}
}

// The deadline stops check.sh with its children and reports the declared
// checks as not executed, never passed.
func TestTheDeadlineStopsTheOracleAndLeavesChecksNotExecuted(t *testing.T) {
	root, opts := oracleRepo(t, map[string]string{"a": "exit 0\n"}, "sleep 30 &\nsleep 30\n")
	opts.Timeout = time.Second
	start := time.Now()
	run, records, err := RunOracle(root, opts)
	if err != nil {
		t.Fatalf("RunOracle: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("the run took %s; the deadline did not stop it", elapsed)
	}
	if run.Outcome != OutcomeTimeout || run.IntegrationFailure || len(records) != 1 || records[0].Outcome != OutcomeNotExecuted {
		t.Fatalf("run %+v records %+v; want a timeout with a not-executed check", run, records)
	}
}

// Executors get a deadline earlier than the parent's, so a hung check is
// recorded as a timeout by its executor before the parent stops the group.
func TestAHungCheckIsRecordedAsATimeoutBeforeTheOracleDeadline(t *testing.T) {
	root, opts := oracleRepo(t, map[string]string{"a-hangs": "sleep 30\n", "b-later": "exit 0\n"}, "")
	opts.Timeout = 5 * time.Second
	run, records, err := RunOracle(root, opts)
	if err != nil {
		t.Fatalf("RunOracle: %v", err)
	}
	got := byCheck(records)
	if run.Outcome != OutcomeFailed || got["a-hangs"].Outcome != OutcomeTimeout || got["a-hangs"].Executor != ExecutorScript ||
		got["b-later"].Outcome != OutcomeNotExecuted || got["b-later"].Executor != ExecutorScript {
		t.Fatalf("run %s records %+v; want the executor's timeout and not-executed records", run.Outcome, records)
	}
}

// An isolated run writes its records under the named root and nothing into
// the live streams, and a nested contract from the environment is replaced.
func TestAnIsolatedRunLeavesNoLiveEvidence(t *testing.T) {
	root, opts := oracleRepo(t, map[string]string{"a": "exit 0\n"}, "")
	opts.EvidenceDir = t.TempDir()
	t.Setenv(EnvRunID, "inherited-run")
	run, records, err := RunOracle(root, opts)
	if err != nil {
		t.Fatalf("RunOracle: %v", err)
	}
	if !run.Passed() || len(records) != 1 || records[0].RunID != run.RunID || run.RunID == "inherited-run" {
		t.Fatalf("run %+v records %+v; want a passing run with its own record", run, records)
	}
	if stored, _ := Executions(root); len(stored) != 0 {
		t.Fatalf("an isolated run wrote %d live execution records", len(stored))
	}
	if runs, _ := OracleRuns(root); len(runs) != 0 {
		t.Fatalf("an isolated run wrote %d live oracle runs", len(runs))
	}
	identity, _ := RepoIdentity(root)
	if isolated, _ := executionsIn(opts.EvidenceDir, identity); len(isolated) != 1 {
		t.Fatalf("the isolated root holds %d records; want 1", len(isolated))
	}
	if _, err := os.Stat(filepath.Join(opts.EvidenceDir, repoKey(identity), runsName)); err != nil {
		t.Fatalf("the isolated root has no oracle run: %v", err)
	}
}

// A repository without check.sh is reported as such, before anything runs.
func TestRunOracleReportsAMissingOracle(t *testing.T) {
	root := checkRepo(t, noCountScript)
	if _, _, err := RunOracle(root, RunOptions{}); !errors.Is(err, ErrNoOracle) {
		t.Fatalf("err %v; want ErrNoOracle", err)
	}
}

// A relative evidence directory is refused, so isolated records never land
// beside the working directory.
func TestInvocationFromEnvRefusesARelativeEvidenceDirectory(t *testing.T) {
	t.Setenv(EnvEvidenceDir, "evidence")
	if _, err := InvocationFromEnv(); err == nil {
		t.Fatal("a relative evidence directory was accepted")
	}
	abs := t.TempDir()
	t.Setenv(EnvEvidenceDir, abs)
	if inv, err := InvocationFromEnv(); err != nil || inv.EvidenceDir != abs {
		t.Fatalf("inv %+v err %v; want the absolute directory", inv, err)
	}
}
