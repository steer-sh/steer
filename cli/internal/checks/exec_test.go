package checks

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// execRepo makes a repository whose manifest declares the given script checks,
// each written from scripts.
func execRepo(t *testing.T, scripts map[string]string) string {
	t.Helper()
	root := checkRepo(t, noCountScript)
	m, _ := Load(root)
	for id, body := range scripts {
		rel := ".steer/checks/" + id + ".sh"
		writeFile(t, root, rel, body)
		m.Checks = append(m.Checks, Declaration{ID: id, Kind: KindScript, Script: rel})
	}
	if err := Save(root, m); err != nil {
		t.Fatal(err)
	}
	return root
}

func byCheck(records []Execution) map[string]Execution {
	out := map[string]Execution{}
	for _, r := range records {
		out[r.CheckID] = r
	}
	return out
}

// The executor runs every script check even after one fails, records each
// outcome with the parent's run id and the current detector digest, and
// writes nothing into the validation history.
func TestExecRunsEveryCheckAndRecordsEachOutcome(t *testing.T) {
	root := execRepo(t, map[string]string{
		"a-fails":  "echo 'literal count found' >&2\nexit 1\n",
		"b-passes": "echo fine\n",
		"c-broken": "steer-no-such-tool\n",
	})
	var stdout, stderr bytes.Buffer
	records, err := Exec(root, ExecOptions{
		Stdout: &stdout, Stderr: &stderr,
		Invocation: Invocation{RunID: "run-1", Source: "stop", Harness: "claude", Session: "s1"},
	})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	got := byCheck(records)
	if len(records) != 3 || got["a-fails"].Outcome != OutcomeFailed || got["b-passes"].Outcome != OutcomePassed || got["c-broken"].Outcome != OutcomeError {
		t.Fatalf("records %+v; want failed, passed, error", records)
	}
	if !strings.Contains(got["a-fails"].Excerpt, "literal count found") || !strings.Contains(stderr.String(), "literal count found") {
		t.Fatalf("the failure diagnostic was not streamed and kept: excerpt %q stderr %q", got["a-fails"].Excerpt, stderr.String())
	}
	if !strings.Contains(stdout.String(), "fine") {
		t.Fatalf("stdout %q; want the passing check's output", stdout.String())
	}
	det, _ := DetectorOf(root, Declaration{ID: "a-fails", Kind: KindScript, Script: ".steer/checks/a-fails.sh"})
	for _, r := range records {
		if r.RunID != "run-1" || r.Source != "stop" || r.Harness != "claude" || r.Session != "s1" || r.Executor != ExecutorScript {
			t.Errorf("record %s carries %+v; want the parent's run identity", r.CheckID, r)
		}
	}
	if got["a-fails"].DetectorDigest != det.Digest {
		t.Errorf("detector digest %q; want %q", got["a-fails"].DetectorDigest, det.Digest)
	}
	stored, err := Executions(root)
	if err != nil || len(stored) != 3 {
		t.Fatalf("stream holds %d records (err %v); want 3", len(stored), err)
	}
	if history, _ := History(root); len(history) != 0 {
		t.Fatalf("execution wrote %d validation runs", len(history))
	}
}

// The deadline stops a hanging check with its children, and the checks after
// it are recorded as not executed rather than skipped silently.
func TestExecStopsAtTheDeadlineAndRecordsTheRestNotExecuted(t *testing.T) {
	root := execRepo(t, map[string]string{
		"a-hangs": "sleep 30 &\nsleep 30\n",
		"b-later": "exit 0\n",
	})
	start := time.Now()
	records, err := Exec(root, ExecOptions{Invocation: Invocation{Deadline: time.Now().Add(500 * time.Millisecond)}, Timeout: time.Hour})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("exec took %s; the deadline did not stop it", elapsed)
	}
	got := byCheck(records)
	if got["a-hangs"].Outcome != OutcomeTimeout || got["b-later"].Outcome != OutcomeNotExecuted {
		t.Fatalf("records %+v; want timeout then not-executed", records)
	}
}

// Selecting a go-test binding by id is refused: it runs through the Go suite.
func TestExecRefusesAGoTestBinding(t *testing.T) {
	root := execRepo(t, map[string]string{"a": "exit 0\n"})
	m, _ := Load(root)
	m.Checks = append(m.Checks, Declaration{ID: "go-one", Kind: KindGoTest, Package: ".", Test: "TestX"})
	if err := Save(root, m); err != nil {
		t.Fatal(err)
	}
	if _, err := Exec(root, ExecOptions{IDs: []string{"go-one"}}); err == nil || !strings.Contains(err.Error(), "go-test") {
		t.Fatalf("err %v; want a refusal naming steer check go-test", err)
	}
	records, err := Exec(root, ExecOptions{})
	if err != nil || len(records) != 1 || records[0].CheckID != "a" {
		t.Fatalf("--all ran %+v (err %v); want only the script check", records, err)
	}
}

// A malformed parent deadline is an error, never an unbounded run.
func TestInvocationFromEnvRefusesAMalformedDeadline(t *testing.T) {
	for _, name := range []string{EnvSource, EnvHarness, EnvSession, EnvEvidenceDir} {
		t.Setenv(name, "")
	}
	t.Setenv(EnvDeadline, "soon")
	if _, err := InvocationFromEnv(); err == nil {
		t.Fatal("a malformed deadline was accepted")
	}
	at := time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)
	t.Setenv(EnvDeadline, at.Format(time.RFC3339Nano))
	t.Setenv(EnvRunID, "r")
	inv, err := InvocationFromEnv()
	if err != nil || !inv.Deadline.Equal(at) || inv.RunID != "r" || inv.Source != SourceShell {
		t.Fatalf("inv %+v err %v; want the deadline, run id and the shell source", inv, err)
	}
}

// Init writes a check.sh only when none exists. The scaffold runs the
// executor through STEER_BIN and fails with an instruction when steer is
// missing.
func TestTheScaffoldRunsTheExecutorAndFailsWithoutSteer(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the scaffold needs bash")
	}
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module example.com/m\n")
	if created, err := WriteScaffold(root); err != nil || !created {
		t.Fatalf("WriteScaffold: created %v err %v", created, err)
	}
	if !OracleRunsExec(root) {
		t.Fatal("the scaffold does not name the executor")
	}
	if !bytes.Contains(Scaffold(root), []byte(GoSuiteInvocation)) {
		t.Fatal("a root go.mod did not add the Go suite line")
	}

	fakeDir := t.TempDir()
	calls := filepath.Join(fakeDir, "calls")
	writeFile(t, fakeDir, "steer", "#!/usr/bin/env bash\necho \"$*\" >> "+calls+"\n[ \"$2\" != exec ]\n")
	os.Chmod(filepath.Join(fakeDir, "steer"), 0o755)
	cmd := exec.Command("bash", OraclePath(root))
	cmd.Env = append(os.Environ(), "STEER_BIN="+filepath.Join(fakeDir, "steer"))
	if err := cmd.Run(); err == nil {
		t.Fatal("check.sh passed although the executor failed")
	}
	logged, _ := os.ReadFile(calls)
	if string(logged) != "check exec --all\ncheck go-test -- ./...\n" {
		t.Fatalf("steer calls %q; want exec then go-test, both run", logged)
	}

	cmd = exec.Command("bash", OraclePath(root))
	cmd.Env = append(os.Environ(), "STEER_BIN=", "PATH=/usr/bin:/bin")
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "STEER_BIN") {
		t.Fatalf("without steer: err %v output %q; want a failure naming STEER_BIN", err, out)
	}

	writeFile(t, root, OracleName, "#!/usr/bin/env bash\nmake test\n")
	if created, err := WriteScaffold(root); err != nil || created {
		t.Fatalf("an existing check.sh: created %v err %v; want it kept", created, err)
	}
	if data, _ := os.ReadFile(OraclePath(root)); string(data) != "#!/usr/bin/env bash\nmake test\n" {
		t.Fatalf("the existing check.sh changed: %q", data)
	}
	if OracleRunsExec(root) {
		t.Fatal("an oracle without the executor was reported as integrated")
	}
}

// A check.sh that runs a check by id is found, so a removal can say the line
// now fails. The --all scaffold, a comment and another command do not count.
func TestOracleNamesCheckFindsANamedExecInvocation(t *testing.T) {
	root := t.TempDir()
	if OracleNamesCheck(root, "no-todo") {
		t.Fatal("a repository without check.sh names a check")
	}
	writeFile(t, root, OracleName, string(Scaffold(root)))
	if OracleNamesCheck(root, "no-todo") {
		t.Fatal("the --all scaffold was read as naming no-todo")
	}
	writeFile(t, root, OracleName, "# \"$steer\" check exec no-todo\necho no-todo\n"+
		"\"$steer\" check exec --all || grep no-todo x\n\"$steer\" check exec lint; echo no-todo\n"+
		"\"$steer\" check exec --all&& echo no-todo\necho check exec no-todo\n")
	if OracleNamesCheck(root, "no-todo") {
		t.Fatal("a comment, a later command or a command that is not steer was read as naming no-todo")
	}
	writeFile(t, root, OracleName, "\"$steer\" check exec lint \"no-todo\" || status=1\n")
	if !OracleNamesCheck(root, "no-todo") {
		t.Fatal("a named exec invocation was not found")
	}
	if OracleNamesCheck(root, "no") {
		t.Fatal("a prefix of a named id matched")
	}
	writeFile(t, root, OracleName, "\"$steer\" check exec lint && \"$steer\" check exec no-todo\n")
	if !OracleNamesCheck(root, "no-todo") {
		t.Fatal("a second exec invocation on one line was not found")
	}
}

// The oracle's --all invocation is what makes a check the manifest gains run
// without an edit to check.sh. A comment, a named id and a command that is not
// steer do not count.
func TestOracleRunsEveryCheckFindsTheAllInvocation(t *testing.T) {
	root := t.TempDir()
	if OracleRunsEveryCheck(root) {
		t.Fatal("a repository without check.sh runs every check")
	}
	writeFile(t, root, OracleName, "# \"$steer\" check exec --all\n"+
		"\"$steer\" check exec no-todo || status=1\necho check exec --all\n")
	if OracleRunsEveryCheck(root) {
		t.Fatal("a comment, a named id or a command that is not steer was read as --all")
	}
	writeFile(t, root, OracleName, string(Scaffold(root)))
	if !OracleRunsEveryCheck(root) {
		t.Fatal("the scaffold's --all invocation was not found")
	}
}
