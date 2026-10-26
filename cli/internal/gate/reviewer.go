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
	OutcomeOK RunOutcome = "ok"
	// OutcomeSalvagedClean is a reply that conforms to the prompt's clean protocol
	// under a failed status. It is kept apart from ok so the ledger still records
	// that the provider errored — a denied command sets status ERROR for the whole
	// invocation even though the model answered in full.
	OutcomeSalvagedClean RunOutcome = "salvaged_clean"
	OutcomeSalvaged      RunOutcome = "salvaged"
	OutcomeLost          RunOutcome = "lost"
	OutcomeEmpty         RunOutcome = "empty"
	OutcomeTimeout       RunOutcome = "timeout"
	OutcomeSpawn         RunOutcome = "spawn"
	OutcomeExit          RunOutcome = "exit"
	OutcomeUnparseable   RunOutcome = "unparseable"
	// The outcomes below come from an API reviewer, which reads a status code where a
	// CLI exits. truncated is a reply cut at the output limit, kept apart from salvage
	// because a cut reply can drop the finding that decides the commit.
	OutcomeTruncated   RunOutcome = "truncated"
	OutcomeAuth        RunOutcome = "auth"
	OutcomeRateLimited RunOutcome = "rate_limited"
	OutcomeNoKey       RunOutcome = "no_key"
)

// ReviewRun is one reviewer run. Text is the usable review or empty.
type ReviewRun struct {
	Text    string
	Outcome RunOutcome
	Detail  string
	// ResponseModel is the model the provider says answered, "" when it says none.
	ResponseModel string
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
	// ResponseModel is the model the provider says answered. An API endpoint can
	// route a configured name to another model, so this is recorded beside the
	// configured one rather than in place of it.
	ResponseModel string
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

// EndpointReviewer is a Reviewer that sends the prompt to a network endpoint.
type EndpointReviewer interface {
	// Endpoint is the host the prompt goes to, never the full URL.
	Endpoint() string
}

// EndpointOf is the host a reviewer sends to, "" for one that runs a CLI.
func EndpointOf(r Reviewer) string {
	if e, ok := r.(EndpointReviewer); ok {
		return e.Endpoint()
	}
	return ""
}

// Classify turns a provider's raw result into a run outcome. A body that conforms to
// the review protocol — findings, or the clean reply — is a completed review whatever
// the status says: `salvaged` and `salvaged_clean`. Only a body conforming to neither
// is dropped as `lost`.
func Classify(raw RawRun) ReviewRun {
	run := classify(raw)
	run.ResponseModel = raw.ResponseModel
	return run
}

func classify(raw RawRun) ReviewRun {
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
	// A lost run is dropped whole, so its detail is the only account of what it said.
	// A provider that reports no error leaves the body as that account.
	lost := func() ReviewRun {
		if detail == "" {
			return ReviewRun{Outcome: OutcomeLost, Detail: text}
		}
		return ReviewRun{Outcome: OutcomeLost, Detail: detail}
	}
	if raw.Status == "SUCCESS" {
		// A success still has to be a review. A run whose tool calls were refused
		// answers with the refusal and no findings, and counting that as a clean
		// review is how a commit lands on evidence nobody produced.
		if CountFindings(text) > 0 || IsNoFindings(text) {
			return ReviewRun{Text: text, Outcome: OutcomeOK}
		}
		return lost()
	}
	if CountFindings(text) > 0 {
		return ReviewRun{Text: text, Outcome: OutcomeSalvaged, Detail: detail}
	}
	if IsCleanReply(text) {
		return ReviewRun{Text: text, Outcome: OutcomeSalvagedClean, Detail: detail}
	}
	return lost()
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
		// A reply may OPEN with the clean words and still carry a finding below them.
		// The finding count is what decides, so the opener can never bury one.
		if CountFindings(run.Text) > 0 || !IsNoFindings(run.Text) {
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
	models := map[string]int{}
	for _, run := range runs {
		outcomes[string(run.Outcome)]++
		if run.ResponseModel != "" {
			models[run.ResponseModel]++
		}
		if run.Detail != "" && run.Outcome != OutcomeOK {
			errors = append(errors, string(run.Outcome)+": "+truncate(run.Detail, cfg.MaxDetailChars))
		}
	}
	row := Row{"reviewer_outcomes": outcomes}
	if len(errors) > 0 {
		row["reviewer_errors"] = errors
	}
	if len(models) > 0 {
		row["reviewer_response_models"] = models
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

// OffFormatReviews counts the reviews that reached the composed review carrying no
// indexed severity line and no clean reply. That pair is the signature of a reviewer
// that wrote its findings as prose: the strict count reads zero while the text holds
// real defects, so the row records a clean diff the reviewers did not find. It is
// measurement only and decides nothing.
func OffFormatReviews(reviews []string) int {
	total := 0
	for _, review := range reviews {
		if strings.TrimSpace(review) == "" || IsNoFindings(review) {
			continue
		}
		if CountFindings(review) == 0 {
			total++
		}
	}
	return total
}

func truncate(text string, n int) string {
	if len(text) <= n {
		return text
	}
	return text[:n]
}
