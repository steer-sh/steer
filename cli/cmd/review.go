package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/steer-sh/steer-cli/internal/branchreview"
	"github.com/steer-sh/steer-cli/internal/config"
	"github.com/steer-sh/steer-cli/internal/gate"
	"github.com/steer-sh/steer-cli/internal/gate/reviewers"
	"github.com/steer-sh/steer-cli/internal/harness"
	"github.com/steer-sh/steer-cli/internal/settings"
)

var (
	reviewBase       string
	reviewFlags      config.BranchReviewer
	reviewAllowDirty bool
	reviewJSON       bool
	reviewStdin      bool
	reviewClean      bool
	reviewListAll    bool
)

var reviewCmd = &cobra.Command{
	Use:   "review",
	Short: "Review a whole branch before a pull request, and record the rulings",
	Long: `review reads a branch's whole net change, from where it left its base branch to
HEAD, with the branch reviewer, before a pull request opens. A commit review reads
one commit at a time and misses a defect that spans commits; this one reads them
together, with the repository at HEAD to search.

It is advisory: it blocks nothing. Each review is recorded in
~/.steer/state/branch-review, apart from the commit-review ledger, and the author's
ruling on each finding is recorded with steer review rule.

The branch reviewer is the branch_reviewer.* settings, which inherit reviewer.*
except runs, timeout and a CLI reviewer's effort, which default to the branch's own
(see steer config show).`,
	Example: dedent(`
  steer review branch
  steer review branch --base origin/main --json
  steer review rule 3fa9c1d2e4b5 --stdin < rulings.json
  steer review list`),
}

var reviewBranchCmd = &cobra.Command{
	Use:   "branch",
	Short: "Review base..HEAD as one change",
	Long: `branch reviews the commits from the merge base of the base branch and HEAD, up
to HEAD. With no --base it tries origin's HEAD, then origin/main, main,
origin/master and master.

The reviewer reads files from disk while the diff ends at HEAD, so tracked edits
that are not committed stop the review unless --allow-dirty names them to the
reviewer. It exits 0 whenever a reviewer answered, findings or not, and when the
branch holds nothing to review (no change, or prose files only); it exits non-zero
when a review ran and no reviewer answered.

With --json, findings[] numbers every finding across the reviewers; those are the
numbers steer review rule takes, while the raw review text numbers each reviewer's
own findings from 1.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		repo, err := reviewRepo()
		if err != nil {
			return err
		}
		// Pre-run warnings ride the result, so --json and the row carry them; a
		// run that stops before a result prints them here.
		var warnings []string
		warn := func(w string) {
			if !slices.Contains(warnings, w) {
				warnings = append(warnings, w)
			}
		}
		printWarnings := func() {
			for _, w := range warnings {
				fmt.Fprintln(os.Stderr, "steer:", w)
			}
		}
		cfg, fileErr := settings.LoadBranch(reviewFlags)
		if fileErr != nil {
			warn(fileErr.Error() + " — using the shipped defaults")
		}
		toolsWarning, err := branchProviderUsable(cfg)
		if err != nil {
			printWarnings()
			return err
		}
		if toolsWarning != "" {
			warn(toolsWarning)
		}
		cfg, reviewer, err := settings.Resolve(cfg, warn)
		if err != nil {
			printWarnings()
			return err
		}
		fmt.Fprintf(os.Stderr, "Reviewing the branch with %s: %d run(s), each up to %s.\n",
			cfg.ReviewerLabel(), cfg.ReviewerRuns, cfg.ReviewTimeout)
		res, err := branchreview.Run(context.Background(), cfg, reviewer, branchreview.Request{
			Repo: repo, BaseRef: reviewBase, AllowDirty: reviewAllowDirty,
			Dir: branchreview.Dir(), Host: cfg.HostID, Harness: string(harness.Detect()), Warnings: warnings,
		})
		if err != nil {
			printWarnings()
			return err
		}
		if reviewJSON {
			if err := writeJSON(cmd.OutOrStdout(), branchReviewJSON(res, cfg)); err != nil {
				return err
			}
		} else {
			renderBranchReview(cmd.OutOrStdout(), res, cfg)
		}
		for _, w := range res.Warnings {
			fmt.Fprintln(os.Stderr, "steer:", w)
		}
		if res.StoreError != nil {
			fmt.Fprintln(os.Stderr, "steer: the review was not recorded:", res.StoreError)
		}
		if res.Phase == branchreview.PhaseUngated {
			return fmt.Errorf("no reviewer answered: %s", res.Trouble)
		}
		return nil
	},
}

var reviewRuleCmd = &cobra.Command{
	Use:   "rule REVIEW_ID",
	Short: "Record your ruling on each defect a branch review found",
	Long: `rule records one ruling per defect of a branch review on this machine. Read the
rulings as JSON on stdin with --stdin:

  {"rulings": [{"index": 1, "verdict": "ACCEPTED", "qualifier": "", "resolution": "..."},
               {"indices": [2, 3], "verdict": "REJECTED", "qualifier": "", "resolution": "..."}]}

Each finding is in exactly one ruling. When several reviewers report one defect,
one ruling names all their findings with indices. verdict is ACCEPTED or REJECTED; an acceptance you leave unfixed takes the
qualifier "not fixed"; resolution is one sentence. A review with no findings takes
--clean. Record the rulings after the fix commits land: the receipt stamps the
current HEAD, and a HEAD that moved since the review reads as head_advanced.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if reviewStdin == reviewClean {
			return errors.New("pass exactly one of --stdin or --clean")
		}
		repo, err := reviewRepo()
		if err != nil {
			return err
		}
		host := gate.HostID()
		review, err := branchreview.FindReview(branchreview.Dir(), host, args[0])
		if err != nil {
			return err
		}
		var input struct {
			Rulings []branchreview.RulingInput `json:"rulings"`
		}
		if reviewStdin {
			dec := json.NewDecoder(cmd.InOrStdin())
			dec.DisallowUnknownFields()
			if err := dec.Decode(&input); err != nil {
				return fmt.Errorf("read the rulings: %w", err)
			}
		}
		receipt, err := branchreview.Rule(branchreview.Dir(), host, repo, string(harness.Detect()), review, input.Rulings)
		if err != nil {
			return err
		}
		if reviewJSON {
			return writeJSON(cmd.OutOrStdout(), receipt)
		}
		moved := "HEAD is where the review read it"
		switch {
		case receipt.HeadAdvanced:
			moved = "commits landed on the reviewed HEAD since the review"
		case receipt.HeadElsewhere:
			moved = "HEAD is now a commit that does not descend from the reviewed one"
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Recorded %d ruling(s) on the %d finding(s) of review %s: %d accepted, %d accepted and not fixed, %d rejected; %s.\n",
			len(receipt.Rulings), len(review.Findings), receipt.ReviewID, receipt.Accepted, receipt.AcceptedNotFixed, receipt.Rejected, moved)
		return nil
	},
}

var reviewListCmd = &cobra.Command{
	Use:   "list",
	Short: "List this repository's branch reviews and whether they were ruled",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		repo := ""
		if !reviewListAll {
			var err error
			if repo, err = reviewRepo(); err != nil {
				return err
			}
		}
		host := gate.HostID()
		rows, err := branchreview.List(branchreview.Dir(), host, repo)
		if err != nil {
			return err
		}
		if reviewJSON {
			return writeJSON(cmd.OutOrStdout(), rows)
		}
		out := cmd.OutOrStdout()
		if len(rows) == 0 {
			fmt.Fprintln(out, "No branch reviews recorded on this machine for this repository.")
			return nil
		}
		for _, r := range rows {
			fmt.Fprintln(out, reviewListLine(r))
		}
		return nil
	},
}

// reviewListLine is one review's line in steer review list: its range, phase,
// finding count and whether it was ruled or has nothing to rule.
func reviewListLine(r branchreview.Listed) string {
	count := fmt.Sprintf("%d finding(s)", r.Slots)
	if r.Unparsed > 0 {
		count += fmt.Sprintf(", %d reply(ies) unparsed", r.Unparsed)
	}
	status := "not ruled"
	switch {
	case r.Ruled && r.HeadAdvanced:
		status = "ruled, HEAD moved after review"
	case r.Ruled && r.HeadElsewhere:
		status = "ruled, HEAD moved off the reviewed commit"
	case r.Ruled:
		status = "ruled"
	case r.Unparsed > 0:
		status = "cannot be ruled one by one"
	case !r.Rulable:
		status = "nothing to rule"
	}
	return fmt.Sprintf("%s  %s  %s..%s  %-11s %s  %s", r.ReviewID,
		time.Unix(r.TS, 0).Format("2006-01-02 15:04"), shortSHA(r.BaseSHA), shortSHA(r.HeadSHA),
		r.Phase, count, status)
}

// reviewRepo is the git toplevel of the working directory.
func reviewRepo() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	out, ok := gate.Git(cwd, 15*time.Second, "rev-parse", "--show-toplevel")
	if !ok {
		return "", fmt.Errorf("%s is not inside a git repository", cwd)
	}
	return gate.Canonical(strings.TrimSpace(out)), nil
}

// branchProviderUsable refuses a provider that cannot review a branch, and returns a
// warning for an API reviewer that cannot read past the diff.
func branchProviderUsable(cfg gate.Config) (string, error) {
	p, ok := reviewers.FindProvider(cfg.ReviewerProvider)
	if !ok {
		return "", nil
	}
	if err := settings.BranchProviderRefusal(p.ID); err != nil {
		return "", err
	}
	if p.API && (cfg.ReviewerAPITools == "" || cfg.ReviewerAPITools == "off") {
		return "reviewer.api.tools is off, so the branch reviewer reads only the diff " +
			"and no unchanged file; steer config set reviewer.api.tools auto lets it read them", nil
	}
	return "", nil
}

func renderBranchReview(out io.Writer, res branchreview.Result, cfg gate.Config) {
	fmt.Fprintf(out, "Branch review %s: %s..%s (%s), %d commit(s), %d file(s)\n", res.ReviewID,
		shortSHA(res.Base), shortSHA(res.Head), res.BaseRef, res.Commits, res.Files)
	switch res.Phase {
	case branchreview.PhaseSkipEmpty:
		fmt.Fprintln(out, "Nothing to review: the branch changes no reviewable file.")
		return
	case branchreview.PhaseSkipProse:
		fmt.Fprintln(out, "Not reviewed: the branch changes prose files only.")
		return
	case branchreview.PhaseSkipBinary:
		fmt.Fprintln(out, "Not reviewed: the branch only changes the content of existing binary files.")
		return
	}
	fmt.Fprintf(out, "Reviewer %s, %d of %d run(s) answered in %s.\n\n", cfg.ReviewerLabel(), res.Answered,
		len(res.Runs), res.Elapsed.Round(time.Second))
	if res.Answered == 0 {
		return
	}
	if res.Review == "" {
		fmt.Fprintln(out, "NO FINDINGS")
		fmt.Fprintf(out, "\nRecord it: steer review rule %s --clean\n", res.ReviewID)
		return
	}
	if res.Unparsed > 0 {
		fmt.Fprintln(out, res.Review)
		fmt.Fprintf(out, "\n%d reply(ies) above are not wholly in the finding format, so their findings cannot be ruled one by one.\n",
			res.Unparsed)
		renderPolicy(out)
		return
	}
	// Each reviewer numbers its own findings from 1; rule takes these numbers.
	for _, f := range res.Findings {
		fmt.Fprintf(out, "[%d] SEVERITY: %s  (reviewer %d)\n    FILE: %s\n    CLAIM: %s\n    FAILS WHEN: %s\n",
			f.Index, f.Severity, f.Run, f.File, f.Claim, f.FailsWhen)
		if f.Rule != "" {
			fmt.Fprintf(out, "    RULE: %s\n", f.Rule)
		}
		fmt.Fprintln(out)
	}
	renderPolicy(out)
	fmt.Fprintf(out, "Rule on each finding by the number above, after any fix commits: steer review rule %s --stdin\n", res.ReviewID)
}

// renderPolicy prints the adjudication policy the commit gate's deny carries, so a
// branch finding is ruled under the same policy as a commit's.
func renderPolicy(out io.Writer) {
	fmt.Fprintf(out, "\nRule on each finding under this policy:\n\n%s\n\n", gate.AdjudicationPolicy())
}

// branchReviewJSON is the machine-readable form of a review's result.
func branchReviewJSON(res branchreview.Result, cfg gate.Config) map[string]any {
	out := map[string]any{
		"review_id": res.ReviewID, "phase": res.Phase, "base_ref": res.BaseRef,
		"base_sha": res.Base, "head_sha": res.Head, "branch": res.Branch,
		"commits": res.Commits, "files": res.Files, "reviewer": cfg.ReviewerLabel(),
		"answered": res.Answered, "runs": len(res.Runs), "elapsed_ms": res.Elapsed.Milliseconds(),
		"finding_slots": res.Slots, "findings": res.Findings, "review": res.Review,
		"findings_unparsed": res.Unparsed,
	}
	if res.Findings == nil {
		out["findings"] = []branchreview.Finding{}
	}
	if res.Review != "" {
		out["adjudication"] = gate.AdjudicationPolicy()
	}
	if res.Trouble != "" {
		out["trouble"] = res.Trouble
	}
	if len(res.Warnings) > 0 {
		out["warnings"] = res.Warnings
	}
	if res.StoreError != nil {
		out["store_error"] = res.StoreError.Error()
	}
	return out
}

func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

func init() {
	f := reviewBranchCmd.Flags()
	f.StringVar(&reviewBase, "base", "", "the base branch or commit (default: origin's HEAD, then main or master)")
	f.StringVar(&reviewFlags.Provider, "provider", "", "the branch reviewer, outranking branch_reviewer.provider")
	f.StringVar(&reviewFlags.Model, "model", "", "the branch reviewer's model")
	f.StringVar(&reviewFlags.Effort, "effort", "", "the branch reviewer's reasoning effort")
	f.IntVar(&reviewFlags.Runs, "runs", 0, fmt.Sprintf("how many reviewers read the branch, 1 to %d", settings.MaxBranchRuns))
	f.BoolVar(&reviewAllowDirty, "allow-dirty", false, "review despite tracked edits that are not committed, naming them to the reviewer")
	f.BoolVar(&reviewJSON, "json", false, "emit the result as JSON")
	reviewRuleCmd.Flags().BoolVar(&reviewStdin, "stdin", false, "read the rulings as JSON from stdin")
	reviewRuleCmd.Flags().BoolVar(&reviewClean, "clean", false, "record a review with no findings")
	reviewRuleCmd.Flags().BoolVar(&reviewJSON, "json", false, "emit the receipt as JSON")
	reviewListCmd.Flags().BoolVar(&reviewListAll, "all", false, "list every repository's reviews")
	reviewListCmd.Flags().BoolVar(&reviewJSON, "json", false, "emit JSON")
	reviewCmd.AddCommand(reviewBranchCmd, reviewRuleCmd, reviewListCmd)
	rootCmd.AddCommand(reviewCmd)
}
