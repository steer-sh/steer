//go:build e2e

package e2e

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

// TestReviewerArgvPerProvider checks the argv each adapter builds and the path its
// prompt travels, through steer commit-gate test-reviewer.
//
// pins existing behaviour
func TestReviewerArgvPerProvider(t *testing.T) {
	t.Parallel()
	s := NewSandbox(t)
	for _, provider := range []string{"agy", "claude-code", "codex"} {
		t.Run(provider, func(t *testing.T) {
			s.t = t
			s.Reviewer(provider, "model-"+provider, "high", 3, 20)
			s.Script("clean")
			s.MustSteer("commit-gate", "test-reviewer")
			calls := s.Calls()
			if len(calls) != 1 {
				t.Fatalf("test-reviewer made %d calls, want 1", len(calls))
			}
			c := calls[0]
			if c.Provider != providerBinary[provider] || c.Model != "model-"+provider || c.Effort != "high" {
				t.Fatalf("call = %s model %q effort %q, want %s model-%s high",
					c.Provider, c.Model, c.Effort, providerBinary[provider], provider)
			}
			var want []string
			switch provider {
			case "agy":
				if c.Stdin != "" {
					t.Errorf("agy got %d bytes on stdin, want the prompt as the argument after -p", len(c.Stdin))
				}
				want = []string{"--model", "model-agy", "--effort", "high", "--print-timeout", "1m20s",
					"--output-format", "json", "-p", c.Prompt}
			case "claude-code":
				want = []string{"-p", "--output-format", "json", "--no-session-persistence",
					"--safe-mode", "--tools", "Read,Grep,Glob", "--model", "model-claude-code", "--effort", "high"}
			case "codex":
				want = []string{"exec", "--skip-git-repo-check", "--ephemeral", "--sandbox", "read-only",
					"--disable", "hooks", "--disable", "apps", "--model", "model-codex",
					"-c", "model_reasoning_effort=high", "-"}
			}
			if !slices.Equal(c.Argv, want) {
				t.Errorf("argv = %q\nwant   %q", c.Argv, want)
			}
			if !strings.Contains(c.Prompt, "+++ b/stats.py") {
				t.Errorf("the prompt does not carry the sample diff:\n%.300s", c.Prompt)
			}
		})
	}
}

// TestTestReviewerFailsOnEachFailureMode checks that test-reviewer exits 1 and shows
// the reviewer's own output when the reviewer does not answer.
//
// pins existing behaviour
func TestTestReviewerFailsOnEachFailureMode(t *testing.T) {
	t.Parallel()
	cases := []struct{ mode, want string }{
		{"exit", "rc=3 stub: the reviewer crashed"},
		{"auth", "stub: authentication required"},
		{"unknown-model", `stub: unknown model "model-`},
		{"sleep", "no response before the deadline"},
	}
	for _, provider := range []string{"agy", "claude-code", "codex"} {
		malformed := "unparseable: {not json"
		empty := "unparseable"
		if provider == "codex" {
			malformed = "lost: I read the change"
			empty = "empty"
		}
		all := append(cases[:len(cases):len(cases)], struct{ mode, want string }{"malformed", malformed},
			struct{ mode, want string }{"empty", empty})
		for _, tc := range all {
			t.Run(provider+"/"+tc.mode, func(t *testing.T) {
				t.Parallel()
				s := NewSandbox(t)
				s.Setenv("STEER_STUB_SLEEP", "10")
				s.Reviewer(provider, "model-"+provider, "", 3, 2)
				s.Script(tc.mode)
				r := s.Steer("commit-gate", "test-reviewer")
				if r.Code != 1 {
					t.Fatalf("test-reviewer exited %d on %s, want 1:\n%s", r.Code, tc.mode, r.Output())
				}
				out := strings.Join(strings.Fields(r.Output()), " ")
				if !strings.Contains(out, "The reviewer did not answer.") || !strings.Contains(out, tc.want) {
					t.Errorf("output does not name %q:\n%s", tc.want, r.Output())
				}
			})
		}
	}
}

// TestTestReviewerWithNoReviewerOnPath checks the answer when the configured reviewer
// CLI is not installed.
//
// pins existing behaviour
func TestTestReviewerWithNoReviewerOnPath(t *testing.T) {
	t.Parallel()
	s := NewSandbox(t, WithoutStubs())
	for _, provider := range []string{"agy", "claude-code", "codex"} {
		t.Run(provider, func(t *testing.T) {
			s.t = t
			s.Reviewer(provider, "", "", 3, 5)
			r := s.Steer("commit-gate", "test-reviewer")
			if r.Code != 1 {
				t.Fatalf("test-reviewer exited %d with no %s on PATH, want 1:\n%s", r.Code, provider, r.Output())
			}
			if out := r.Output(); !strings.Contains(out, "The reviewer did not answer.") ||
				!strings.Contains(out, fmt.Sprintf("%q", providerBinary[provider])) {
				t.Errorf("output does not name the missing %s executable:\n%s", providerBinary[provider], out)
			}
		})
	}
}
