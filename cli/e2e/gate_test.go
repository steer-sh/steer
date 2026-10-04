//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// precommitSandbox arms the pre-commit channel with a three-run stub panel.
func precommitSandbox(t *testing.T, opts ...SandboxOption) *Sandbox {
	s := NewSandbox(t, opts...)
	s.Reviewer("claude-code", "sonnet", "", 3, 3)
	s.MustSteer("commit-gate", "install", "--precommit")
	return s
}

// TestPrecommitCleanReviewProceeds checks that a clean panel lets the commit land
// with one receipt and a clean ledger row for the repository.
//
// pins existing behaviour
func TestPrecommitCleanReviewProceeds(t *testing.T) {
	t.Parallel()
	s := precommitSandbox(t)
	s.Script("clean")
	before := s.Head()
	s.StageCode("mod.py")
	r := s.Run(s.Repo, "", nil, "git", "commit", "-m", "add mod")
	if r.Code != 0 || s.Head() == before {
		t.Fatalf("commit exited %d and HEAD moved %v:\n%s", r.Code, s.Head() != before, r.Output())
	}
	if !strings.Contains(r.Stderr, "3 reviewers read this diff and found nothing — commit proceeds.") {
		t.Errorf("no clean receipt:\n%s", r.Stderr)
	}
	if n := len(s.Calls()); n != 3 {
		t.Errorf("the panel made %d reviewer calls, want 3", n)
	}
	rows := s.LedgerRows()
	if len(rows) != 1 || rows[0]["phase"] != "clean" || rows[0]["repo"] != s.Repo {
		t.Errorf("ledger rows = %v, want one clean row for %s", rows, s.Repo)
	}
}

// TestPrecommitFindingBlocksTheCommit checks that a standing finding stops the commit
// and shows the reviewer's text.
//
// pins existing behaviour
func TestPrecommitFindingBlocksTheCommit(t *testing.T) {
	t.Parallel()
	s := precommitSandbox(t)
	s.Script("finding")
	before := s.Head()
	s.StageCode("mod.py")
	r := s.Run(s.Repo, "", nil, "git", "commit", "-m", "add mod")
	if r.Code == 0 || s.Head() != before {
		t.Fatalf("commit exited 0 or HEAD moved over a finding:\n%s", r.Output())
	}
	for _, want := range []string{"CLAIM: stub finding 1.", "[commit-gate] Commit blocked"} {
		if !strings.Contains(r.Stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, r.Stderr)
		}
	}
	if rows := s.LedgerRows(); len(rows) != 1 || rows[0]["phase"] != "deny" {
		t.Errorf("ledger rows = %v, want one deny row", rows)
	}
}

// TestPrecommitFailsOpenWhenNoReviewerAnswers checks that every panel failure lets
// the commit land with a receipt that says no review happened.
//
// pins existing behaviour
func TestPrecommitFailsOpenWhenNoReviewerAnswers(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"exit", "auth", "unknown-model", "malformed", "empty", "sleep", "missing"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			var opts []SandboxOption
			if mode == "missing" {
				opts = append(opts, WithoutStubs())
			}
			s := precommitSandbox(t, opts...)
			s.Setenv("STEER_STUB_SLEEP", "10")
			s.Script(mode)
			before := s.Head()
			s.StageCode("mod.py")
			r := s.Run(s.Repo, "", nil, "git", "commit", "-m", "add mod")
			if r.Code != 0 || s.Head() == before {
				t.Fatalf("commit exited %d and HEAD moved %v; the gate must fail open:\n%s",
					r.Code, s.Head() != before, r.Output())
			}
			if !strings.Contains(r.Stderr, "This commit was NOT reviewed") {
				t.Errorf("no not-reviewed receipt:\n%s", r.Stderr)
			}
			if rows := s.LedgerRows(); len(rows) != 1 || rows[0]["phase"] != "ungated" {
				t.Errorf("ledger rows = %v, want one ungated row", rows)
			}
		})
	}
}

// TestPrecommitSkipReceipts checks that a skipped commit gets one reason and calls no
// reviewer.
//
// pins existing behaviour
func TestPrecommitSkipReceipts(t *testing.T) {
	t.Parallel()
	cases := []struct{ file, content, want string }{
		{"NOTES.md", strings.Repeat("prose line\n", 80), "not reviewed — every changed file is prose"},
		{"tiny.py", "x = 1\n", "under the 400-character minimum for a review"},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			t.Parallel()
			s := precommitSandbox(t)
			s.Script("finding")
			s.Write(tc.file, tc.content)
			s.Git("add", tc.file)
			r := s.Run(s.Repo, "", nil, "git", "commit", "-m", "skip me")
			if r.Code != 0 {
				t.Fatalf("commit exited %d:\n%s", r.Code, r.Output())
			}
			if !strings.Contains(r.Stderr, tc.want) {
				t.Errorf("stderr lacks %q:\n%s", tc.want, r.Stderr)
			}
			if n := len(s.Calls()); n != 0 {
				t.Errorf("a skipped commit made %d reviewer calls", n)
			}
		})
	}
}

// TestPrecommitReviewsARootCommit checks a first commit, where HEAD does not resolve.
//
// pins existing behaviour
func TestPrecommitReviewsARootCommit(t *testing.T) {
	t.Parallel()
	s := precommitSandbox(t, WithoutBaseCommit())
	s.Script("finding")
	s.StageCode("mod.py")
	r := s.Run(s.Repo, "", nil, "git", "commit", "-m", "first")
	if r.Code == 0 || s.Head() != "" {
		t.Fatalf("a root commit with a finding landed:\n%s", r.Output())
	}
	if !strings.Contains(r.Stderr, "CLAIM: stub finding 1.") {
		t.Errorf("the panel did not review the root commit:\n%s", r.Stderr)
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
