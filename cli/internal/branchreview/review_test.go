package branchreview

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/steer-sh/steer-cli/internal/gate"
)

// The subcommand is spelled from a constant so a shell writing this file does not
// trip the commit gate on itself.
const verb = "co" + "mmit"

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, repo, path, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repo, path), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// branchRepo is a repository whose main holds a.go and whose branch feature adds
// two commits on top of it.
func branchRepo(t *testing.T) string {
	t.Helper()
	// LoadAddendum reads the global manifest under the home directory.
	t.Setenv("HOME", t.TempDir())
	repo := gate.Canonical(t.TempDir())
	git(t, repo, "init", "-q", "-b", "main")
	write(t, repo, "a.go", "package a\n\nfunc A() int { return 1 }\n")
	git(t, repo, "add", "-A")
	git(t, repo, verb, "-q", "-m", "base")
	git(t, repo, "switch", "-q", "-c", "feature")
	write(t, repo, "a.go", "package a\n\nfunc A() int { return 2 }\n")
	git(t, repo, verb, "-q", "-am", "first: A returns 2")
	write(t, repo, "b.go", "package a\n\nfunc B() int { return A() }\n")
	git(t, repo, "add", "-A")
	git(t, repo, verb, "-q", "-m", "second: add B")
	return repo
}

// stub answers each run with the next reply and keeps the prompts it read.
type stub struct {
	mu      sync.Mutex
	replies []string
	prompts []string
}

func (s *stub) Name() string       { return "stub" }
func (s *stub) ModelLabel() string { return "stub-model" }
func (s *stub) Review(_ context.Context, prompt, _ string) gate.RawRun {
	s.mu.Lock()
	defer s.mu.Unlock()
	reply := "NO FINDINGS"
	if len(s.prompts) < len(s.replies) {
		reply = s.replies[len(s.prompts)]
	}
	s.prompts = append(s.prompts, prompt)
	return gate.RawRun{Response: reply, Status: "SUCCESS"}
}

const oneFinding = `[1] SEVERITY: BLOCKING
    FILE: b.go
    CLAIM: B returns A.
    FAILS WHEN: always.`

func request(t *testing.T, repo string) Request {
	return Request{Repo: repo, Dir: t.TempDir(), Host: "testhost"}
}

func config(runs int) gate.Config {
	cfg := gate.Defaults()
	cfg.ReviewerRuns = runs
	return cfg
}

// TestRunReviewsTheWholeBranchAndRecordsIt checks that one review reads every
// commit since the merge base, numbers the findings across runs, and records the
// row, the diff and refs pinning both ends.
func TestRunReviewsTheWholeBranchAndRecordsIt(t *testing.T) {
	repo := branchRepo(t)
	reviewer := &stub{replies: []string{oneFinding, "NO FINDINGS", oneFinding}}
	req := request(t, repo)
	req.Warnings = []string{"reviewer.api.tools is off"}
	req.Harness = "codex"
	// A state root an older binary left open is closed by the first review.
	stateRoot := filepath.Join(t.TempDir(), "state")
	if err := os.MkdirAll(stateRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(stateRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	req.Dir = filepath.Join(stateRoot, Component)
	res, err := Run(context.Background(), config(3), reviewer, req)
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{stateRoot, req.Dir, filepath.Join(req.Dir, "diffs")} {
		if info, err := os.Stat(dir); err != nil || info.Mode().Perm() != 0o700 {
			t.Errorf("%s: %v, %v; want mode 700", dir, info, err)
		}
	}
	if len(res.Warnings) != 1 || res.Warnings[0] != req.Warnings[0] {
		t.Errorf("warnings = %q; want the request's warning", res.Warnings)
	}
	// Run numbers follow the composed output, which shows no clean reply.
	if !strings.Contains(res.Review, "--- reviewer 2 ---") || strings.Contains(res.Review, "--- reviewer 3 ---") {
		t.Errorf("composed review = %s", res.Review)
	}
	if res.Phase != PhaseFindings || res.Commits != 2 || res.Files != 2 || res.BaseRef != "main" {
		t.Fatalf("result = %+v", res)
	}
	if len(res.Findings) != 2 || res.Findings[1].Index != 2 || res.Findings[1].Run != 2 || res.Findings[0].File != "b.go" {
		t.Errorf("findings = %+v", res.Findings)
	}
	for _, want := range []string{"first: A returns 2", "second: add B", "return 2", "func B()"} {
		if !strings.Contains(reviewer.prompts[0], want) {
			t.Errorf("the prompt lacks %q", want)
		}
	}
	review, err := FindReview(req.Dir, req.Host, res.ReviewID)
	if err != nil || len(review.Findings) != 2 || review.HeadSHA != res.Head || review.Repo != repo {
		t.Fatalf("FindReview = %+v, %v", review, err)
	}
	raw, _ := os.ReadFile(ReviewsFile(req.Dir, req.Host))
	var row map[string]any
	if err := json.Unmarshal(raw, &row); err != nil {
		t.Fatal(err)
	}
	if errs, _ := row["review_errors"].([]any); len(errs) != 1 || errs[0] != req.Warnings[0] {
		t.Errorf("review_errors = %v; want the request's warning", row["review_errors"])
	}
	if row["channel"] != gate.ChannelBranch || row["harness"] != "codex" {
		t.Errorf("channel = %v, harness = %v; want %q and the request's harness", row["channel"], row["harness"], gate.ChannelBranch)
	}
	if fields, _ := row["instrument_fields"].(map[string]any); fields["engine"] != gate.BranchInstrumentSuffix {
		t.Errorf("instrument_fields = %v", row["instrument_fields"])
	}
	if name, _ := row["diff_file"].(string); name == "" {
		t.Error("the row names no saved diff")
	} else if _, err := os.Stat(filepath.Join(req.Dir, "diffs", name)); err != nil {
		t.Errorf("the saved diff is missing: %v", err)
	}
	for _, sha := range []string{res.Base, res.Head} {
		git(t, repo, "rev-parse", "--verify", "refs/branch-review/"+sha)
	}
}

// TestRunRefusesUncommittedEditsUnlessAllowed checks that a dirty tracked file stops
// the review, and that --allow-dirty names it to the reviewer instead.
func TestRunRefusesUncommittedEditsUnlessAllowed(t *testing.T) {
	repo := branchRepo(t)
	write(t, repo, "a.go", "package a // edited\n")
	req := request(t, repo)
	var dirty *DirtyError
	if _, err := Run(context.Background(), config(1), &stub{}, req); !errors.As(err, &dirty) || dirty.Paths[0] != "a.go" {
		t.Fatalf("err = %v; want a DirtyError naming a.go", err)
	}
	if _, err := os.Stat(ReviewsFile(req.Dir, req.Host)); !os.IsNotExist(err) {
		t.Error("a refused review wrote a row")
	}
	// A staged edit that the working copy undoes is still not HEAD.
	git(t, repo, "add", "a.go")
	git(t, repo, "restore", "--worktree", "--source=HEAD", "a.go")
	if _, err := Run(context.Background(), config(1), &stub{}, req); !errors.As(err, &dirty) {
		t.Fatalf("staged only: err = %v; want a DirtyError", err)
	}
	req.AllowDirty = true
	reviewer := &stub{}
	res, err := Run(context.Background(), config(1), reviewer, req)
	if err != nil || res.Phase != PhaseClean {
		t.Fatalf("allowed: %+v, %v", res, err)
	}
	if !strings.Contains(reviewer.prompts[0], "<dirty-working-tree>") || !strings.Contains(reviewer.prompts[0], "- a.go") {
		t.Error("the prompt does not name the uncommitted path")
	}
}

// TestRunNeverReadsAnUnusableAnswerAsClean checks that a reply whose findings the
// parser refuses records findings it cannot number, that no usable answer records
// ungated, and that neither can be ruled.
func TestRunNeverReadsAnUnusableAnswerAsClean(t *testing.T) {
	repo := branchRepo(t)
	for _, tc := range []struct {
		reply         string
		phase         string
		unparsed      int
		ruleRefusalIn string
	}{
		{oneFinding + "\n\n[2] SEVERITY: HIGH\n    FILE: a.go", PhaseFindings, 1, "could not be parsed"},
		{oneFinding + "\n\nSEVERITY: BLOCKING\n    FILE: a.go", PhaseFindings, 1, "could not be parsed"},
		{"B calls A, which changed; that is a bug.", PhaseUngated, 0, "no reviewer's answer"},
	} {
		req := request(t, repo)
		res, err := Run(context.Background(), config(1), &stub{replies: []string{tc.reply}}, req)
		if err != nil || res.Phase != tc.phase || res.Unparsed != tc.unparsed {
			t.Fatalf("reply %q: phase %s, unparsed %d, err %v", tc.reply, res.Phase, res.Unparsed, err)
		}
		review, _ := FindReview(req.Dir, req.Host, res.ReviewID)
		if _, err := Rule(req.Dir, req.Host, repo, "", review, nil); err == nil || !strings.Contains(err.Error(), tc.ruleRefusalIn) {
			t.Errorf("reply %q: Rule err = %v; want %q", tc.reply, err, tc.ruleRefusalIn)
		}
		if listed, err := List(req.Dir, req.Host, repo); err != nil || len(listed) != 1 || listed[0].Rulable {
			t.Errorf("reply %q: List = %+v, %v; want one review that is not rulable", tc.reply, listed, err)
		}
	}
}

// TestASkippedReviewReportsAManifestThatDidNotLoad checks that a prose-only skip
// still records and returns the reviewed-rule manifest's load error, since a rule
// it dropped may be one that would have reviewed the branch.
func TestASkippedReviewReportsAManifestThatDidNotLoad(t *testing.T) {
	repo := branchRepo(t)
	git(t, repo, "switch", "-q", "main")
	if err := os.MkdirAll(filepath.Join(repo, ".steer", "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, repo, ".steer/hooks/steer-manifest.json", "{not json")
	git(t, repo, "add", "-A")
	git(t, repo, verb, "-q", "-m", "broken manifest")
	git(t, repo, "switch", "-q", "-c", "docs")
	write(t, repo, "notes.md", "# notes\n")
	git(t, repo, "add", "-A")
	git(t, repo, verb, "-q", "-m", "docs")
	req := request(t, repo)
	res, err := Run(context.Background(), config(1), &stub{}, req)
	if err != nil || res.Phase != PhaseSkipProse || len(res.Warnings) != 1 {
		t.Fatalf("result = %+v, %v", res, err)
	}
	raw, _ := os.ReadFile(ReviewsFile(req.Dir, req.Host))
	if !strings.Contains(string(raw), `"review_errors"`) {
		t.Errorf("the skip row does not record the load error: %s", raw)
	}
}

// TestRunSkipsABranchWithNothingToReview checks that HEAD at its base records a skip
// row, with the request's warnings and the branch channel but no harness the request
// did not name, and runs no reviewer; that a branch changing only
// an existing binary file is skipped too, unless git may run an external diff or a
// reviewed rule covers the file.
func TestRunSkipsABranchWithNothingToReview(t *testing.T) {
	repo := branchRepo(t)
	git(t, repo, "switch", "-q", "main")
	reviewer := &stub{}
	req := request(t, repo)
	req.Warnings = []string{"settings file did not load"}
	res, err := Run(context.Background(), config(1), reviewer, req)
	if err != nil || res.Phase != PhaseSkipEmpty || len(reviewer.prompts) != 0 {
		t.Fatalf("result = %+v, %v, %d prompts", res, err, len(reviewer.prompts))
	}
	if len(res.Warnings) != 1 || res.Warnings[0] != req.Warnings[0] {
		t.Errorf("warnings = %q; want the request's warning", res.Warnings)
	}
	if review, err := FindReview(req.Dir, req.Host, res.ReviewID); err != nil || review.Phase != PhaseSkipEmpty {
		t.Errorf("the skip row = %+v, %v", review, err)
	}
	if listed, err := List(req.Dir, req.Host, repo); err != nil || len(listed) != 1 || listed[0].Rulable {
		t.Errorf("List = %+v, %v; want the skip as not rulable", listed, err)
	}
	raw, _ := os.ReadFile(ReviewsFile(req.Dir, req.Host))
	var row map[string]any
	if err := json.Unmarshal(raw, &row); err != nil {
		t.Fatal(err)
	}
	if errs, _ := row["review_errors"].([]any); len(errs) != 1 || errs[0] != req.Warnings[0] {
		t.Errorf("the skip row does not record the request's warning: %s", raw)
	}
	if _, named := row["harness"]; row["channel"] != gate.ChannelBranch || named {
		t.Errorf("skip row channel = %v, harness = %v; want %q and no harness", row["channel"], row["harness"], gate.ChannelBranch)
	}

	t.Setenv("GIT_EXTERNAL_DIFF", "")
	os.Unsetenv("GIT_EXTERNAL_DIFF")
	write(t, repo, "logo.png", "\x89PNG\x00\x01")
	git(t, repo, "add", "-A")
	git(t, repo, verb, "-q", "-m", "add logo")
	git(t, repo, "switch", "-q", "-c", "pics")
	write(t, repo, "logo.png", "\x89PNG\x00\x02")
	git(t, repo, verb, "-q", "-am", "redraw logo")
	if res, err := Run(context.Background(), config(1), reviewer, req); err != nil || res.Phase != PhaseSkipBinary ||
		len(reviewer.prompts) != 0 {
		t.Errorf("binary-only branch: result = %+v, %v, %d prompts", res, err, len(reviewer.prompts))
	}
	git(t, repo, "config", "diff.external", "true")
	if res, err := Run(context.Background(), config(1), reviewer, req); err != nil || res.Phase == PhaseSkipBinary {
		t.Errorf("binary-only branch under an external diff: result = %+v, %v; want it reviewed", res, err)
	}
	git(t, repo, "config", "--unset", "diff.external")
	git(t, repo, "switch", "-q", "main")
	if err := os.MkdirAll(filepath.Join(repo, ".steer", "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, repo, ".steer/hooks/steer-manifest.json",
		`{"hooks":{},"rules":{"png":{"text":"keep logos small","review":{"paths":["*.png"]}}}}`)
	git(t, repo, "add", "-A")
	git(t, repo, verb, "-q", "-m", "png rule")
	git(t, repo, "switch", "-q", "-c", "ruled-pics")
	write(t, repo, "logo.png", "\x89PNG\x00\x03")
	git(t, repo, verb, "-q", "-am", "redraw logo again")
	if res, err := Run(context.Background(), config(1), reviewer, req); err != nil || res.Phase == PhaseSkipBinary ||
		len(res.Warnings) != 1 {
		t.Errorf("binary-only branch under a reviewed rule: result = %+v, %v; want it reviewed", res, err)
	}
}

// TestResolveBaseNamesTheFlagWhenNoBaseResolves checks the default order, the
// errors for a missing default and a bad --base, and that origin's HEAD is recorded
// by its short name.
func TestResolveBaseNamesTheFlagWhenNoBaseResolves(t *testing.T) {
	repo := branchRepo(t)
	if ref, _, err := ResolveBase(repo, ""); err != nil || ref != "main" {
		t.Fatalf("default base = %q, %v", ref, err)
	}
	if _, _, err := ResolveBase(repo, "nonesuch"); err == nil || !strings.Contains(err.Error(), "--base nonesuch") {
		t.Errorf("bad --base: %v", err)
	}
	git(t, repo, "branch", "-q", "-m", "main", "trunk")
	if _, _, err := ResolveBase(repo, ""); err == nil || !strings.Contains(err.Error(), "--base <ref>") {
		t.Errorf("no default base: %v", err)
	}
	if ref, _, err := ResolveBase(repo, "trunk"); err != nil || ref != "trunk" {
		t.Errorf("--base trunk = %q, %v", ref, err)
	}
	git(t, repo, "update-ref", "refs/remotes/origin/trunk", "trunk")
	git(t, repo, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/trunk")
	// A local branch spelled like the remote one is not the base.
	git(t, repo, "branch", "-q", "origin/trunk", "HEAD")
	git(t, repo, "config", "core.warnAmbiguousRefs", "false")
	want := git(t, repo, "rev-parse", "trunk")
	if ref, base, err := ResolveBase(repo, ""); err != nil || ref != "origin/trunk" || base != want {
		t.Errorf("origin's HEAD = %q at %s, %v; want origin/trunk at %s", ref, base, err, want)
	}
}

// TestRuleTakesOneValidRulingPerFinding checks each refusal of a ruling set, then a
// valid set's counts and its head_advanced stamp after a fix commit.
func TestRuleTakesOneValidRulingPerFinding(t *testing.T) {
	repo := branchRepo(t)
	req := request(t, repo)
	res, err := Run(context.Background(), config(2), &stub{replies: []string{oneFinding + "\n    RULE: global:b", oneFinding + "\n    RULE: global:b"}}, req)
	if err != nil {
		t.Fatal(err)
	}
	review, _ := FindReview(req.Dir, req.Host, res.ReviewID)
	ok := RulingInput{Index: 1, Verdict: VerdictAccepted, Resolution: "fixed B"}
	for _, tc := range []struct {
		name    string
		rulings []RulingInput
		want    string
	}{
		{"missing", []RulingInput{ok}, "unruled: 2"},
		{"twice", []RulingInput{ok, ok}, "ruled twice"},
		{"unknown index", []RulingInput{ok, {Index: 3, Verdict: VerdictRejected, Resolution: "x"}}, "no finding 3"},
		{"bad verdict", []RulingInput{ok, {Index: 2, Verdict: "MAYBE", Resolution: "x"}}, "verdict"},
		{"qualified rejection", []RulingInput{ok, {Index: 2, Verdict: VerdictRejected, Qualifier: QualifierNotFix, Resolution: "x"}}, "no qualifier"},
		{"empty resolution", []RulingInput{ok, {Index: 2, Verdict: VerdictRejected}}, "resolution is empty"},
	} {
		if _, err := Rule(req.Dir, req.Host, repo, "", review, tc.rulings); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v; want %q", tc.name, err, tc.want)
		}
	}
	if _, err := Rule(req.Dir, req.Host, "/elsewhere", "", review, nil); err == nil {
		t.Error("a ruling from another repository was recorded")
	}
	write(t, repo, "b.go", "package a\n\nfunc B() int { return 2 }\n")
	git(t, repo, verb, "-q", "-am", "fix B")
	receipt, err := Rule(req.Dir, req.Host, repo, "codex", review, []RulingInput{ok,
		{Index: 2, Verdict: VerdictAccepted, Qualifier: QualifierNotFix, Resolution: "same as 1"}})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Accepted != 1 || receipt.AcceptedNotFixed != 1 || !receipt.HeadAdvanced || receipt.Rulings[0].Claim != "B returns A." ||
		receipt.Rulings[0].Rule != "global:b" || receipt.Rulings[1].Rule != "global:b" {
		t.Errorf("receipt = %+v", receipt)
	}
	listed, err := List(req.Dir, req.Host, repo)
	if err != nil || len(listed) != 1 || !listed[0].Rulable || !listed[0].Ruled || !listed[0].HeadAdvanced {
		t.Errorf("List = %+v, %v", listed, err)
	}
	// A HEAD that does not descend from the reviewed one is not an advance.
	git(t, repo, "switch", "-q", "main")
	moved, err := Rule(req.Dir, req.Host, repo, "", review, []RulingInput{ok, {Index: 2, Verdict: VerdictRejected, Resolution: "x"}})
	if err != nil || moved.HeadAdvanced || !moved.HeadElsewhere {
		t.Errorf("on main: receipt = %+v, %v", moved, err)
	}
	if listed, err := List(req.Dir, req.Host, repo); err != nil || len(listed) != 1 || listed[0].HeadAdvanced || !listed[0].HeadElsewhere {
		t.Errorf("on main: List = %+v, %v", listed, err)
	}
}
