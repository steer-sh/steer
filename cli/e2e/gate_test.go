//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/gate"
)

// reviewSandbox arms the Claude Code commit gate with a three-run stub panel.
func reviewSandbox(t *testing.T, opts ...SandboxOption) (*Sandbox, *NativeGate) {
	t.Helper()
	return newNativeGate(t, "claude-code", ".claude/settings.json", "commit-gate review", opts...)
}

// TestCleanReviewProceeds checks that a clean panel lets the commit run with one
// receipt and a clean ledger row for the repository.
//
// pins existing behaviour
func TestCleanReviewProceeds(t *testing.T) {
	t.Parallel()
	s, g := reviewSandbox(t)
	s.Script("clean")
	s.StageCode("mod.py")
	r := g.Hook("git commit -m 'add mod'", s.Repo)
	if r.Denied() {
		t.Fatalf("a clean review denied the commit:\n%s", r.Text())
	}
	receipt := "[commit-gate] " + filepath.Base(s.Repo) + ` "add mod" (firing 1, diff `
	if !strings.Contains(r.System, "found nothing — commit proceeds.") || !strings.Contains(r.Context, receipt) {
		t.Errorf("want a clean receipt starting %q:\n%s", receipt, r.Text())
	}
	if n := len(s.Calls()); n != 3 {
		t.Errorf("the panel made %d reviewer calls, want 3", n)
	}
	rows := s.LedgerRows()
	if len(rows) != 1 || rows[0]["phase"] != "clean" || rows[0]["repo"] != s.Repo {
		t.Errorf("ledger rows = %v, want one clean row for %s", rows, s.Repo)
	}
}

// TestFindingBlocksTheCommit checks that a standing finding refuses the commit and
// shows the reviewer's text.
//
// pins existing behaviour
func TestFindingBlocksTheCommit(t *testing.T) {
	t.Parallel()
	s, g := reviewSandbox(t)
	s.Script("finding")
	s.StageCode("mod.py")
	r := g.Hook("git commit -m 'add mod'", s.Repo)
	if !r.Denied() {
		t.Fatalf("a finding did not refuse the commit:\n%s", r.Raw)
	}
	if !strings.Contains(r.Text(), "CLAIM: stub finding 1.") {
		t.Errorf("the reply lacks the finding:\n%s", r.Text())
	}
	if rows := s.LedgerRows(); len(rows) != 1 || rows[0]["phase"] != "deny" {
		t.Errorf("ledger rows = %v, want one deny row", rows)
	}
}

// TestNitpickOnlyRoundProceeds checks that a panel whose every finding is NITPICK
// lets the commit run, shows the findings and the not-blocked line, and records the
// round and the sequence's release.
//
// pins existing behaviour
func TestNitpickOnlyRoundProceeds(t *testing.T) {
	t.Parallel()
	s, g := reviewSandbox(t)
	s.Script("nitpick")
	s.StageCode("mod.py")
	r := g.Hook("git commit -m 'add mod'", s.Repo)
	if r.Denied() {
		t.Fatalf("a nitpick-only round denied the commit:\n%s", r.Text())
	}
	for _, want := range []string{"[1] SEVERITY: NITPICK", "**Round 1, terminal**",
		"[commit-gate] Commit not blocked — 3 of 3 reviewers returned 3 finding(s), duplicates included, all rated NITPICK"} {
		if !strings.Contains(r.Text(), want) {
			t.Errorf("the reply lacks %q:\n%s", want, r.Text())
		}
	}
	rows := s.LedgerRows()
	if len(rows) != 2 || rows[0]["phase"] != gate.PhaseNitpick || rows[1]["phase"] != gate.PhaseChainEnd ||
		rows[1]["blocks"] != float64(0) || rows[1]["terminal_round"] != float64(1) {
		t.Errorf("ledger rows = %v, want a nitpick row and its release with terminal round 1", rows)
	}
}

// TestFailsOpenWhenNoReviewerAnswers checks that every panel failure lets the commit
// run with a receipt that says no review happened.
//
// pins existing behaviour
func TestFailsOpenWhenNoReviewerAnswers(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"exit", "auth", "unknown-model", "malformed", "empty", "sleep", "missing"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			var opts []SandboxOption
			if mode == "missing" {
				opts = append(opts, WithoutStubs())
			}
			s, g := reviewSandbox(t, opts...)
			s.Setenv("STEER_STUB_SLEEP", "10")
			s.Script(mode)
			s.StageCode("mod.py")
			r := g.Hook("git commit -m 'add mod'", s.Repo)
			if r.Denied() {
				t.Fatalf("the gate must fail open:\n%s", r.Text())
			}
			if !strings.Contains(r.Text(), "This commit was NOT reviewed") {
				t.Errorf("no not-reviewed receipt:\n%s", r.Text())
			}
			if rows := s.LedgerRows(); len(rows) != 1 || rows[0]["phase"] != "ungated" {
				t.Errorf("ledger rows = %v, want one ungated row", rows)
			}
		})
	}
}

// TestSkipReceipts checks that a skipped commit gets one reason and calls no
// reviewer.
//
// pins existing behaviour
func TestSkipReceipts(t *testing.T) {
	t.Parallel()
	cases := []struct{ file, content, want string }{
		{"NOTES.md", strings.Repeat("prose line\n", 80), "not reviewed — every changed file is prose"},
		{"package-lock.json", "lock\n", "not reviewed — the diff is empty"},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			t.Parallel()
			s, g := reviewSandbox(t)
			s.Script("finding")
			s.Write(tc.file, tc.content)
			s.Git("add", tc.file)
			r := g.Hook("git commit -m 'skip me'", s.Repo)
			if r.Denied() {
				t.Fatalf("a skipped commit was denied:\n%s", r.Text())
			}
			if !strings.Contains(r.Text(), tc.want) {
				t.Errorf("the reply lacks %q:\n%s", tc.want, r.Text())
			}
			if n := len(s.Calls()); n != 0 {
				t.Errorf("a skipped commit made %d reviewer calls", n)
			}
		})
	}
}

// TestReviewsARootCommit checks a first commit, where HEAD does not resolve.
//
// pins existing behaviour
func TestReviewsARootCommit(t *testing.T) {
	t.Parallel()
	s, g := reviewSandbox(t, WithoutBaseCommit())
	s.Script("finding")
	s.StageCode("mod.py")
	r := g.Hook("git commit -m 'first'", s.Repo)
	if !r.Denied() || !strings.Contains(r.Text(), "CLAIM: stub finding 1.") {
		t.Errorf("the panel did not refuse the root commit over its finding:\n%s", r.Text())
	}
}

// TestHarnessHookRunsFromAShellSpecialHome registers each harness's commit gate from
// a home and repository whose paths hold shell-special bytes, then runs the
// registered command through sh as the harness does.
//
// pins existing behaviour
func TestHarnessHookRunsFromAShellSpecialHome(t *testing.T) {
	t.Parallel()
	cases := []struct{ harness, config, sub string }{
		{"claude-code", ".claude/settings.json", "commit-gate review"},
		{"codex", ".codex/hooks.json", "commit-gate review-codex"},
	}
	for _, tc := range cases {
		t.Run(tc.harness, func(t *testing.T) {
			t.Parallel()
			s := NewSandbox(t, WithHomeName("ho me(&#)"), WithRepoName("re po"))
			s.Reviewer("agy", "m", "", 3, 5)
			s.MustSteer("bin", "install")
			s.MustSteer("commit-gate", "install", "--harness", tc.harness)
			command := s.RegisteredCommand(filepath.Join(s.Repo, tc.config), tc.sub)
			if !strings.Contains(command, "'") {
				t.Errorf("registered command %q does not quote its path", command)
			}
			s.StageCode("mod.py")
			for _, mode := range []string{"finding", "clean"} {
				s.Script(mode)
				payload, _ := json.Marshal(map[string]any{
					"hook_event_name": "PreToolUse", "tool_name": "Bash",
					"tool_input": map[string]any{"command": fmt.Sprintf("git -C '%s' commit -m 'add mod'", s.Repo),
						"workdir": s.Repo},
					"session_id": "e2e-" + mode, "cwd": s.Repo,
					"transcript_path": filepath.Join(s.Root, "none.jsonl"),
				})
				r := s.Run(s.Repo, string(payload), nil, "sh", "-c", command)
				if r.Code != 0 {
					t.Fatalf("%s: the registered command exited %d:\n%s", mode, r.Code, r.Output())
				}
				denied := strings.Contains(r.Stdout, `"permissionDecision":"deny"`)
				if denied != (mode == "finding") {
					t.Errorf("%s: denied=%v:\n%s", mode, denied, r.Output())
				}
				if n := len(s.Calls()); n != 3 {
					t.Errorf("%s: the panel made %d reviewer calls, want 3", mode, n)
				}
			}
		})
	}
}
