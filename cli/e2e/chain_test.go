//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	noVarHook = "no-var-hook"
	noVarRule = "never use var | use const or let"
	noVarSrc  = `#!/usr/bin/env python3
import sys
import json

def main():
    data = json.load(sys.stdin)
    command = data.get("tool_input", {}).get("command", "")
    if "var " in command:
        print(json.dumps({"decision": "block", "reason": "never use var"}))
        sys.exit(0)
    sys.exit(0)

if __name__ == "__main__":
    main()
`
	noVarClass = "steer hook: " + noVarHook
)

// RegisterNoVar registers the no-var guard into the project store for one harness.
func (s *Sandbox) RegisterNoVar(harness string) {
	s.t.Helper()
	src := s.path(noVarHook + ".py")
	if err := os.WriteFile(src, []byte(noVarSrc), 0o644); err != nil {
		s.t.Fatal(err)
	}
	s.MustSteer("register", src, "--rule", noVarRule, "--harness", harness)
}

// FireNoVar runs the project master on a Claude Code payload that the guard blocks
// and returns the deny reason.
func (s *Sandbox) FireNoVar(session string) string {
	s.t.Helper()
	payload, _ := json.Marshal(map[string]any{
		"hook_event_name": "PreToolUse", "tool_name": "Bash",
		"tool_input": map[string]any{"command": "var x = 1"},
		"session_id": session, "cwd": s.Repo,
	})
	r := s.Run(s.Repo, string(payload), nil, "python3", filepath.Join(s.Repo, ".steer", "hooks", "steer-hooks.py"))
	if r.Code != 0 {
		s.t.Fatalf("master exited %d:\n%s", r.Code, r.Output())
	}
	var out struct {
		HookSpecificOutput struct {
			PermissionDecision       string `json:"permissionDecision"`
			PermissionDecisionReason string `json:"permissionDecisionReason"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &out); err != nil || out.HookSpecificOutput.PermissionDecision != "deny" {
		s.t.Fatalf("master did not deny (%v):\n%s", err, r.Output())
	}
	return out.HookSpecificOutput.PermissionDecisionReason
}

// SeedBlockedSession writes a Claude Code transcript in which one Bash call came back
// blocked with reason, under the project directory Claude Code would use for the
// repository.
func (s *Sandbox) SeedBlockedSession(session, reason string) {
	s.t.Helper()
	s.SeedBashSession(session, "var x = 1", "PreToolUse:Bash hook error: "+reason)
}

// SessionPath is where Claude Code keeps the transcript of a session started in the
// repository.
func (s *Sandbox) SessionPath(session string) string {
	slug := strings.NewReplacer("/", "-", ".", "-").Replace(s.Repo)
	return filepath.Join(s.Home, ".claude", "projects", slug, session+".jsonl")
}

// SeedBashSession writes a Claude Code transcript in which one Bash call running
// command came back as an error carrying result.
func (s *Sandbox) SeedBashSession(session, command, result string) {
	s.t.Helper()
	now := time.Now().UTC()
	ts := func(ms int) string {
		return now.Add(time.Duration(ms) * time.Millisecond).Format("2006-01-02T15:04:05.000Z")
	}
	base := func(kind string, ms int, message map[string]any) map[string]any {
		return map[string]any{"type": kind, "sessionId": session, "cwd": s.Repo, "timestamp": ts(ms), "message": message}
	}
	s.writeTranscript(s.SessionPath(session), []map[string]any{
		base("user", 0, map[string]any{"role": "user", "content": "run " + command}),
		base("assistant", 1, map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "tool_use", "id": "t1", "name": "Bash", "input": map[string]any{"command": command}}}}),
		base("user", 2, map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "tool_result", "tool_use_id": "t1", "is_error": true, "content": result}}}),
	})
}

// writeTranscript writes one JSON object per line to path.
func (s *Sandbox) writeTranscript(path string, lines []map[string]any) {
	s.t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		s.t.Fatal(err)
	}
	var b strings.Builder
	for _, line := range lines {
		data, _ := json.Marshal(line)
		b.Write(data)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		s.t.Fatal(err)
	}
}

// nextSecond waits for the next whole second and returns it, so a split at that
// second falls strictly after everything already written, including the 2 ms that
// SeedBashSession stamps ahead of the clock.
func nextSecond() int64 {
	next := time.Now().Add(2*time.Millisecond).Unix() + 1
	time.Sleep(time.Until(time.Unix(next, 0)))
	return next
}

// noVarFixArgs is `steer fix register` for the no-var guard on the session corpus.
func noVarFixArgs(installed int64, extra ...string) []string {
	return append([]string{"fix", "register", "--tier", "guard", "--hook-id", noVarHook,
		"--corpus", "transcript-friction", "--signal-kind", "hook_blocks",
		"--class", "var-decl", "--match", noVarClass,
		"--problem", "Agents declare JavaScript variables with var.",
		"--intervention", "A guard blocks var declarations in shell commands.",
		"--target", ".steer/hooks/" + noVarHook + ".py",
		"--installed-ts", fmt.Sprint(installed)}, extra...)
}

// Records reads the sandbox's intervention inventory.
func (s *Sandbox) Records() []map[string]any {
	s.t.Helper()
	return readJSONL(s.t, filepath.Join(s.Home, ".steer", "state", "commit-review", "interventions.jsonl"))
}

// AuditRows reads the project store's guard audit log.
func (s *Sandbox) AuditRows() []map[string]any {
	s.t.Helper()
	return readJSONL(s.t, filepath.Join(s.Repo, ".steer", "hooks", "steer-audit.jsonl"))
}

func readJSONL(t *testing.T, path string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var row map[string]any
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		rows = append(rows, row)
	}
	return rows
}

// scoredObservation runs `steer fix score --json` and returns the one record's
// observation.
func (s *Sandbox) scoredObservation() map[string]any {
	s.t.Helper()
	r := s.MustSteer("fix", "score", "--json")
	var records []map[string]any
	if err := json.Unmarshal([]byte(r.Stdout), &records); err != nil || len(records) != 1 {
		s.t.Fatalf("fix score --json: %v, %d record(s):\n%s", err, len(records), r.Stdout)
	}
	obs, _ := records[0]["observation"].(map[string]any)
	return obs
}

func dig(m map[string]any, keys ...string) any {
	var v any = m
	for _, k := range keys {
		next, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = next[k]
	}
	return v
}

// TestGuardChainSessionCorpus drives Suite A's guard chain on the transcript-friction
// corpus: register refuses a guard that blocked before the cutoff unless forced, a
// firing before registration lands in the baseline window, the match preview equals
// the frozen baseline, and the firing after registration joins its root session.
//
// pins existing behaviour
func TestGuardChainSessionCorpus(t *testing.T) {
	t.Parallel()
	s := NewSandbox(t)
	s.RegisterNoVar("claude-code")

	reason := s.FireNoVar("sess-before")
	for _, want := range []string{"never use var", noVarClass, "steer contest "} {
		if !strings.Contains(reason, want) {
			t.Errorf("deny reason lacks %q:\n%s", want, reason)
		}
	}
	s.SeedBlockedSession("sess-before", reason)
	installed := nextSecond()

	s.MustSteer("report", "refresh")
	match := s.MustSteer("fix", "match", noVarClass, "--corpus", "transcript-friction",
		"--signal-kind", "hook_blocks", "--scope-harness", "claude-code",
		"--installed-ts", fmt.Sprint(installed))
	if !strings.Contains(match.Stdout, "registration baseline: 1/1 sessions") {
		t.Errorf("fix match did not preview a 1/1 baseline:\n%s", match.Stdout)
	}

	refused := s.Steer(noVarFixArgs(installed, "--scope-harness", "claude-code")...)
	if refused.Code == 0 || !strings.Contains(refused.Output(), "already blocked work on this machine") {
		t.Fatalf("register accepted a guard whose block precedes the cutoff:\n%s", refused.Output())
	}
	s.MustSteer(noVarFixArgs(installed, "--scope-harness", "claude-code", "--force")...)
	records := s.Records()
	if len(records) != 1 {
		t.Fatalf("inventory holds %d records, want 1", len(records))
	}
	rec := records[0]
	audit := s.AuditRows()
	if len(audit) != 1 {
		t.Fatalf("audit log holds %d rows before the second firing, want 1", len(audit))
	}
	want := map[string]any{
		"hook_id": noVarHook, "hook_scope": "project", "hook_repo": s.Repo, "hook_host": "e2e",
		"rule": noVarRule, "rule_hash": audit[0]["rule_hash"], "hook_digest": audit[0]["hook_digest"],
		"installed_ts": float64(installed),
	}
	for k, v := range want {
		if rec[k] != v {
			t.Errorf("record %s = %v, want %v", k, rec[k], v)
		}
	}
	if b := dig(rec, "baseline", "hits"); b != float64(1) {
		t.Errorf("frozen baseline hits = %v, want the 1 the preview showed", b)
	}
	if h := dig(rec, "scope", "harness"); fmt.Sprint(h) != "[claude-code]" {
		t.Errorf("frozen scope harness = %v", h)
	}

	s.SeedBlockedSession("sess-after", s.FireNoVar("sess-after"))
	s.MustSteer("report", "refresh")
	obs := s.scoredObservation()
	checks := map[string]any{
		"counts.split_ts":     float64(installed),
		"counts.before.hits":  float64(1),
		"counts.after.hits":   float64(1),
		"firing.applicable":   true,
		"firing.log_present":  true,
		"firing.class_scoped": true,
		"firing.rows":         float64(1),
		"firing.units":        float64(1),
	}
	for path, v := range checks {
		if got := dig(obs, strings.Split(path, ".")...); got != v {
			t.Errorf("observation %s = %v, want %v", path, got, v)
		}
	}
}

// TestGuardFixRefusesAnUnguardedHarness checks that a guard-tier record cannot claim
// a harness the guard is not registered in.
//
// pins existing behaviour
func TestGuardFixRefusesAnUnguardedHarness(t *testing.T) {
	t.Parallel()
	s := NewSandbox(t)
	s.RegisterNoVar("claude-code")
	r := s.Steer(noVarFixArgs(time.Now().Unix(), "--scope-harness", "codex")...)
	if r.Code == 0 || !strings.Contains(r.Output(), "names codex, where this guard does not run") {
		t.Fatalf("exit %d; a guard record scoped to unguarded codex was not refused by name:\n%s", r.Code, r.Output())
	}
	if len(s.Records()) != 0 {
		t.Errorf("the refused registration wrote a record")
	}
}

// TestGuardFixWithoutAnAuditLogIsUnknownActivity checks that a guard that never fired
// on this machine reads as unknown activity, never as a known zero.
//
// pins existing behaviour
func TestGuardFixWithoutAnAuditLogIsUnknownActivity(t *testing.T) {
	t.Parallel()
	s := NewSandbox(t)
	s.RegisterNoVar("claude-code")
	s.SeedBlockedSession("sess-before", "never use var\n\n— "+noVarClass)
	installed := nextSecond()
	s.MustSteer("report", "refresh")
	s.MustSteer(noVarFixArgs(installed, "--scope-harness", "claude-code")...)
	if rows := s.AuditRows(); rows != nil {
		t.Fatalf("the audit log exists with %d rows", len(rows))
	}
	obs := s.scoredObservation()
	if got := dig(obs, "firing", "log_present"); got != false {
		t.Errorf("firing.log_present = %v with no audit log, want false", got)
	}
	if got := dig(obs, "firing", "applicable"); got != true {
		t.Errorf("firing.applicable = %v, want true for a guard record", got)
	}
}

// auditQueue runs `steer audit queue --json` and returns its counts and listed ids.
func (s *Sandbox) auditQueue() (counts map[string]any, ids []string) {
	s.t.Helper()
	r := s.MustSteer("audit", "queue", "--json")
	var q struct {
		Counts map[string]any `json:"counts"`
		Items  []struct {
			FiringID string `json:"firing_id"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &q); err != nil {
		s.t.Fatalf("audit queue --json: %v\n%s", err, r.Stdout)
	}
	for _, it := range q.Items {
		ids = append(ids, it.FiringID)
	}
	return q.Counts, ids
}

// TestGuardFiringAuditLoop drives Suite A step 7a through the binary: a firing reaches
// the queue, show stays inside its bound, the footer's contest command records a
// contest that unblocks and dequeues nothing, and exclude and reinstate move the row
// out of the queue and back.
//
// pins existing behaviour
func TestGuardFiringAuditLoop(t *testing.T) {
	t.Parallel()
	s := NewSandbox(t)
	s.RegisterNoVar("claude-code")
	reason := s.FireNoVar("sess-1")

	_, footer, ok := strings.Cut(reason, "steer contest ")
	if !ok {
		t.Fatalf("deny reason carries no contest command:\n%s", reason)
	}
	id := strings.Fields(footer)[0]
	if !strings.Contains(reason, "firing: "+id) {
		t.Errorf("the footer's firing id and contest id differ:\n%s", reason)
	}

	counts, ids := s.auditQueue()
	if counts["queued"] != float64(1) || len(ids) != 1 || ids[0] != id {
		t.Fatalf("queue = %v %v, want the one firing %s", counts, ids, id)
	}

	const bound = 4000
	show := s.MustSteer("audit", "show", id, "--max-bytes", fmt.Sprint(bound))
	if len(show.Stdout) > bound || !strings.Contains(show.Stdout, id) {
		t.Errorf("audit show printed %d bytes over a %d bound, or omitted %s:\n%s", len(show.Stdout), bound, id, show.Stdout)
	}

	s.MustSteer("contest", id, "the task needs var for a legacy script")
	if n := len(readJSONL(t, filepath.Join(s.Home, ".steer", "state", "firing-audits", "contests.jsonl"))); n != 1 {
		t.Errorf("contests.jsonl holds %d rows, want 1", n)
	}
	if counts, _ := s.auditQueue(); counts["queued"] != float64(1) {
		t.Errorf("a contest changed the queue: %v", counts)
	}
	contests := s.MustSteer("audit", "contests", "--json")
	var c struct {
		Guards []struct {
			HookName  string `json:"hook_name"`
			Contested int    `json:"contested"`
		} `json:"guards"`
	}
	if err := json.Unmarshal([]byte(contests.Stdout), &c); err != nil || len(c.Guards) != 1 ||
		c.Guards[0].HookName != noVarHook || c.Guards[0].Contested != 1 {
		t.Errorf("audit contests = %+v (%v), want one contest on %s", c.Guards, err, noVarHook)
	}

	s.MustSteer("audit", "exclude", id, "--reason", "synthetic e2e firing",
		"--evidence", "the payload was piped into the master by a test")
	if counts, ids := s.auditQueue(); counts["queued"] != float64(0) || counts["excluded"] != float64(1) || len(ids) != 0 {
		t.Errorf("after exclude the queue = %v %v, want none queued and one excluded", counts, ids)
	}
	if excluded := s.MustSteer("audit", "rows", "--excluded", "--json"); !strings.Contains(excluded.Stdout, id) {
		t.Errorf("audit rows --excluded omits %s:\n%s", id, excluded.Stdout)
	}

	s.MustSteer("audit", "reinstate", id, "--reason", "the firing was real work")
	if counts, ids := s.auditQueue(); counts["queued"] != float64(1) || counts["excluded"] != float64(0) || len(ids) != 1 {
		t.Errorf("after reinstate the queue = %v %v, want the firing back", counts, ids)
	}
}

// writeNoVarFixtures writes a fixture file for the no-var guard. allowVar labels the
// var command as allowed, which the guard contradicts.
func (s *Sandbox) writeNoVarFixtures(allowVar bool) string {
	s.t.Helper()
	payload := func(command string) map[string]any {
		return map[string]any{"hook_event_name": "PreToolUse", "tool_name": "Bash",
			"tool_input": map[string]any{"command": command}}
	}
	varExpect := "block"
	if allowVar {
		varExpect = "allow"
	}
	data, _ := json.Marshal(map[string]any{"version": 1, "hook": noVarHook, "cases": []any{
		map[string]any{"name": "var-command", "payload": payload("var x = 1"), "expect": varExpect, "origin": "synthetic"},
		map[string]any{"name": "let-command", "payload": payload("let x = 1"), "expect": "allow", "origin": "synthetic"},
	}})
	path := s.path("no-var-fixtures.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		s.t.Fatal(err)
	}
	return path
}

// TestFixtureRunWritesNoLiveAuditRow checks Suite A steps 2 and 3: stored cases replay
// against the guard and the master, and the replay leaves the store's audit log
// untouched.
//
// pins existing behaviour
func TestFixtureRunWritesNoLiveAuditRow(t *testing.T) {
	t.Parallel()
	s := NewSandbox(t)
	src := s.path(noVarHook + ".py")
	if err := os.WriteFile(src, []byte(noVarSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	s.MustSteer("register", src, "--rule", noVarRule, "--harness", "claude-code",
		"--fixtures", s.writeNoVarFixtures(false))
	r := s.MustSteer("fixture", "run", noVarHook)
	for _, want := range []string{"var-command", "let-command", "blocked by this guard", "The guard intercepts its stored cases."} {
		if !strings.Contains(r.Output(), want) {
			t.Errorf("fixture run lacks %q:\n%s", want, r.Output())
		}
	}
	if rows := s.AuditRows(); rows != nil {
		t.Errorf("fixture run wrote %d live audit row(s)", len(rows))
	}
	if counts, ids := s.auditQueue(); len(ids) != 0 {
		t.Errorf("a fixture firing reached the audit queue: %v %v", counts, ids)
	}
}

// TestFixtureRunFailsAnAllowCaseTheGuardBlocks checks that a case labelled allow fails
// the replay when the guard blocks it.
//
// pins existing behaviour
func TestFixtureRunFailsAnAllowCaseTheGuardBlocks(t *testing.T) {
	t.Parallel()
	s := NewSandbox(t)
	s.RegisterNoVar("claude-code")
	src := s.path(noVarHook + ".py")
	reg := s.MustSteer("register", src, "--rule", noVarRule, "--harness", "claude-code",
		"--fixtures", s.writeNoVarFixtures(true))
	if !strings.Contains(reg.Output(), "NOT verified") || !strings.Contains(reg.Output(), "labelled allow that this guard blocked") {
		t.Errorf("register did not report the contradicted allow case:\n%s", reg.Output())
	}
	if r := s.Steer("fixture", "run", noVarHook); r.Code == 0 || !strings.Contains(r.Output(), "var-command") {
		t.Fatalf("exit %d; a blocked allow case did not fail the replay by name:\n%s", r.Code, r.Output())
	}
}

// TestGuardFixRefusesAnUnapprovedCodexRunner checks that a guard record scoped to Codex
// is refused while the registered Codex runner has no approval record, and that
// --force records it anyway.
//
// pins existing behaviour
func TestGuardFixRefusesAnUnapprovedCodexRunner(t *testing.T) {
	t.Parallel()
	s := NewSandbox(t)
	s.RegisterNoVar("codex")
	r := s.Steer(noVarFixArgs(time.Now().Unix(), "--scope-harness", "codex")...)
	if r.Code == 0 || !strings.Contains(r.Output(), "guard runner is registered but has no approval record") {
		t.Fatalf("exit %d; an unapproved Codex runner was not refused by name:\n%s", r.Code, r.Output())
	}
	if n := len(s.Records()); n != 0 {
		t.Fatalf("the refused registration wrote %d records", n)
	}
	s.MustSteer(noVarFixArgs(time.Now().Unix(), "--scope-harness", "codex", "--force")...)
	if n := len(s.Records()); n != 1 {
		t.Errorf("--force wrote %d records, want 1", n)
	}
}

// TestBothManagedInstallersRefuseAMultiLineRule checks that a guard's rule and an
// instruction rule must each be one line, and that a refusal writes neither document.
//
// pins existing behaviour
func TestBothManagedInstallersRefuseAMultiLineRule(t *testing.T) {
	t.Parallel()
	s := NewSandbox(t)
	src := s.path(noVarHook + ".py")
	if err := os.WriteFile(src, []byte(noVarSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	const multi = "never use var\n## Injected"
	for _, args := range [][]string{
		{"register", src, "--rule", multi, "--harness", "claude-code"},
		{"rules", "add", "multi", "--rule", multi},
	} {
		if r := s.Steer(args...); r.Code == 0 || !strings.Contains(r.Output(), "rule text must be one line") {
			t.Errorf("steer %s: exit %d, want a one-line refusal:\n%s", args[0], r.Code, r.Output())
		}
	}
	for _, doc := range []string{"CLAUDE.md", "AGENTS.md"} {
		if fileExists(filepath.Join(s.Repo, doc)) {
			t.Errorf("a refused multi-line rule wrote %s", doc)
		}
	}
}

// TestNamesakeGuardsKeepSeparateFiringCounts checks that a guard of the same name in
// the project and global stores counts only its own store's rows, and that two blocks
// in one second are two rows.
//
// pins existing behaviour
func TestNamesakeGuardsKeepSeparateFiringCounts(t *testing.T) {
	t.Parallel()
	s := NewSandbox(t)
	s.RegisterNoVar("claude-code")
	s.MustSteer("register", s.path(noVarHook+".py"), "--rule", noVarRule, "--harness", "claude-code", "--scope", "global")
	installed := nextSecond() - 1
	fire := func(store string) {
		payload := `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"var x = 1"},"session_id":"s1"}`
		if r := s.Run(s.Repo, payload, nil, "python3", filepath.Join(store, "steer-hooks.py")); !strings.Contains(r.Stdout, `"deny"`) {
			t.Fatalf("the %s master did not deny:\n%s", store, r.Output())
		}
	}
	fire(filepath.Join(s.Repo, ".steer", "hooks"))
	fire(filepath.Join(s.Home, ".steer", "hooks"))
	fire(filepath.Join(s.Home, ".steer", "hooks"))
	for _, scope := range []string{"project", "global"} {
		s.MustSteer("fix", "register", "--tier", "guard", "--hook-id", noVarHook, "--hook-scope", scope,
			"--class", "var-"+scope, "--match", "never use var",
			"--problem", "Agents declare JavaScript variables with var.",
			"--intervention", "A guard blocks var declarations in shell commands.",
			"--target", ".steer/hooks/"+noVarHook+".py", "--scope-harness", "claude-code",
			"--installed-ts", fmt.Sprint(installed), "--force")
	}
	r := s.MustSteer("fix", "score", "--json")
	var records []map[string]any
	if err := json.Unmarshal([]byte(r.Stdout), &records); err != nil {
		t.Fatalf("fix score --json: %v\n%s", err, r.Stdout)
	}
	rows := map[string]any{}
	for _, rec := range records {
		rows[fmt.Sprint(rec["id"])] = dig(rec, "observation", "firing", "rows")
	}
	if rows["var-project"] != float64(1) || rows["var-global"] != float64(2) {
		t.Errorf("firing rows = %v, want var-project 1 and var-global 2", rows)
	}
}

const keepSrc = `#!/usr/bin/env python3
import sys
import json

def main():
    data = json.load(sys.stdin)
    if "KEEP" in data.get("tool_input", {}).get("old_string", ""):
        print(json.dumps({"decision": "block", "reason": "never remove a line marked KEEP"}))
    sys.exit(0)

if __name__ == "__main__":
    main()
`

// TestFixtureMineCarriesRemovedText checks Suite A step 2's deletion-only fixture: the
// gate stores a diff that only removes lines and deletes a file, the miner turns each
// removal into an Edit carrying the removed text and the deleted file into the
// runtime's delete marker, and a guard on removed text intercepts the mined cases.
//
// pins existing behaviour
func TestFixtureMineCarriesRemovedText(t *testing.T) {
	t.Parallel()
	s, g := newNativeGate(t, "claude-code", ".claude/settings.json", "commit-gate review")
	var before, after strings.Builder
	for i := 0; i < 20; i++ {
		line := fmt.Sprintf("def f%d(x):\n    return x + %d\n", i, i)
		if i%5 == 0 {
			before.WriteString(strings.TrimSuffix(line, "\n") + "  # KEEP\n")
			continue
		}
		before.WriteString(line)
		after.WriteString(line)
	}
	s.Write("mod.py", before.String())
	s.Write("notes.txt", "old notes\nKEEP this\n")
	s.Git("add", "mod.py", "notes.txt")
	s.Git("commit", "-q", "-m", "add files")
	s.Write("mod.py", after.String())
	s.Git("rm", "-q", "notes.txt")
	s.Git("add", "mod.py")
	s.Script("finding", "finding", "finding")
	if r := g.Hook("git commit -m 'drop the markers'", ""); !r.Denied() {
		t.Fatalf("the review did not deny, so no diff is stored:\n%s", r.Text())
	}
	rows := s.LedgerRows()
	diff, _ := rows[len(rows)-1]["diff_file"].(string)
	out := s.path("candidates.json")
	s.MustSteer("fixture", "mine", "--diff", diff, "-o", out)
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var mined struct {
		Candidates []struct {
			Path        string         `json:"path"`
			Hunk        int            `json:"hunk"`
			DeletedFile bool           `json:"deleted_file"`
			Payload     map[string]any `json:"payload"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(data, &mined); err != nil {
		t.Fatalf("candidates: %v\n%s", err, data)
	}
	var cases []any
	removals, deletes := 0, 0
	for _, c := range mined.Candidates {
		input, _ := c.Payload["tool_input"].(map[string]any)
		old, hasOld := input["old_string"].(string)
		expect := "allow"
		switch {
		case c.DeletedFile:
			deletes++
			if c.Path != "notes.txt" || input["steer_operation"] != "delete" || hasOld || input["new_string"] != "" {
				t.Errorf("the deleted file is not the runtime's delete marker without its content: %v", c.Payload)
			}
		case c.Path == "mod.py":
			removals++
			if !strings.Contains(old, "# KEEP") || input["new_string"] != "" {
				t.Errorf("hunk %d does not carry only its removed text: %v", c.Hunk, input)
			}
			expect = "block"
		}
		payload := map[string]any{"hook_event_name": "PreToolUse"}
		for k, v := range c.Payload {
			payload[k] = v
		}
		cases = append(cases, map[string]any{"name": fmt.Sprintf("%s-hunk-%d", c.Path, c.Hunk),
			"payload": payload, "expect": expect, "origin": "reconstructed"})
	}
	if removals != 4 || deletes != 1 {
		t.Fatalf("mined %d removal hunks and %d deleted files, want 4 and 1:\n%s", removals, deletes, data)
	}

	fixtures, _ := json.Marshal(map[string]any{"version": 1, "hook": "keep-marker", "cases": cases})
	if err := os.WriteFile(s.path("keep-fixtures.json"), fixtures, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.path("keep-marker.py"), []byte(keepSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	s.MustSteer("register", s.path("keep-marker.py"), "--rule", "never remove a line marked KEEP | leave it in place",
		"--harness", "claude-code", "--fixtures", s.path("keep-fixtures.json"))
	r := s.MustSteer("fixture", "run", "keep-marker")
	for _, want := range []string{"mod.py-hunk-1", "notes.txt-hunk-1", "The guard intercepts its stored cases."} {
		if !strings.Contains(r.Output(), want) {
			t.Errorf("fixture run lacks %q:\n%s", want, r.Output())
		}
	}
}
