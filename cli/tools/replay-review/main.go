// Command replay-review runs one reviewer panel over a saved round diff, through the
// live gate's context, prompt and panel code, and writes the result as JSON. It writes
// no ledger row. tests/replay_reviewers.py prepares the worktrees and drives it.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/steer-sh/steer-cli/internal/gate"
	"github.com/steer-sh/steer-cli/internal/settings"
)

// omittedRe matches the gate's notices naming files the saved diff left out.
var omittedRe = regexp.MustCompile(`(?s)NOT (?:by )?the commit: (.*?)\. These files ARE`)

// binaryRe matches a binary section, which the replay leaves unstaged.
var binaryRe = regexp.MustCompile(`(?m)^Binary files (.+) and (.+) differ$`)

// changedPaths is the commit's file list, with renames split into a deletion and an
// addition as the live gate lists them: the staged changes against base, plus the
// files the gate's notices say it excluded or dropped from the saved diff and its
// binary sections, which the staged tree lacks.
func changedPaths(wt, base, diff string) ([]string, error) {
	out, err := exec.Command("git", "-C", wt, "diff", "--cached", "--name-only",
		"--no-relative", "--no-renames", "-z", base).Output()
	if err != nil {
		return nil, fmt.Errorf("listing staged paths against %s: %w", base, err)
	}
	var paths []string
	seen := map[string]bool{}
	add := func(p string) {
		if p != "" && !seen[p] {
			seen[p] = true
			paths = append(paths, p)
		}
	}
	for _, p := range strings.Split(string(out), "\x00") {
		add(p)
	}
	preamble, _, _ := strings.Cut(diff, "diff --git ")
	for _, m := range omittedRe.FindAllStringSubmatch(preamble, -1) {
		for _, p := range strings.Split(m[1], ", ") {
			add(p)
		}
	}
	for _, m := range binaryRe.FindAllStringSubmatch(diff, -1) {
		for _, side := range m[1:] {
			if unquoted, err := strconv.Unquote(side); err == nil && strings.HasPrefix(side, `"`) {
				side = unquoted
			}
			if strings.HasPrefix(side, "a/") || strings.HasPrefix(side, "b/") {
				add(side[2:])
			}
		}
	}
	return paths, nil
}

func main() {
	provider := flag.String("provider", "", "reviewer provider (required)")
	model := flag.String("model", "", "reviewer model (required)")
	effort := flag.String("effort", "", "reviewer effort; empty for the provider's own")
	runs := flag.Int("runs", 3, "reviewers in the panel")
	base := flag.String("base", "HEAD", "the revision the saved diff was applied to")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: replay-review --provider P --model M [--effort E] [--runs N] [--base REV] <worktree> <diff-file> <subject> <out.json>")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() != 4 || *provider == "" || *model == "" || *runs < 1 {
		flag.Usage()
		os.Exit(2)
	}
	if err := replay(*provider, *model, *effort, *base, *runs, flag.Arg(0), flag.Arg(1), flag.Arg(2), flag.Arg(3)); err != nil {
		fmt.Fprintln(os.Stderr, "replay-review:", err)
		os.Exit(1)
	}
}

func replay(provider, model, effort, base string, runs int, wt, diffFile, subject, out string) error {
	cfg, err := gate.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "replay-review:", err, "— using the shipped defaults")
	}
	cfg.ReviewerProvider = provider
	cfg.ReviewerModel = model
	cfg.ReviewerModelOverride = model
	cfg.ReviewerEffort = effort
	cfg.ReviewerRuns = runs
	cfg, reviewer, err := settings.Resolve(cfg, func(w string) { fmt.Fprintln(os.Stderr, "replay-review:", w) })
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(diffFile)
	if err != nil {
		return err
	}
	diff := string(raw)
	paths, err := changedPaths(wt, base, diff)
	if err != nil {
		return err
	}
	ctxBlock := gate.BuildContext(cfg, wt, gate.ChangedSymbols(diff, paths), paths)
	prompt := gate.BuildReviewPrompt(gate.NeutralizeTags(subject), gate.NeutralizeTags(diff),
		ctxBlock, gate.LoadAddendum(wt))
	start := time.Now()
	// The round's diff is staged on its base, so the index is what the commit lands.
	result := gate.NewPanel(cfg, reviewer).Run(context.Background(), prompt, wt, gate.DiffSpec{StagedOnly: true})
	elapsed := time.Since(start)
	_, findings := gate.ComposeReview(result.Reviews)
	verdict := "deny"
	if result.Answered == 0 {
		verdict = "ungated"
	} else if len(result.Reviews) == 0 {
		verdict = "clean"
	}
	row := gate.Row{
		"model": cfg.ReviewerModel, "effort": cfg.ReviewerEffort, "provider": reviewer.Name(),
		"runs": cfg.ReviewerRuns, "elapsed_s": elapsed.Seconds(), "answered": result.Answered,
		"finding_slots": findings, "verdict": verdict, "paths": len(paths),
		"context_chars": len(ctxBlock), "prompt_chars": len(prompt),
	}
	for k, v := range gate.ReviewerReport(cfg, result.Runs) {
		row[k] = v
	}
	if len(result.Reviews) > 0 {
		for k, v := range gate.FindingShape(result.Reviews, paths) {
			row[k] = v
		}
	}
	texts := []string{}
	for _, r := range result.Runs {
		texts = append(texts, r.Text)
	}
	row["texts"] = texts
	data, err := json.MarshalIndent(row, "", " ")
	if err != nil {
		return err
	}
	fmt.Printf("%s %s: %s slots=%d answered=%d/%d %.0fs\n", reviewer.Name(), cfg.ReviewerModel,
		verdict, findings, result.Answered, cfg.ReviewerRuns, elapsed.Seconds())
	if err := os.WriteFile(out, data, 0o644); err != nil {
		return err
	}
	if verdict == "ungated" {
		return fmt.Errorf("no reviewer in the panel answered: %s", gate.ReviewerTrouble(result.Runs))
	}
	return nil
}
