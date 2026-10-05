//go:build e2e

package e2e

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// reviewedPaths is the set of files whose diff each logged reviewer call read,
// joined in call order.
func (s *Sandbox) reviewedPaths() []string {
	s.t.Helper()
	var out []string
	for _, c := range s.Calls() {
		prompt := c.Prompt + c.Stdin
		var paths []string
		for _, name := range []string{"first.py", "second.py"} {
			if strings.Contains(prompt, "+++ b/"+name) {
				paths = append(paths, name)
			}
		}
		out = append(out, strings.Join(paths, ","))
	}
	return out
}

// TestNativeAmendIsReviewedAgainstTheCommitItReplaces checks Suite C's amend case on
// both native hooks: an ordinary commit is reviewed as its delta against HEAD, and an
// --amend over the same index as the whole commit that replaces HEAD.
//
// pins existing behaviour
func TestNativeAmendIsReviewedAgainstTheCommitItReplaces(t *testing.T) {
	t.Parallel()
	for _, h := range nativeHarnesses {
		t.Run(h.harness, func(t *testing.T) {
			t.Parallel()
			s, g := newNativeGate(t, h.harness, h.config, h.sub)
			s.StageCode("first.py")
			s.Git("commit", "-q", "--no-verify", "-m", "add first")
			s.StageCode("second.py")

			s.Script("clean")
			if r := g.Hook("git commit -m 'add second'", s.Repo); r.Denied() {
				t.Fatalf("a clean review denied the commit:\n%s", r.Text())
			}
			if got := s.reviewedPaths(); fmt.Sprint(got) != "[second.py second.py second.py]" {
				t.Errorf("an ordinary commit's reviewers read %v, want only second.py", got)
			}

			s.Script("clean")
			if r := g.Hook("git commit --amend -m 'add first and second'", s.Repo); r.Denied() {
				t.Fatalf("a clean review denied the amend:\n%s", r.Text())
			}
			want := "[first.py,second.py first.py,second.py first.py,second.py]"
			if got := s.reviewedPaths(); fmt.Sprint(got) != want {
				t.Errorf("the amend's reviewers read %v, want both files of the replacing commit", got)
			}
		})
	}
}

// TestPrecommitAmendIsReviewedAndLands records the pre-commit channel under --amend:
// git tells a pre-commit hook nothing about the amend, so the review reads the index
// against HEAD, and the amend lands after a clean review.
//
// pins existing behaviour
func TestPrecommitAmendIsReviewedAndLands(t *testing.T) {
	t.Parallel()
	s := precommitSandbox(t)
	s.StageCode("first.py")
	s.Git("commit", "-q", "--no-verify", "-m", "add first")
	before := s.Head()
	s.StageCode("second.py")

	s.Script("clean")
	r := s.Run(s.Repo, "", nil, "git", "commit", "--amend", "-m", "add first and second")
	if r.Code != 0 || s.Head() == before {
		t.Fatalf("the amend exited %d and HEAD moved %v:\n%s", r.Code, s.Head() != before, r.Output())
	}
	if got := s.reviewedPaths(); len(got) != 3 || !strings.Contains(got[0], "second.py") {
		t.Errorf("the amend's reviewers read %v, want three reviews of second.py", got)
	}
}

// TestReviewerChangeNamesTheMeasurementsItLandsIn checks that a reviewer setting edit
// names each chain-corpus record it moves, minor for effort and major for model, and
// that the record's provenance then lists the two reviewers apart.
//
// pins existing behaviour
func TestReviewerChangeNamesTheMeasurementsItLandsIn(t *testing.T) {
	t.Parallel()
	s, g := newNativeGate(t, "claude-code", ".claude/settings.json", "commit-gate review")
	s.RegisterNoVar("claude-code")
	const concern = "the change declares a variable with var"

	id := g.DenyThenRetry("sess-1", "mod.py", "add mod")
	s.SeedReplies("sess-1", time.Now(), chainLedger(id, "add mod", concern))
	s.MustSteer("commit-gate", "backfill", "--apply")
	installed := nextSecond()
	s.MustSteer("fix", "register", "--tier", "guard", "--hook-id", noVarHook,
		"--corpus", "review-findings", "--kind", "false-positive", "--class", "var-finding",
		"--match", "declares a variable with var",
		"--problem", "Reviewers flag var declarations the agent then rejects.",
		"--intervention", "A guard blocks var declarations before they reach review.",
		"--target", ".steer/hooks/"+noVarHook+".py",
		"--scope-repo-root", s.Repo, "--scope-harness", "claude-code", "--installed-ts", fmt.Sprint(installed))
	baseline, _ := s.Records()[0]["instrument"].(string)
	if !strings.HasPrefix(baseline, "sonnet/3/") {
		t.Fatalf("the record froze instrument %q, want the sonnet panel's", baseline)
	}

	if r := s.MustSteer("config", "set", "reviewer.timeout", "4"); strings.Contains(r.Output(), "lands in") {
		t.Errorf("a timeout edit named a measurement:\n%s", r.Output())
	}
	r := s.MustSteer("config", "set", "reviewer.effort", "high")
	for _, want := range []string{"lands in 1 measurement(s)", "minor", "var-finding",
		"effort — the same reviewer, set differently, counts the window after this change"} {
		if !strings.Contains(r.Output(), want) {
			t.Errorf("the effort edit's warning lacks %q:\n%s", want, r.Output())
		}
	}
	r = s.MustSteer("config", "set", "reviewer.model", "opus")
	for _, want := range []string{"lands in 1 measurement(s)", "major", "var-finding",
		"model, effort — a different reviewer counts the window after this change"} {
		if !strings.Contains(r.Output(), want) {
			t.Errorf("the model edit's warning lacks %q:\n%s", want, r.Output())
		}
	}

	id = g.DenyThenRetry("sess-2", "mod2.py", "add mod2")
	s.SeedReplies("sess-2", time.Now(), chainLedger(id, "add mod2", concern))
	s.MustSteer("commit-gate", "backfill", "--apply")
	obs := s.scoredObservation()
	before := fmt.Sprint(dig(obs, "provenance", "before"))
	after := fmt.Sprint(dig(obs, "provenance", "after"))
	if dig(obs, "provenance", "kind") != "reviewer" || before != "["+baseline+"]" ||
		!strings.HasPrefix(after, "[opus/3/") || !strings.HasSuffix(after, "/high]") {
		t.Errorf("provenance = %v, want the sonnet baseline before and the opus panel at high after",
			dig(obs, "provenance"))
	}
}

// TestAnAddendumChangeMovesTheReviewerIdentity checks that a review addendum entry
// joins the identity each ledger row stamps: the prompt digest stays, the addendum
// digest appears after it, and removing the entry restores the stock identity.
//
// pins existing behaviour
func TestAnAddendumChangeMovesTheReviewerIdentity(t *testing.T) {
	t.Parallel()
	s, g := newNativeGate(t, "claude-code", ".claude/settings.json", "commit-gate review")
	instrument := func(file string) string {
		t.Helper()
		s.Script("clean")
		s.StageCode(file)
		if r := g.Hook("git commit -m 'add "+file+"'", ""); r.Denied() {
			t.Fatalf("a clean review denied the commit:\n%s", r.Text())
		}
		s.Git("commit", "-q", "--no-verify", "-m", "add "+file)
		rows := s.LedgerRows()
		inst, _ := rows[len(rows)-1]["instrument"].(string)
		return inst
	}

	stock := instrument("a.py")
	segments := strings.Split(stock, "/")
	if len(segments) != 4 || strings.Contains(segments[2], "+") {
		t.Fatalf("the stock instrument %q carries an addendum digest", stock)
	}
	s.MustSteer("commit-gate", "addendum", "add", "vague-claims", "--scope", "global",
		"--text", "Raise a finding only when you can name the input that fails.")
	with := strings.Split(instrument("b.py"), "/")
	prompt, addendum, ok := strings.Cut(with[2], "+")
	if len(with) != 4 || !ok || prompt != segments[2] || addendum == "" {
		t.Errorf("with an addendum the instrument is %v, want prompt %s plus an addendum digest", with, segments[2])
	}
	s.MustSteer("commit-gate", "addendum", "remove", "vague-claims", "--scope", "global")
	if got := instrument("c.py"); got != stock {
		t.Errorf("after the removal the instrument is %q, want the stock %q", got, stock)
	}
}
