package branchreview

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/steer-sh/steer-cli/internal/gate"
)

const gitWait = 30 * time.Second

// defaultBases are the refs tried, in order, after origin's HEAD when no base is named.
var defaultBases = []string{"origin/main", "main", "origin/master", "master"}

// ResolveBase returns the ref the base was taken from and the merge base of that ref
// and HEAD. An empty ref tries origin's HEAD, then defaultBases.
func ResolveBase(repo, ref string) (string, string, error) {
	candidates := []string{ref}
	if ref == "" {
		candidates = nil
		if out, ok := gate.Git(repo, gitWait, "symbolic-ref", "-q", "refs/remotes/origin/HEAD"); ok {
			if name := strings.TrimSpace(out); name != "" {
				candidates = append(candidates, name)
			}
		}
		candidates = append(candidates, defaultBases...)
	}
	for _, c := range candidates {
		if _, ok := gate.Git(repo, gitWait, "rev-parse", "--verify", "--quiet", c+"^{commit}"); !ok {
			if ref != "" {
				return "", "", fmt.Errorf("--base %s names no commit in %s", ref, repo)
			}
			continue
		}
		out, ok := gate.Git(repo, gitWait, "merge-base", c, "HEAD")
		if !ok {
			return "", "", fmt.Errorf("%s and HEAD share no history, so the branch has no base; name one with --base <ref>", c)
		}
		return c, strings.TrimSpace(out), nil
	}
	return "", "", fmt.Errorf("found no base branch (tried origin's HEAD, %s); name one with --base <ref>",
		strings.Join(defaultBases, ", "))
}

// DirtyError is a working tree that holds tracked edits HEAD does not.
type DirtyError struct{ Paths []string }

func (e *DirtyError) Error() string {
	shown := e.Paths
	if len(shown) > 5 {
		shown = shown[:5]
	}
	return fmt.Sprintf("the working tree holds %d tracked path(s) with uncommitted edits (%s), and a reviewer "+
		"reads files from disk while the branch diff ends at HEAD; commit or stash them first, or pass --allow-dirty",
		len(e.Paths), strings.Join(shown, ", "))
}

// DirtyPaths lists the root-relative tracked paths whose index or working copy
// differs from HEAD. Both are asked: an API reviewer reads the index, and a staged
// edit the working copy undoes shows in only one of them.
func DirtyPaths(repo string) ([]string, error) {
	seen := map[string]bool{}
	var paths []string
	for _, args := range [][]string{
		{"diff", "--name-only", "--no-relative", "-z", "HEAD"},
		{"diff", "--cached", "--name-only", "--no-relative", "-z", "HEAD"},
	} {
		out, ok := gate.Git(repo, gitWait, args...)
		if !ok {
			return nil, errors.New("git could not compare the index and working tree with HEAD")
		}
		for _, p := range strings.Split(out, "\x00") {
			if p != "" && !seen[p] {
				seen[p] = true
				paths = append(paths, p)
			}
		}
	}
	return paths, nil
}

// Request is one branch to review.
type Request struct {
	// Repo is the git toplevel.
	Repo string
	// BaseRef is the ref the operator named, or "" for the default.
	BaseRef string
	// AllowDirty reviews despite tracked edits HEAD does not hold.
	AllowDirty bool
	// Dir is the store directory; Host names the shard.
	Dir  string
	Host string
	// Warnings are the caller's failures before the run, such as a settings file
	// that did not load; the row and Result record them first.
	Warnings []string
}

// Result is what one branch review produced.
type Result struct {
	ReviewID string
	Phase    string
	BaseRef  string
	Base     string
	Head     string
	Branch   string
	Commits  int
	Files    int
	// Review is the panel's composed output, "" when no reviewer found anything.
	Review   string
	Slots    int
	Findings []Finding
	// Unparsed counts the replies whose findings could not be read one by one.
	Unparsed int
	Answered int
	Runs     []gate.ReviewRun
	Elapsed  time.Duration
	// Trouble names why runs did not answer.
	Trouble string
	// StoreError is a failure to record the review, which the review still survives.
	StoreError error
	// Warnings are the failures the row records beside the review: the request's
	// own, a reviewed-rule manifest that did not load, or a diff that was not saved.
	Warnings []string
}

// Run reviews the branch req names with reviewer under cfg and records the row. It
// returns an error, and records nothing, only when there is no branch to review.
func Run(ctx context.Context, cfg gate.Config, reviewer gate.Reviewer, req Request) (Result, error) {
	cfg.ReviewerProvider = reviewer.Name()
	cfg.ReviewerModel = reviewer.ModelLabel()
	cfg.ReviewerEndpoint = gate.EndpointOf(reviewer)

	baseRef, base, err := ResolveBase(req.Repo, req.BaseRef)
	if err != nil {
		return Result{}, err
	}
	headOut, ok := gate.Git(req.Repo, gitWait, "rev-parse", "HEAD")
	if !ok {
		return Result{}, errors.New("git could not read HEAD")
	}
	head := strings.TrimSpace(headOut)
	dirty, err := DirtyPaths(req.Repo)
	if err != nil {
		return Result{}, err
	}
	if len(dirty) > 0 && !req.AllowDirty {
		return Result{}, &DirtyError{Paths: dirty}
	}
	id, err := newReviewID()
	if err != nil {
		return Result{}, err
	}
	res := Result{ReviewID: id, BaseRef: baseRef, Base: base, Head: head, Branch: currentBranch(req.Repo)}

	diff, ok := gate.GetBranchDiff(cfg, req.Repo, base, head)
	if !ok {
		return Result{}, fmt.Errorf("git could not diff %s..%s", short(base), short(head))
	}
	commits, count, ok := gate.BranchCommits(req.Repo, base, head)
	if !ok {
		return Result{}, fmt.Errorf("git could not list the commits of %s..%s", short(base), short(head))
	}
	res.Commits, res.Files = count, len(diff.Paths)
	addendum := gate.LoadAddendum(req.Repo)
	row := gate.Row{
		"ts": time.Now().Unix(), "review_id": id, "repo": req.Repo, "branch": res.Branch,
		"base_ref": baseRef, "base_sha": base, "head_sha": head, "commits": count,
		"dirty_allowed": req.AllowDirty, "files": len(diff.Paths), "host": req.Host,
		"instrument":        cfg.BranchInstrumentID(addendum.Digest()),
		"instrument_fields": cfg.BranchInstrumentFields(addendum.Digest()),
	}
	if tree, ok := gate.Git(req.Repo, gitWait, "rev-parse", "HEAD^{tree}"); ok {
		row["tree_sha"] = strings.TrimSpace(tree)
	}
	if len(dirty) > 0 {
		row["dirty_paths"] = gate.SlashPaths(dirty)
	}
	// Recorded before the skips: a rule that did not load may be the rule that
	// would have reviewed a prose-only branch.
	errs := append(append([]string{}, req.Warnings...), addendum.Errors...)
	if len(errs) > 0 {
		row["review_errors"] = errs
		res.Warnings = errs
	}

	switch {
	case base == head || diff.Raw == "":
		res.Phase = PhaseSkipEmpty
	case gate.IsProseOnly(diff.Paths) && len(addendum.RulesFor(diff.RulePaths)) == 0:
		res.Phase = PhaseSkipProse
	}
	if res.Phase != "" {
		row["phase"] = res.Phase
		res.StoreError = appendRow(ReviewsFile(req.Dir, req.Host), "review", row)
		return res, nil
	}

	pinRefs(req.Repo, base, head)
	context := ""
	if len(dirty) > 0 {
		context = gate.DirtyTreeNote(dirty, diff.Paths)
	}
	prompt := gate.BuildBranchPrompt(base, head, commits, diff.Text, context, addendum, diff.RulePaths)
	start := time.Now()
	panel := gate.NewPanel(cfg, reviewer).Run(ctx, prompt, req.Repo, gate.DiffSpec{StagedOnly: true})
	res.Elapsed = time.Since(start)
	res.Answered, res.Runs = panel.Answered, panel.Runs
	res.Review, res.Slots = gate.ComposeReview(panel.Reviews)
	res.Findings, res.Unparsed = parseFindings(panel.Reviews)
	row["findings_unparsed"] = res.Unparsed
	res.Trouble = gate.ReviewerTrouble(panel.Runs)
	res.Phase = phaseOf(panel, res.Slots, res.Unparsed)

	applied := addendum.AppliedRuleIDs(diff.RulePaths)
	for key, value := range gate.ReviewerReport(cfg, panel.Runs) {
		row[key] = value
	}
	for key, value := range gate.FindingShape(panel.Reviews, diff.Paths, applied) {
		row[key] = value
	}
	row["phase"] = res.Phase
	row["reviewers"] = panel.Answered
	row["review_elapsed_ms"] = res.Elapsed.Milliseconds()
	row["changed_paths"] = gate.SlashPaths(diff.Paths)
	row["finding_slots"] = res.Slots
	row["severity_lines_loose"] = gate.LooseFindingCount(panel.Reviews)
	row["reviews_off_format"] = gate.OffFormatReviews(panel.Reviews)
	row["findings"] = res.Findings
	if res.Review != "" {
		row["review"] = res.Review
	}
	if len(applied) > 0 {
		row["reviewed_rules_applied"] = applied
	}
	if saved, err := saveDiff(req.Dir, diff.Text); err != nil {
		errs = append(errs, "save_diff: "+err.Error())
	} else {
		for key, value := range saved {
			row[key] = value
		}
	}
	if len(errs) > 0 {
		row["review_errors"] = errs
		res.Warnings = errs
	}
	res.StoreError = appendRow(ReviewsFile(req.Dir, req.Host), "review", row)
	return res, nil
}

// phaseOf names how a reviewed round ended. A reply that is not the clean answer
// and holds no finding the parser reads is not a clean review.
func phaseOf(panel gate.PanelResult, slots, unparsed int) string {
	switch {
	case panel.Answered == 0:
		return PhaseUngated
	case slots == 0 && unparsed == 0:
		return PhaseClean
	case unparsed == 0 && gate.NitpickOnlyRound(panel.Reviews):
		return PhaseNitpick
	}
	return PhaseFindings
}

// parseFindings numbers the findings of reviews, the panel's non-clean replies, in
// the order ComposeReview shows them, and counts the replies whose findings could
// not all be parsed: a parse error, no finding, or more severity lines than the
// parser read.
func parseFindings(reviews []string) ([]Finding, int) {
	var out []Finding
	unparsed := 0
	for i, review := range reviews {
		parsed, err := gate.ParseFindings(review)
		if err != nil || len(parsed) == 0 || gate.CountFindingsLoose(review) > len(parsed) {
			unparsed++
			continue
		}
		for _, f := range parsed {
			out = append(out, Finding{Index: len(out) + 1, Run: i + 1, Severity: f.Severity,
				File: f.File, Claim: f.Claim, FailsWhen: f.FailsWhen, Rule: f.Rule})
		}
	}
	return out, unparsed
}

// pinRefs keeps both ends reachable after a rebase or a deleted branch, so git can
// still produce the whole reviewed range.
func pinRefs(repo string, shas ...string) {
	for _, sha := range shas {
		gate.Git(repo, gitWait, "update-ref", "refs/branch-review/"+sha, sha)
	}
}

func currentBranch(repo string) string {
	out, ok := gate.Git(repo, gitWait, "symbolic-ref", "-q", "--short", "HEAD")
	if !ok {
		return ""
	}
	return strings.TrimSpace(out)
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
