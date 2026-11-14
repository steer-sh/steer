//go:build e2e

package e2e

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// nativeHarnesses are the agent harnesses whose PreToolUse hook carries the gate.
var nativeHarnesses = []struct{ harness, config, sub string }{
	{"claude-code", ".claude/settings.json", "commit-gate review"},
	{"codex", ".codex/hooks.json", "commit-gate review-codex"},
}

// NativeGate is one harness's registered commit-gate hook in a sandbox.
type NativeGate struct {
	s       *Sandbox
	command string
	harness string
	// turn and transcript, when set, ride every payload as the harness's turn field
	// and transcript_path.
	turn, transcript string
}

// HookReply is the parsed reply of a PreToolUse hook.
type HookReply struct {
	Decision, Reason, Context, System, Raw string
}

// Denied reports that the reply refuses the tool call.
func (r HookReply) Denied() bool { return r.Decision == "deny" }

// Text is every field the agent or the operator sees.
func (r HookReply) Text() string { return r.Reason + "\n" + r.Context + "\n" + r.System }

// newNativeGate arms harness's commit gate with a three-run stub panel.
func newNativeGate(t *testing.T, harness, config, sub string, opts ...SandboxOption) (*Sandbox, *NativeGate) {
	t.Helper()
	s := NewSandbox(t, opts...)
	s.Reviewer("claude-code", "sonnet", "", 3, 3)
	s.MustSteer("bin", "install")
	s.MustSteer("commit-gate", "install", "--harness", harness)
	return s, &NativeGate{s: s, harness: harness, command: s.RegisteredCommand(filepath.Join(s.Repo, config), sub)}
}

// Hook sends a Bash tool call through the registered command, as the harness does.
// An empty workdir leaves the field out of the payload.
func (g *NativeGate) Hook(command, workdir string) HookReply {
	g.s.t.Helper()
	return g.hookAs("e2e-native", command, workdir)
}

// hookAs is Hook from the named session.
func (g *NativeGate) hookAs(session, command, workdir string) HookReply {
	g.s.t.Helper()
	input := map[string]any{"command": command}
	if workdir != "" {
		input["workdir"] = workdir
	}
	fields := map[string]any{
		"hook_event_name": "PreToolUse", "tool_name": "Bash", "tool_input": input,
		"session_id": session, "cwd": g.s.Repo,
		"transcript_path": filepath.Join(g.s.Root, "none.jsonl"),
	}
	if g.transcript != "" {
		fields["transcript_path"] = g.transcript
	}
	if g.turn != "" {
		fields[g.turnKey()] = g.turn
	}
	payload, _ := json.Marshal(fields)
	r := g.s.Run(g.s.Repo, string(payload), nil, "sh", "-c", g.command)
	if r.Code != 0 {
		g.s.t.Fatalf("the registered hook exited %d:\n%s", r.Code, r.Output())
	}
	reply := HookReply{Raw: r.Stdout}
	if strings.TrimSpace(r.Stdout) == "" {
		return reply
	}
	var out struct {
		Hook struct {
			Decision string `json:"permissionDecision"`
			Reason   string `json:"permissionDecisionReason"`
			Context  string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
		System string `json:"systemMessage"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &out); err != nil {
		g.s.t.Fatalf("hook reply: %v\n%s", err, r.Stdout)
	}
	reply.Decision, reply.Reason, reply.Context, reply.System = out.Hook.Decision, out.Hook.Reason, out.Hook.Context, out.System
	return reply
}

// turnKey is the payload field that names the turn on this harness.
func (g *NativeGate) turnKey() string {
	if g.harness == "codex" {
		return "turn_id"
	}
	return "prompt_id"
}

// offeredExample is the command a refusal ends with, pointed at repo.
func offeredExample(t *testing.T, reason, repo string) string {
	t.Helper()
	_, example, ok := strings.Cut(reason, "\n\n")
	if !ok {
		t.Fatalf("the refusal offers no command form:\n%s", reason)
	}
	return strings.ReplaceAll(example, "/absolute/path/to/repo", repo)
}

// TestRefusalsNameAFormThatCommits sends each refused commit form through each
// harness's registered hook, then the form the refusal names. The refused form must
// call no reviewer; the named form must be reviewed, and run through sh it must land
// the commit with its whole message.
//
// pins existing behaviour
func TestRefusalsNameAFormThatCommits(t *testing.T) {
	t.Parallel()
	type refusal struct {
		name   string
		codex  bool // only the Codex adapter refuses it
		setup  func(s *Sandbox)
		cmd    string // R is the repository
		noDir  bool   // the payload names no workdir
		want   string
		before string   // a step the refusal says to run on its own first
		pass   string   // "" takes the example the refusal ends with
		offers []string // the refusal's own words that before and pass follow
	}
	cases := []refusal{
		{name: "possible-commit", cmd: `bash -c "git -C R commit -m x"`, want: "holds a possible commit"},
		{name: "possible-beside", cmd: `git -C R commit -m x && bash -c "git -C R commit -m y"`,
			want: "also holds a possible commit", pass: "git -C R commit -m x",
			offers: []string{"Run the commit as its own plain command"}},
		{name: "dynamic-message", cmd: `git -C R commit -F "$MSG"`, want: "cannot establish"},
		{name: "work-tree", cmd: "git -C R --work-tree=R commit -m x", want: "with --work-tree",
			pass: "git -C R commit -m x", offers: []string{"Select the repository with `git -C /absolute/path/to/repo`"}},
		{name: "git-environment", cmd: "GIT_INDEX_FILE=R/.git/index git -C R commit -m x",
			want: "sets GIT_INDEX_FILE", pass: "git -C R commit -m x",
			offers: []string{"run the commit without that variable"}},
		{name: "step-before", cmd: "python3 gen.py && git -C R commit -m x", want: "runs `python3 gen.py` before",
			before: "python3 gen.py", pass: "git status && git -C R commit -m x",
			offers: []string{"Run that step as its own command first", "such as git status"}},
		{name: "redirect", cmd: "git -C R commit -m x > R/out.txt", want: "writes its output",
			pass: "git -C R commit -m x > /dev/null", offers: []string{"send the output to /dev/null"}},
		{name: "concurrent", cmd: "git -C R commit -m x | python3 gen.py", want: "while its commit runs",
			pass: "git -C R commit -m x | tail -n 1", offers: []string{"pipe its output only to a filter such as tail"}},
		{name: "selected-paths", cmd: "git -C R commit -m x mod.py", want: "selects its own paths",
			pass: "git -C R commit -m x", offers: []string{"run a plain commit with no paths"}},
		{name: "partly-staged", cmd: "git -C R add mod.py && git -C R commit -m x", want: "partly staged",
			setup:  func(s *Sandbox) { s.Write("mod.py", s.Git("show", ":mod.py")+"# unstaged\n") },
			before: "git -C R add mod.py", pass: "git -C R commit -m x",
			offers: []string{"Run every step before the commit as its own command, then run the commit alone"}},
		{name: "no-workdir", codex: true, cmd: "git commit -m x", noDir: true, want: "names no repository",
			pass: "cd R && git commit -m x", offers: []string{"`cd /absolute/path/to/repo && git commit ...`"}},
	}
	for _, h := range nativeHarnesses {
		for _, tc := range cases {
			if tc.codex && h.harness != "codex" {
				continue
			}
			t.Run(h.harness+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				s, g := newNativeGate(t, h.harness, h.config, h.sub, WithTools("tail"))
				s.Write("gen.py", "open('generated.txt', 'w').write('x')\n")
				s.StageCode("mod.py")
				if tc.setup != nil {
					tc.setup(s)
				}
				fill := func(cmd string) string { return strings.ReplaceAll(cmd, "R", s.Repo) }
				workdir := s.Repo
				if tc.noDir {
					workdir = ""
				}
				s.Script("clean")

				refused := g.Hook(fill(tc.cmd), workdir)
				if !refused.Denied() || !strings.Contains(refused.Reason, tc.want) {
					t.Fatalf("%q: want a deny naming %q, got:\n%s", tc.cmd, tc.want, refused.Raw)
				}
				for _, phrase := range tc.offers {
					if !strings.Contains(refused.Reason, phrase) {
						t.Errorf("the refusal no longer offers %q, which the passing form follows:\n%s", phrase, refused.Reason)
					}
				}
				if n := len(s.Calls()); n != 0 {
					t.Errorf("a refused command made %d reviewer calls", n)
				}
				if rows := s.LedgerRows(); len(rows) != 0 {
					t.Errorf("a refused command wrote ledger rows: %v", rows)
				}

				pass := fill(tc.pass)
				if tc.pass == "" {
					pass = offeredExample(t, refused.Reason, s.Repo)
				}
				if tc.before != "" {
					if r := s.Run(s.Repo, "", nil, "sh", "-c", fill(tc.before)); r.Code != 0 {
						t.Fatalf("the step %q exited %d:\n%s", tc.before, r.Code, r.Output())
					}
				}
				reviewed := g.Hook(pass, s.Repo)
				if reviewed.Denied() {
					t.Fatalf("the offered form %q is refused:\n%s", pass, reviewed.Raw)
				}
				if n := len(s.Calls()); n != 3 {
					t.Errorf("the offered form %q made %d reviewer calls, want 3", pass, n)
				}
				head := s.Head()
				if r := s.Run(s.Repo, "", nil, "sh", "-c", pass); r.Code != 0 || s.Head() == head {
					t.Fatalf("the offered form %q exited %d and HEAD moved %v:\n%s",
						pass, r.Code, s.Head() != head, r.Output())
				}
				if tc.pass == "" {
					if msg := s.Git("log", "-1", "--format=%B"); strings.TrimSpace(msg) != "subject\n\nbody" {
						t.Errorf("the offered form landed the message %q", msg)
					}
				}
			})
		}
	}
}

// TestNativeGateFailsOpenWhenNoReviewerAnswers checks each harness's hook against
// every panel failure: the tool call is not refused, and the agent or the operator is
// told that no review ran.
//
// pins existing behaviour
func TestNativeGateFailsOpenWhenNoReviewerAnswers(t *testing.T) {
	t.Parallel()
	for _, h := range nativeHarnesses {
		for _, mode := range []string{"exit", "auth", "unknown-model", "malformed", "empty", "sleep", "missing"} {
			t.Run(h.harness+"/"+mode, func(t *testing.T) {
				t.Parallel()
				var opts []SandboxOption
				if mode == "missing" {
					opts = append(opts, WithoutStubs())
				}
				s, g := newNativeGate(t, h.harness, h.config, h.sub, opts...)
				s.Setenv("STEER_STUB_SLEEP", "10")
				s.Script(mode)
				s.StageCode("mod.py")
				reply := g.Hook("git -C "+s.Repo+" commit -m 'add mod'", s.Repo)
				if reply.Denied() {
					t.Fatalf("the gate refused a commit no reviewer judged:\n%s", reply.Raw)
				}
				if !strings.Contains(reply.Text(), "NOT reviewed") {
					t.Errorf("the reply does not say the commit was not reviewed:\n%s", reply.Raw)
				}
				if rows := s.LedgerRows(); len(rows) != 1 || rows[0]["phase"] != "ungated" {
					t.Errorf("ledger rows = %v, want one ungated row", rows)
				}
			})
		}
	}
}
