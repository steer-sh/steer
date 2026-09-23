package checks

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// testLockHolderEnv makes the test binary hold the named repository's lock
// until it is killed.
const testLockHolderEnv = "STEER_CHECKS_TEST_LOCK_HOLDER"

func testLockHolder(identity string) int {
	if _, _, err := acquireRepoLock(identity, LockHolder{RunID: "held-by-child", PID: os.Getpid(), Source: SourceRun}); err != nil {
		fmt.Println("error:", err)
		return 1
	}
	fmt.Println("locked")
	time.Sleep(time.Minute)
	return 0
}

// stopRepo makes an adopted Git repository with the given script checks and
// check.sh, and a Stop request for it from one Claude Code session.
func stopRepo(t *testing.T, scripts map[string]string, oracle string) (string, StopRequest) {
	t.Helper()
	root, opts := oracleRepo(t, scripts, oracle)
	gitIn(t, root, "init", "-q")
	identity, err := RepoIdentity(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := appendAdoption(Adoption{Version: AdoptionVersion, Action: ActionAdopt, Repo: identity, At: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	return identity, StopRequest{Harness: "claude-code", Session: "session-a", CWD: root, Timeout: opts.Timeout, SteerBin: opts.SteerBin}
}

func dispositions(t *testing.T, root string) []string {
	t.Helper()
	runs, err := OracleRuns(root)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, r := range runs {
		out = append(out, r.Disposition)
	}
	return out
}

// A failure blocks with its diagnostics. The same failure on the same tree
// then yields even though its output changed; an edit blocks again; a pass
// clears the session, so the old failure blocks once more.
func TestStopBlocksAFailureOnceThenYieldsUntilTheTreeChanges(t *testing.T) {
	root, req := stopRepo(t, map[string]string{
		"a-fails":  "echo \"literal count found in pid $$\" >&2\nexit 1\n",
		"b-passes": "exit 0\n",
	}, "")

	d := Stop(req)
	if d.Disposition != DispositionBlock || d.Run == nil || d.Run.Source != SourceStop || d.Run.Harness != "claude-code" {
		t.Fatalf("first stop %+v; want a block from a stop run", d)
	}
	for _, want := range []string{"a-fails (failed)", "literal count found", "Fix the code"} {
		if !strings.Contains(d.Reason, want) {
			t.Errorf("block reason lacks %q:\n%s", want, d.Reason)
		}
	}
	if strings.Contains(d.Reason, "b-passes") {
		t.Errorf("block reason names the passing check:\n%s", d.Reason)
	}

	if d := Stop(req); d.Disposition != DispositionYield || d.Reason != "" || !strings.Contains(d.Message, "unchanged") {
		t.Fatalf("second stop %+v; want a yield with an operator message", d)
	}

	writeFile(t, root, "src/edit.txt", "a change\n")
	if d := Stop(req); d.Disposition != DispositionBlock {
		t.Fatalf("stop after an edit: %s; want a block", d.Disposition)
	}

	writeFile(t, root, ".steer/checks/a-fails.sh", "exit 0\n")
	if d := Stop(req); d.Disposition != DispositionAllow || d.Reply() != nil {
		t.Fatalf("stop after a fix: %+v; want a silent allow", d)
	}
	writeFile(t, root, ".steer/checks/a-fails.sh", "exit 1\n")
	Stop(req)
	writeFile(t, root, ".steer/checks/a-fails.sh", "exit 0\n")
	Stop(req)
	writeFile(t, root, ".steer/checks/a-fails.sh", "exit 1\n")
	if d := Stop(req); d.Disposition != DispositionBlock {
		t.Fatalf("a failure after a pass: %s; want a block, the pass cleared the retry state", d.Disposition)
	}

	want := []string{DispositionBlock, DispositionYield, DispositionBlock, DispositionAllow, DispositionBlock, DispositionAllow, DispositionBlock}
	if got := dispositions(t, root); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("recorded dispositions %v; want %v", got, want)
	}
}

// Retry state belongs to one session of one harness: a failure another
// session or harness was blocked on still blocks.
func TestStopRetryStateIsPerSessionAndHarness(t *testing.T) {
	_, req := stopRepo(t, map[string]string{"a": "exit 1\n"}, "")
	if d := Stop(req); d.Disposition != DispositionBlock {
		t.Fatalf("first stop: %s", d.Disposition)
	}
	other := req
	other.Session = "session-b"
	if d := Stop(other); d.Disposition != DispositionBlock {
		t.Fatalf("another session: %s; want a block", d.Disposition)
	}
	codex := req
	codex.Harness = "codex"
	if d := Stop(codex); d.Disposition != DispositionBlock {
		t.Fatalf("the same session id from codex: %s; want a block", d.Disposition)
	}
	if d := Stop(req); d.Disposition != DispositionYield {
		t.Fatalf("the first session again: %s; want a yield", d.Disposition)
	}
}

// check.sh that passes without running a declared check blocks, and the
// reason says no assertion failed and names the line to add.
func TestStopBlocksAnIntegrationFailureWithoutClaimingAnAssertionFailed(t *testing.T) {
	_, req := stopRepo(t, map[string]string{"a": "exit 0\n"}, "exit 0\n")
	d := Stop(req)
	if d.Disposition != DispositionBlock || d.Run == nil || !d.Run.IntegrationFailure {
		t.Fatalf("stop %+v; want a block on an integration failure", d)
	}
	for _, want := range []string{"without running these declared checks: a", "No assertion failed", ExecInvocation} {
		if !strings.Contains(d.Reason, want) {
			t.Errorf("reason lacks %q:\n%s", want, d.Reason)
		}
	}
	if strings.Contains(d.Reason, "failed (") {
		t.Errorf("reason reports a failed check:\n%s", d.Reason)
	}
}

// A run that passes its deadline ends the turn with a notice and no block.
func TestStopFailsOpenOnATimeout(t *testing.T) {
	root, req := stopRepo(t, map[string]string{"a": "exit 0\n"}, "sleep 30\n")
	req.Timeout = time.Second
	d := Stop(req)
	if d.Disposition != DispositionTimeout || d.Reason != "" || !strings.Contains(d.Message, "deadline") {
		t.Fatalf("stop %+v; want a timeout notice without a block", d)
	}
	if got := dispositions(t, root); len(got) != 1 || got[0] != DispositionTimeout {
		t.Fatalf("recorded dispositions %v; want [timeout]", got)
	}
}

// A Stop while another run holds the repository records a not-executed run
// naming the holder, writes no check records, and does not pass.
func TestStopReportsAConcurrentRunAsItsOwnState(t *testing.T) {
	root, req := stopRepo(t, map[string]string{"a": "exit 0\n"}, "")
	lock, _, err := acquireRepoLock(root, LockHolder{RunID: "holder-run", PID: os.Getpid(), Source: SourceRun})
	if err != nil {
		t.Fatal(err)
	}
	d := Stop(req)
	lock.release()
	if d.Disposition != DispositionConcurrent || d.Reason != "" || !strings.Contains(d.Message, "holder-run") {
		t.Fatalf("stop %+v; want a concurrent notice naming the holder", d)
	}
	runs, _ := OracleRuns(root)
	if len(runs) != 1 || runs[0].Outcome != OutcomeNotExecuted || runs[0].ConcurrentWith != "holder-run" ||
		runs[0].Disposition != DispositionConcurrent || runs[0].Passed() {
		t.Fatalf("runs %+v; want one not-executed run naming holder-run", runs)
	}
	if stored, _ := Executions(root); len(stored) != 0 {
		t.Fatalf("a refused run wrote %d check records", len(stored))
	}
	if d := Stop(req); d.Disposition != DispositionAllow {
		t.Fatalf("stop after the holder released: %s; want allow", d.Disposition)
	}
}

// check run is refused the same way while another run holds the repository.
func TestRunOracleRefusesAConcurrentRun(t *testing.T) {
	root, opts := oracleRepo(t, map[string]string{"a": "exit 0\n"}, "")
	identity, _ := RepoIdentity(root)
	lock, _, err := acquireRepoLock(identity, LockHolder{RunID: "holder-run", Source: SourceStop})
	if err != nil {
		t.Fatal(err)
	}
	defer lock.release()
	run, records, err := RunOracle(root, opts)
	if !errors.Is(err, ErrConcurrentRun) || run.ConcurrentWith != "holder-run" || run.Outcome != OutcomeNotExecuted || records != nil {
		t.Fatalf("run %+v records %v err %v; want a refusal naming holder-run", run, records, err)
	}
}

// The lock dies with its holder: a killed run leaves the repository free.
func TestAKilledHolderReleasesTheRepositoryLock(t *testing.T) {
	root := checkRepo(t, noCountScript)
	identity, _ := RepoIdentity(root)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := exec.Command(exe)
	child.Env = append(os.Environ(), testLockHolderEnv+"="+identity)
	stdout, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	line, _ := bufio.NewReader(stdout).ReadString('\n')
	if strings.TrimSpace(line) != "locked" {
		child.Process.Kill()
		t.Fatalf("holder said %q", line)
	}
	if _, holder, err := acquireRepoLock(identity, LockHolder{RunID: "second"}); !errors.Is(err, ErrConcurrentRun) || holder.RunID != "held-by-child" {
		child.Process.Kill()
		t.Fatalf("holder %+v err %v; want the child named as holder", holder, err)
	}
	child.Process.Kill()
	child.Wait()
	lock, _, err := acquireRepoLock(identity, LockHolder{RunID: "second"})
	if err != nil {
		t.Fatalf("after the holder was killed: %v", err)
	}
	lock.release()
}

// An unadopted repository never runs check.sh. One that declares Steer checks
// gets one notice per session; a repository without them, none.
func TestStopDoesNotRunAnUnadoptedRepository(t *testing.T) {
	root, opts := oracleRepo(t, map[string]string{"a": "exit 1\n"}, "")
	gitIn(t, root, "init", "-q")
	req := StopRequest{Harness: "codex", Session: "s", CWD: root, Timeout: opts.Timeout, SteerBin: opts.SteerBin}
	d := Stop(req)
	if d.Disposition != DispositionNotAdopted || d.Run != nil || !strings.Contains(d.Message, "steer check adopt") {
		t.Fatalf("first stop %+v; want a not-adopted notice without a run", d)
	}
	if d := Stop(req); d.Disposition != DispositionNotAdopted || d.Message != "" {
		t.Fatalf("second stop %+v; want no second notice", d)
	}
	if runs, _ := OracleRuns(root); len(runs) != 0 {
		t.Fatalf("an unadopted repository recorded %d runs", len(runs))
	}

	plain := t.TempDir()
	gitIn(t, plain, "init", "-q")
	req.CWD = plain
	if d := Stop(req); d.Disposition != DispositionNotAdopted || d.Message != "" {
		t.Fatalf("stop without a manifest %+v; want silence", d)
	}
	req.CWD = t.TempDir()
	if d := Stop(req); d.Disposition != DispositionOutside || d.Reply() != nil {
		t.Fatalf("stop outside a repository %+v; want silence", d)
	}
}

// An adopted repository that lost check.sh gets one notice per session.
func TestStopNoticesAMissingOracleOncePerSession(t *testing.T) {
	root, req := stopRepo(t, map[string]string{"a": "exit 0\n"}, "")
	if err := os.Remove(OraclePath(root)); err != nil {
		t.Fatal(err)
	}
	if d := Stop(req); d.Disposition != DispositionNoOracle || !strings.Contains(d.Message, "no check.sh") {
		t.Fatalf("first stop %+v; want a no-oracle notice", d)
	}
	if d := Stop(req); d.Disposition != DispositionNoOracle || d.Message != "" {
		t.Fatalf("second stop %+v; want no second notice", d)
	}
}

// A machine where bash cannot be found lets the turn end with a notice every
// time and records no run, so a missing shell never reads as a pass.
func TestStopFailsOpenWithoutBash(t *testing.T) {
	root, req := stopRepo(t, map[string]string{"a": "exit 1\n"}, "")
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not found")
	}
	onlyGit := t.TempDir()
	if err := os.Symlink(git, filepath.Join(onlyGit, "git")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", onlyGit)
	for range 2 {
		if d := Stop(req); d.Disposition != DispositionNoBash || d.Run != nil || !strings.Contains(d.Message, "bash was not found") {
			t.Fatalf("stop %+v; want a no-bash notice without a run", d)
		}
	}
	if runs, _ := OracleRuns(root); len(runs) != 0 {
		t.Fatalf("a machine without bash recorded %d runs", len(runs))
	}
}

// The hook acts on the root Stop event only, reports a malformed payload to
// the operator, and replies to a failure with a block in the shared shape.
func TestStopHookAnswersOnlyTheRootStopEvent(t *testing.T) {
	root, req := stopRepo(t, map[string]string{"a": "exit 1\n"}, "")
	opts := StopHookOptions{Timeout: req.Timeout, SteerBin: req.SteerBin}
	payload := func(event, agent string) []byte {
		raw, _ := json.Marshal(StopPayload{HookEventName: event, SessionID: "s", CWD: root, AgentID: agent})
		return raw
	}
	for _, raw := range [][]byte{payload("SubagentStop", ""), payload("Stop", "agent-1")} {
		if d, reply := StopHook("codex", raw, opts); d.Disposition != DispositionIgnored || reply != nil {
			t.Fatalf("%s: %+v %+v; want it ignored", raw, d, reply)
		}
	}
	if runs, _ := OracleRuns(root); len(runs) != 0 {
		t.Fatalf("a subagent event ran check.sh %d times", len(runs))
	}
	if d, reply := StopHook("codex", []byte("{"), opts); d.Disposition != DispositionBadContext || reply == nil || reply.Decision != "" || reply.SystemMessage == "" {
		t.Fatalf("malformed payload: %+v %+v; want an operator notice", d, reply)
	}
	if d, reply := StopHook("codex", payload("Stop", ""), opts); d.Disposition != DispositionBlock || reply == nil ||
		reply.Decision != "block" || reply.Reason != d.Reason || d.Run.Harness != "codex" {
		t.Fatalf("root stop: %+v %+v; want a block reply from a codex run", d, reply)
	}
}

// A Stop run records the payload's transcript locators by key presence and
// whether a Stop hook already blocked the turn. A block keeps the reason it
// sent, and that reason names the run, so the transcript entry joins to it.
func TestStopRecordsWhereItsBlockLandsInTheTranscript(t *testing.T) {
	root, req := stopRepo(t, map[string]string{"a": "exit 1\n"}, "")
	opts := StopHookOptions{Timeout: req.Timeout, SteerBin: req.SteerBin}
	claude := fmt.Sprintf(`{"hook_event_name":"Stop","session_id":"s1","cwd":%q,`+
		`"transcript_path":"/t/s1.jsonl","prompt_id":"p-1","stop_hook_active":false}`, root)
	codex := fmt.Sprintf(`{"hook_event_name":"Stop","session_id":"s2","cwd":%q,`+
		`"transcript_path":"/t/rollout.jsonl","turn_id":null}`, root)

	d, reply := StopHook("claude-code", []byte(claude), opts)
	if d.Disposition != DispositionBlock || reply == nil {
		t.Fatalf("claude stop: %+v; want a block", d)
	}
	StopHook("codex", []byte(codex), opts)

	runs, err := OracleRuns(root)
	if err != nil || len(runs) != 2 {
		t.Fatalf("runs %d (%v); want 2", len(runs), err)
	}
	first, second := runs[0], runs[1]
	if first.Version != 2 || first.BlockReason != reply.Reason {
		t.Errorf("recorded block reason %q (version %d); want the reply's reason %q", first.BlockReason, first.Version, reply.Reason)
	}
	if !strings.HasSuffix(reply.Reason, "\n"+runIDLabel+first.RunID) {
		t.Errorf("block reason does not end naming run %s:\n%s", first.RunID, reply.Reason)
	}
	if got := fmt.Sprint(first.Locators); got != "map[prompt_id:p-1 transcript_path:/t/s1.jsonl]" {
		t.Errorf("claude locators %s", got)
	}
	if first.StopHookActive == nil || *first.StopHookActive {
		t.Errorf("claude stop_hook_active %v; want a recorded false", first.StopHookActive)
	}
	if got := fmt.Sprint(second.Locators); got != "map[transcript_path:/t/rollout.jsonl turn_id:]" {
		t.Errorf("codex locators %s; want a null turn_id kept as present and empty", got)
	}
	if second.StopHookActive != nil {
		t.Errorf("codex stop_hook_active %v; want absent, the payload carried none", *second.StopHookActive)
	}
}

// Every run records the working tree it judged, allow and yield included, so a
// later reader can tell a turn that changed nothing from one that fixed code.
func TestStopRecordsTheTreeEachRunJudged(t *testing.T) {
	root, req := stopRepo(t, map[string]string{"a": "exit 1\n"}, "")
	Stop(req)
	Stop(req)
	writeFile(t, root, "src/edit.txt", "a change\n")
	Stop(req)
	writeFile(t, root, ".steer/checks/a.sh", "exit 0\n")
	Stop(req)

	runs, err := OracleRuns(root)
	if err != nil || len(runs) != 4 {
		t.Fatalf("runs %d (%v); want 4", len(runs), err)
	}
	for i, r := range runs {
		if r.Tree == "" {
			t.Errorf("run %d (%s) recorded no tree", i, r.Disposition)
		}
	}
	if runs[0].Tree != runs[1].Tree {
		t.Errorf("block and yield on one tree recorded %s and %s", runs[0].Tree, runs[1].Tree)
	}
	if runs[1].Tree == runs[2].Tree || runs[2].Tree == runs[3].Tree {
		t.Errorf("an edit left the recorded tree unchanged: %s %s %s", runs[1].Tree, runs[2].Tree, runs[3].Tree)
	}
}

// A block offers the blocked agent a contest naming its run, just above the
// run line, and the run records that the offer was made; a yield offers none.
func TestStopBlockOffersAContestOfItsRun(t *testing.T) {
	root, req := stopRepo(t, map[string]string{"a": "exit 1\n"}, "")
	block := Stop(req)
	if block.Disposition != DispositionBlock {
		t.Fatalf("first stop: %+v; want a block", block)
	}
	lines := strings.Split(block.Reason, "\n")
	contest := lines[len(lines)-2]
	if !strings.Contains(contest, "contest --run "+block.Run.RunID+" '<task-specific reason>'") ||
		!strings.Contains(contest, "The block stays.") || lines[len(lines)-3] != "" ||
		lines[len(lines)-1] != RunIDLine(block.Run.RunID) {
		t.Errorf("reason tail = %q; want a blank line, the contest line, then the run line last", lines[len(lines)-3:])
	}
	yield := Stop(req)
	runs, _ := OracleRuns(root)
	if yield.Disposition != DispositionYield || len(runs) != 2 {
		t.Fatalf("second stop: %+v, %d runs; want a recorded yield", yield, len(runs))
	}
	if runs[0].ContestProtocol != ContestProtocol || runs[1].ContestProtocol != 0 {
		t.Errorf("contest protocols = %d, %d; want the block's offer recorded and none on the yield",
			runs[0].ContestProtocol, runs[1].ContestProtocol)
	}
}

// A check check.sh never reached asserted nothing, so the reason lists it apart
// from the failures and says so, instead of as a check that did not pass.
func TestStopBlockListsAnUnreachedCheckApartFromFailures(t *testing.T) {
	_, req := stopRepo(t, map[string]string{"a": "exit 0\n"}, "echo 'gofmt: needs formatting' >&2\nexit 1\n")
	d := Stop(req)
	if d.Disposition != DispositionBlock {
		t.Fatalf("stop: %+v; want a block", d)
	}
	if strings.Contains(d.Reason, "failed: a") || strings.Contains(d.Reason, "did not pass") {
		t.Errorf("reason lists the unreached check as a failure:\n%s", d.Reason)
	}
	if !strings.Contains(d.Reason, "not reached, because check.sh stopped before them: a. They asserted nothing.") {
		t.Errorf("reason does not name the unreached check:\n%s", d.Reason)
	}
}
