package gate

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// RunOutcome names which of the ways a reviewer run ended. Every failure used to
// arrive as the same nil, so a denied tool call, a timeout and a missing binary were
// one indistinguishable event in the log.
type RunOutcome string

const (
	OutcomeOK          RunOutcome = "ok"
	OutcomeSalvaged    RunOutcome = "salvaged"
	OutcomeLost        RunOutcome = "lost"
	OutcomeEmpty       RunOutcome = "empty"
	OutcomeTimeout     RunOutcome = "timeout"
	OutcomeSpawn       RunOutcome = "spawn"
	OutcomeExit        RunOutcome = "exit"
	OutcomeUnparseable RunOutcome = "unparseable"
)

// ReviewRun is one reviewer run. Text is the usable review or empty.
type ReviewRun struct {
	Text    string
	Outcome RunOutcome
	Detail  string
}

// RawRun is what a Reviewer returns before outcome classification: the model's reply,
// the provider's own status and error strings, and how the process ended.
type RawRun struct {
	Response string
	Status   string
	Error    string
	// Outcome, when set, is a terminal classification the provider already made
	// (timeout, spawn, exit, unparseable). Classify honours it and stops.
	Outcome RunOutcome
	Detail  string
}

// Reviewer runs one red-team pass over a prompt. Implementations live in
// internal/gate/reviewers; the engine knows only this.
type Reviewer interface {
	// Name identifies the provider in the ledger and in operator messages.
	Name() string
	// ModelLabel is the label the instrument records. It must name what ACTUALLY ran: the
	// instrument field is the ledger's claim about the thing doing the reviewing, and
	// a row stamped with one provider's default pin while another provider ran is a
	// false claim in the measurement record.
	ModelLabel() string
	// Review runs one pass with cwd set to the repo, so the reviewer's file reads
	// resolve against the code under review rather than wherever the gate started.
	Review(ctx context.Context, prompt, cwd string) RawRun
}

// Classify turns a provider's raw result into a run outcome. A non-success status
// still carrying findings is kept as `salvaged`; one carrying none is dropped as
// `lost`, because only the drop errs toward denying.
func Classify(raw RawRun) ReviewRun {
	if raw.Outcome != "" {
		return ReviewRun{Outcome: raw.Outcome, Detail: raw.Detail}
	}
	text := strings.TrimSpace(raw.Response)
	detail := strings.TrimSpace(raw.Error)
	if text == "" {
		if detail == "" {
			detail = "status=" + raw.Status
		}
		return ReviewRun{Outcome: OutcomeEmpty, Detail: detail}
	}
	if raw.Status == "SUCCESS" {
		return ReviewRun{Text: text, Outcome: OutcomeOK}
	}
	if CountFindings(text) > 0 {
		return ReviewRun{Text: text, Outcome: OutcomeSalvaged, Detail: detail}
	}
	return ReviewRun{Outcome: OutcomeLost, Detail: detail}
}

// Panel is the reviewer panel: N concurrent runs of the same reviewer over the same
// prompt, raw output concatenated with no merge pass.
type Panel struct {
	cfg      Config
	reviewer Reviewer
}

func NewPanel(cfg Config, reviewer Reviewer) *Panel {
	return &Panel{cfg: cfg, reviewer: reviewer}
}

// PanelResult holds what one round of reviewing produced.
type PanelResult struct {
	// Reviews are the answered runs that reported findings.
	Reviews []string
	// Answered counts every run that returned usable text, NO FINDINGS included.
	Answered int
	Runs     []ReviewRun
}

// Run fires the panel.
func (p *Panel) Run(ctx context.Context, prompt, cwd string) PanelResult {
	runs := make([]ReviewRun, p.cfg.ReviewerRuns)
	var wg sync.WaitGroup
	for i := range runs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			runCtx, cancel := context.WithTimeout(ctx, p.cfg.ReviewTimeout)
			defer cancel()
			runs[i] = Classify(p.reviewer.Review(runCtx, prompt, cwd))
		}(i)
	}
	wg.Wait()

	result := PanelResult{Runs: runs}
	for _, run := range runs {
		if run.Text == "" {
			continue
		}
		result.Answered++
		if !strings.HasPrefix(strings.ToUpper(run.Text), "NO FINDINGS") {
			result.Reviews = append(result.Reviews, run.Text)
		}
	}
	return result
}

// ReviewerReport names how each run ended, so a thin panel is diagnosable from the
// ledger alone.
func ReviewerReport(cfg Config, runs []ReviewRun) Row {
	outcomes := map[string]int{}
	var errors []string
	for _, run := range runs {
		outcomes[string(run.Outcome)]++
		if run.Detail != "" && run.Outcome != OutcomeOK {
			errors = append(errors, string(run.Outcome)+": "+truncate(run.Detail, cfg.MaxDetailChars))
		}
	}
	row := Row{"reviewer_outcomes": outcomes}
	if len(errors) > 0 {
		row["reviewer_errors"] = errors
	}
	return row
}

// ReviewerTrouble is one line naming why runs did not answer, for the operator channel.
func ReviewerTrouble(runs []ReviewRun) string {
	seen := map[string]bool{}
	var lost []string
	for _, run := range runs {
		if run.Text != "" {
			continue
		}
		line := string(run.Outcome)
		if run.Detail != "" {
			line += " (" + truncate(run.Detail, 120) + ")"
		}
		if !seen[line] {
			seen[line] = true
			lost = append(lost, line)
		}
	}
	sort.Strings(lost)
	return strings.Join(lost, "; ")
}

// ComposeReview concatenates the raw reviews with no merge pass, and counts the
// finding slots across them.
func ComposeReview(reviews []string) (string, int) {
	parts := make([]string, 0, len(reviews))
	total := 0
	for i, review := range reviews {
		parts = append(parts, "--- reviewer "+strconv.Itoa(i+1)+" ---\n"+review)
		total += CountFindings(review)
	}
	return strings.Join(parts, "\n\n"), total
}

// LooseFindingCount is the measurement-only total across the panel's reviews.
func LooseFindingCount(reviews []string) int {
	total := 0
	for _, review := range reviews {
		total += CountFindingsLoose(review)
	}
	return total
}

func truncate(text string, n int) string {
	if len(text) <= n {
		return text
	}
	return text[:n]
}
