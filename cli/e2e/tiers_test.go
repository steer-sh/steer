//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	todoCheck  = "no-todo"
	todoScript = ".steer/checks/no-todo.sh"
	todoClass  = "TODO left in src"
	todoOutput = "Exit code 1\nsrc/a.txt:1:TODO fix\nFAIL: " + todoClass
)

// DeclareTodoCheck declares a script check that fails on a TODO under src/, adopts the
// repository, registers the Claude Code Stop runner and returns its registered command.
func (s *Sandbox) DeclareTodoCheck() string {
	s.t.Helper()
	s.MustSteer("check", "init")
	s.Write(todoScript, "#!/usr/bin/env bash\nset -euo pipefail\n"+
		"for f in src/*; do\n  [ -f \"$f\" ] || continue\n"+
		"  case \"$(<\"$f\")\" in *TODO*) echo \"FAIL: "+todoClass+"\" >&2; exit 1;; esac\ndone\n")
	if err := os.Chmod(filepath.Join(s.Repo, todoScript), 0o755); err != nil {
		s.t.Fatal(err)
	}
	cases, _ := json.Marshal(map[string]any{"version": 1, "cases": []any{
		map[string]any{"name": "clean", "exit_code": 0, "provenance": "a tree with no TODO",
			"files": map[string]string{"src/a.txt": "done\n"}},
		map[string]any{"name": "todo", "exit_code": 1, "diagnostic": todoClass, "provenance": "a TODO left behind",
			"files": map[string]string{"src/a.txt": "TODO fix\n"}},
	}})
	s.Write(".steer/checks/cases/"+todoCheck+".json", string(cases))
	s.MustSteer("check", "add", todoCheck, "--script", todoScript)
	s.MustSteer("check", "adopt")
	s.MustSteer("bin", "install")
	s.MustSteer("check", "install", "--harness", "claude-code")
	return s.RegisteredCommand(filepath.Join(s.Repo, ".claude", "settings.json"), "check hook")
}

// StopReply is the parsed reply of a Stop hook.
type StopReply struct {
	Decision, Reason, System string
}

// Stop sends a root Stop event for session through the registered command, as the
// harness does at a turn end. active is the harness's stop_hook_active flag.
func (s *Sandbox) Stop(command, session string, active bool) StopReply {
	s.t.Helper()
	payload, _ := json.Marshal(map[string]any{
		"hook_event_name": "Stop", "session_id": session, "cwd": s.Repo,
		"transcript_path": s.SessionPath(session), "stop_hook_active": active,
	})
	r := s.Run(s.Repo, string(payload), nil, "sh", "-c", command)
	if r.Code != 0 {
		s.t.Fatalf("the Stop hook exited %d:\n%s", r.Code, r.Output())
	}
	var reply StopReply
	if strings.TrimSpace(r.Stdout) == "" {
		return reply
	}
	var out struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
		System   string `json:"systemMessage"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &out); err != nil {
		s.t.Fatalf("Stop reply: %v\n%s", err, r.Stdout)
	}
	return StopReply{out.Decision, out.Reason, out.System}
}

// runID reads the run id the Stop block reason ends with.
func runID(t *testing.T, reason string) string {
	t.Helper()
	_, id, ok := strings.Cut(reason, "steer check run: ")
	if !ok {
		t.Fatalf("the block names no run:\n%s", reason)
	}
	return strings.TrimSpace(id)
}

// todoFixArgs is `steer fix register` on the session corpus for the no-todo class,
// scoped to the sandbox repository on Claude Code.
func (s *Sandbox) todoFixArgs(tier string, installed int64, extra ...string) []string {
	return append([]string{"fix", "register", "--tier", tier,
		"--corpus", "transcript-friction", "--signal-kind", "tool_failures",
		"--class", "todo-left-" + tier, "--match", todoClass,
		"--problem", "Agents leave TODO markers in src.",
		"--intervention", "A fix keeps TODO markers out of src.",
		"--scope-repo-root", s.Repo, "--scope-harness", "claude-code",
		"--installed-ts", fmt.Sprint(installed)}, extra...)
}

// TestCheckChainSessionCorpus drives Suite A step 8's check tier through the binary: a
// failure before registration lands in the baseline, the record freezes the check it
// names, and a Stop block after registration is an execution joined to its session
// and a case in the audit queue that contest, exclude and reinstate name by run.
//
// pins existing behaviour
func TestCheckChainSessionCorpus(t *testing.T) {
	t.Parallel()
	s := NewSandbox(t)
	stop := s.DeclareTodoCheck()
	s.Write("src/a.txt", "TODO fix\n")
	s.SeedBashSession("sess-before", "./check.sh", todoOutput)
	installed := nextSecond()

	s.MustSteer("report", "refresh")
	match := s.MustSteer("fix", "match", todoClass, "--corpus", "transcript-friction",
		"--signal-kind", "tool_failures", "--scope-repo-root", s.Repo, "--scope-harness", "claude-code",
		"--installed-ts", fmt.Sprint(installed))
	if !strings.Contains(match.Stdout, "registration baseline: 1/1 sessions") {
		t.Errorf("fix match did not preview a 1/1 baseline:\n%s", match.Stdout)
	}
	s.MustSteer(s.todoFixArgs("check", installed, "--check-id", todoCheck, "--target", todoScript)...)
	records := s.Records()
	if len(records) != 1 {
		t.Fatalf("inventory holds %d records, want 1", len(records))
	}
	rec := records[0]
	for k, v := range map[string]any{"check_id": todoCheck, "check_kind": "script", "check_repo": s.Repo,
		"check_host": "e2e", "installed_ts": float64(installed)} {
		if rec[k] != v {
			t.Errorf("record %s = %v, want %v", k, rec[k], v)
		}
	}
	if rec["check_digest"] == "" || fmt.Sprint(rec["check_harness"]) != "[claude-code]" {
		t.Errorf("record check_digest %q, check_harness %v", rec["check_digest"], rec["check_harness"])
	}
	if b := dig(rec, "baseline", "hits"); b != float64(1) {
		t.Errorf("frozen baseline hits = %v, want the 1 the preview showed", b)
	}

	s.SeedBashSession("sess-after", "./check.sh", todoOutput)
	block := s.Stop(stop, "sess-after", false)
	if block.Decision != "block" || !strings.Contains(block.Reason, "Declared checks that failed: no-todo (failed).") {
		t.Fatalf("Stop did not block on the failing check: %+v", block)
	}
	run := runID(t, block.Reason)
	s.MustSteer("report", "refresh")
	obs := s.scoredObservation()
	for path, v := range map[string]any{
		"counts.split_ts":         float64(installed),
		"counts.before.hits":      float64(1),
		"counts.after.hits":       float64(1),
		"checks.applicable":       true,
		"checks.stream_present":   true,
		"checks.scope":            "eligible_units",
		"checks.executions":       float64(1),
		"checks.outcomes.failed":  float64(1),
		"checks.blocked":          float64(1),
		"checks.units":            float64(1),
		"checks.changed_detector": float64(0),
		"firing.applicable":       false,
	} {
		if got := dig(obs, strings.Split(path, ".")...); got != v {
			t.Errorf("observation %s = %v, want %v", path, got, v)
		}
	}

	if blocks := s.MustSteer("check", "blocks"); !strings.Contains(blocks.Stdout, "run "+run+" blocked a claude-code turn (session sess-after)") {
		t.Errorf("check blocks omits run %s:\n%s", run, blocks.Stdout)
	}
	queued := func() (map[string]any, []string) {
		r := s.MustSteer("audit", "queue", "--json")
		var q struct {
			Counts map[string]any `json:"counts"`
			Items  []struct {
				RunID string `json:"run_id"`
			} `json:"items"`
		}
		if err := json.Unmarshal([]byte(r.Stdout), &q); err != nil {
			t.Fatalf("audit queue --json: %v\n%s", err, r.Stdout)
		}
		var ids []string
		for _, it := range q.Items {
			ids = append(ids, it.RunID)
		}
		return q.Counts, ids
	}
	if counts, ids := queued(); counts["queued"] != float64(1) || fmt.Sprint(ids) != "["+run+"]" {
		t.Fatalf("queue = %v %v, want the one block %s", counts, ids, run)
	}
	s.MustSteer("contest", "--run", run, "the TODO belongs to a vendored file")
	if counts, _ := queued(); counts["queued"] != float64(1) {
		t.Errorf("a contest changed the queue: %v", counts)
	}
	if c := s.MustSteer("audit", "contests", "--json"); !strings.Contains(c.Stdout, `"contested": 1`) {
		t.Errorf("audit contests does not list the contested block:\n%s", c.Stdout)
	}
	s.MustSteer("audit", "exclude", run, "--reason", "synthetic e2e block", "--evidence", "a test sent the Stop event")
	if counts, ids := queued(); counts["queued"] != float64(0) || counts["excluded"] != float64(1) || len(ids) != 0 {
		t.Errorf("after exclude the queue = %v %v, want none queued and one excluded", counts, ids)
	}
	if rows := s.MustSteer("audit", "rows", "--excluded", "--json"); !strings.Contains(rows.Stdout, run) {
		t.Errorf("audit rows --excluded omits run %s:\n%s", run, rows.Stdout)
	}
	s.MustSteer("audit", "reinstate", run, "--reason", "the block was real work")
	if counts, _ := queued(); counts["queued"] != float64(1) || counts["excluded"] != float64(0) {
		t.Errorf("after reinstate the queue = %v, want the block back", counts)
	}
}

// TestStopRetryBlocksAgainAfterADetectorModeChange checks that a repeated Stop over an
// unchanged tree yields, and that a mode-only change to the uncommitted detector is a
// new tree that blocks again.
//
// pins existing behaviour
func TestStopRetryBlocksAgainAfterADetectorModeChange(t *testing.T) {
	t.Parallel()
	s := NewSandbox(t)
	stop := s.DeclareTodoCheck()
	s.Write("src/a.txt", "TODO fix\n")
	if r := s.Stop(stop, "sess-1", false); r.Decision != "block" {
		t.Fatalf("the first Stop did not block: %+v", r)
	}
	if r := s.Stop(stop, "sess-1", true); r.Decision != "" || !strings.Contains(r.System, "check.sh still fails") {
		t.Fatalf("a retry over the same tree and failure did not yield: %+v", r)
	}
	if err := os.Chmod(filepath.Join(s.Repo, todoScript), 0o644); err != nil {
		t.Fatal(err)
	}
	if r := s.Stop(stop, "sess-1", true); r.Decision != "block" {
		t.Errorf("a detector that lost its executable bit did not block again: %+v", r)
	}
}

// TestDeclaredGoTestNeedsAPassingResult checks that a zero-exit check.sh still blocks
// when a declared Go test reports no result, here because it was renamed.
//
// pins existing behaviour
func TestDeclaredGoTestNeedsAPassingResult(t *testing.T) {
	t.Parallel()
	s := NewSandbox(t, WithGo())
	const mod = "module example.com/m\n\ngo 1.21\n"
	add := func(op string) string { return "package calc\n\nfunc Add(a, b int) int { return a " + op + " b }\n" }
	test := func(name string) string {
		return "package calc\n\nimport \"testing\"\n\nfunc " + name +
			"(t *testing.T) {\n\tif Add(2, 2) != 4 {\n\t\tt.Fatal(\"Add is wrong\")\n\t}\n}\n"
	}
	s.MustSteer("check", "init")
	sh, err := os.ReadFile(filepath.Join(s.Repo, "check.sh"))
	if err != nil {
		t.Fatal(err)
	}
	exec := "\"$steer\" check exec --all || status=1\n"
	s.Write("check.sh", strings.Replace(string(sh), exec, exec+"\"$steer\" check go-test -- ./... || status=1\n", 1))
	s.Write("go.mod", mod)
	s.Write("calc/calc.go", add("+"))
	s.Write("calc/calc_test.go", test("TestAdd"))
	cases, _ := json.Marshal(map[string]any{"version": 1, "cases": []any{
		map[string]any{"name": "good", "exit_code": 0, "provenance": "Add adds",
			"files": map[string]string{"go.mod": mod, "calc/calc.go": add("+")}},
		map[string]any{"name": "bad", "exit_code": 1, "diagnostic": "Add is wrong", "provenance": "Add subtracts",
			"files": map[string]string{"go.mod": mod, "calc/calc.go": add("-")}},
	}})
	s.Write(".steer/checks/cases/add.json", string(cases))
	s.MustSteer("check", "add", "add", "--go-package", "calc", "--go-test", "TestAdd")
	s.MustSteer("check", "adopt")
	s.MustSteer("bin", "install")
	s.MustSteer("check", "install", "--harness", "claude-code")
	stop := s.RegisteredCommand(filepath.Join(s.Repo, ".claude", "settings.json"), "check hook")

	if r := s.Stop(stop, "sess-1", false); r.Decision != "" {
		t.Fatalf("Stop blocked a passing declared test: %+v", r)
	}
	s.Write("calc/calc_test.go", test("TestSum"))
	r := s.Stop(stop, "sess-2", false)
	for _, want := range []string{"check.sh exited zero", "add (unavailable)", "renamed"} {
		if r.Decision != "block" || !strings.Contains(r.Reason, want) {
			t.Errorf("Stop over a renamed declared test lacks a block naming %q: %+v", want, r)
		}
	}
}

// TestInstructionChainNeedsANotMechanizableReason checks Suite A step 8's instruction
// tier and step 9's refusals: a record names its installed rule and why no check or
// guard can detect the class, a blank or class-restating reason is refused, and a
// rule id held by both stores is refused until the scope is named.
//
// pins existing behaviour
func TestInstructionChainNeedsANotMechanizableReason(t *testing.T) {
	t.Parallel()
	s := NewSandbox(t)
	const rule = "never leave a TODO in src | finish the work or file an issue"
	s.MustSteer("rules", "add", "no-todo-rule", "--rule", rule)
	s.SeedBashSession("sess-before", "./check.sh", todoOutput)
	installed := nextSecond()
	s.MustSteer("report", "refresh")
	args := func(extra ...string) []string {
		return s.todoFixArgs("instruction", installed, append([]string{"--rule-id", "no-todo-rule", "--target", "CLAUDE.md"}, extra...)...)
	}

	for name, c := range map[string]struct {
		args []string
		want string
	}{
		"missing":   {args(), "--not-mechanizable is required"},
		"blank":     {args("--not-mechanizable", "   "), "--not-mechanizable is required"},
		"the class": {args("--not-mechanizable", "TODO-LEFT-INSTRUCTION"), "--not-mechanizable repeats the class name"},
	} {
		if r := s.Steer(c.args...); r.Code == 0 || !strings.Contains(r.Output(), c.want) {
			t.Errorf("%s reason: exit %d, want a refusal naming %q:\n%s", name, r.Code, c.want, r.Output())
		}
	}
	if n := len(s.Records()); n != 0 {
		t.Fatalf("the refused registrations wrote %d records", n)
	}

	const why = "Whether a TODO is deliberate needs the author's intent, which no check or guard can read."
	s.MustSteer(args("--not-mechanizable", why)...)
	rec := s.Records()[0]
	for k, v := range map[string]any{"tier": "instruction", "rule_id": "no-todo-rule", "rule_scope": "project",
		"not_mechanizable": why} {
		if rec[k] != v {
			t.Errorf("record %s = %v, want %v", k, rec[k], v)
		}
	}
	if b := dig(rec, "baseline", "hits"); b != float64(1) {
		t.Errorf("frozen baseline hits = %v, want 1", b)
	}

	s.MustSteer("rules", "add", "no-todo-rule", "--rule", rule, "--scope", "global")
	r := s.Steer(args("--not-mechanizable", why, "--class", "todo-both")...)
	if r.Code == 0 || !strings.Contains(r.Output(), "--rule-scope") {
		t.Errorf("exit %d; a rule id held by both stores was not refused by naming --rule-scope:\n%s", r.Code, r.Output())
	}
	s.MustSteer(args("--not-mechanizable", why, "--class", "todo-global", "--rule-scope", "global")...)
	if recs := s.Records(); len(recs) != 2 || recs[1]["rule_scope"] != "global" {
		t.Errorf("records = %v, want the second bound to the global rule", recs)
	}
}

// TestReviewTierBindsOnlyACommittedAddendum checks Suite A step 8's review tier: a
// project addendum entry binds only when HEAD holds it, because the gate reads project
// entries from HEAD.
//
// pins existing behaviour
func TestReviewTierBindsOnlyACommittedAddendum(t *testing.T) {
	t.Parallel()
	s := NewSandbox(t)
	const text = "Raise a finding only when you can name the input that fails."
	s.MustSteer("commit-gate", "addendum", "add", "vague-claims", "--text", text)
	args := []string{"fix", "register", "--tier", "review", "--addendum-id", "vague-claims",
		"--kind", "false-positive", "--class", "vague-claim", "--match", "names no failing input",
		"--problem", "Reviewers raise findings that name no failing input.",
		"--intervention", "The review addendum asks for a failing input.",
		"--target", ".steer/hooks/steer-manifest.json", "--scope-repo-root", s.Repo, "--force"}
	if r := s.Steer(args...); r.Code == 0 || !strings.Contains(r.Output(), "is not at HEAD") {
		t.Fatalf("exit %d; an uncommitted addendum entry was not refused:\n%s", r.Code, r.Output())
	}
	if n := len(s.Records()); n != 0 {
		t.Fatalf("the refused registration wrote %d records", n)
	}
	s.Git("add", ".steer/hooks/steer-manifest.json")
	s.Git("commit", "-q", "-m", "review addendum")
	s.MustSteer(args...)
	rec := s.Records()[0]
	for k, v := range map[string]any{"tier": "review", "corpus": "review-findings", "unit": "chain",
		"addendum_id": "vague-claims", "addendum_scope": "project", "addendum": text, "addendum_repo": s.Repo} {
		if rec[k] != v {
			t.Errorf("record %s = %v, want %v", k, rec[k], v)
		}
	}
}
