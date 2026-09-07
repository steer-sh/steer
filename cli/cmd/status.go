package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steer-sh/steer-cli/internal/cadence"
	"github.com/steer-sh/steer-cli/internal/evidence"
	"github.com/steer-sh/steer-cli/internal/gate/reviewers"
	"github.com/steer-sh/steer-cli/internal/status"
	"github.com/steer-sh/steer-cli/internal/styles"
)

var (
	statusJSON        bool
	statusDays        int
	statusProjectOnly bool
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the steer loop's state — setup, what needs a decision, and results",
	Long: `status composes one read-only snapshot of the steer loop and renders it. The unit
is the recurring problem and its measured result, not the hook count. It reads
every recorded problem and fix, the recent reviews, the work the deterministic
tier flagged, the arming state, and how much evidence this machine holds. It
writes no state.

--json prints the versioned SystemStatus contract on stdout, progress on stderr —
the same discipline as 'steer report'. That contract keeps the machine's own
words. An agent runs 'steer status --json' before installing a hook, to read the
full inventory and skip a problem already decided.`,
	Example: `  steer status
  steer status --json
  steer status --days 7 --project-only`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// status never writes: it reads the stored snapshots rather than rebuilding
		// them, so the one writer stays `steer report refresh`.
		// A snapshot renders under an unresolvable provider: it names no provider,
		// so the configured model is the honest value to print.
		cfg, _, resolveErr := resolvedGateConfig()
		s, err := status.Collect(status.Options{
			GateStateDir: cfg.StateDir,
			Config:       cfg,
			WindowDays:   statusDays,
			AllProjects:  !statusProjectOnly,
			Scope:        currentScope(),
			Evidence: evidence.Gather(evidence.Options{
				GateStateDir: cfg.StateDir,
				AllProjects:  !statusProjectOnly,
			}),
		})
		if err != nil {
			return err
		}
		if statusJSON {
			data, err := s.MarshalIndent()
			if err != nil {
				return fmt.Errorf("failed to marshal status: %w", err)
			}
			fmt.Println(string(data))
			return nil
		}
		renderStatus(s, reviewerProbe(cfg, resolveErr))
		return nil
	},
}

// renderStatus prints the loop-first human view. It leads with the one question a
// reader arrives with — will a commit here be reviewed, and what stands in the way —
// because the posture inventory below answers it only to a reader who already knows
// how registration, trust and reviewer availability combine.
func renderStatus(s *status.SystemStatus, probe reviewers.Probe) {
	width := styles.Width()
	fmt.Println()
	fmt.Println(styles.MainHeader.Render(styles.Logo() + " status"))
	fmt.Println()

	renderReadiness(status.ReadinessOf(s, probe), width)
	fmt.Println()
	renderPosture(s, width)
	fmt.Println()
	renderReviewer(s, width)
	fmt.Println()
	renderAttention(s, width)
}

// renderReadiness prints the short answer and the steps that remain. Each step names
// the command that clears it, so the reader never has to derive one from the
// inventory below.
func renderReadiness(r status.Readiness, width int) {
	head := styles.SuccessText.Render(r.Headline)
	if !r.Reviewing {
		head = styles.WarningText.Render(r.Headline)
	}
	fmt.Println(styles.SectionHeader.Render("Commit review"))
	fmt.Print(styles.Wrap(head, "  ", width))
	if len(r.Steps) > 0 && r.StepsLead != "" {
		fmt.Print(styles.Wrap(r.StepsLead, "  ", width))
	}
	for _, step := range r.Steps {
		// The bullet is the label, so a step that runs past the terminal continues
		// under its own text rather than under the marker.
		fmt.Print(styles.Field("·", []string{step.Title}, "  ", 1, width))
		if step.Fix != "" {
			fmt.Print(styles.Wrap(styles.MutedText.Render("run: "+step.Fix), "    ", width))
		}
	}
	if len(r.Unverified) > 0 {
		fmt.Print(styles.Wrap(styles.Bold.Render("Trust unverified by Steer:"), "  ", width))
		for _, note := range r.Unverified {
			fmt.Print(styles.Field("·", []string{note}, "  ", 1, width))
		}
	}
	if r.Note != "" {
		fmt.Print(styles.Wrap(styles.MutedText.Render(r.Note), "  ", width))
	}
}

// postureLabelWidth is the column the Arming labels share. Every row names its
// label in that column, so the values line up whatever the longest label is.
const postureLabelWidth = 16

// renderPosture prints the arming inventory. It carries the same rows the
// dashboard's Arming panel acts on — the commit gate, the denials recorder and
// the guard runner. The resolver is not among them: install and remove write it
// with the commit gate in one operation, so a row of its own reported a second
// state that can never differ from the first.
func renderPosture(s *status.SystemStatus, width int) {
	fmt.Println(styles.SectionHeader.Render("Arming"))
	for _, a := range s.Posture.CommitGate {
		postureRow("Commit gate", armingRow(a), width)
	}
	for _, a := range s.Posture.Denials {
		postureRow("Denials recorder", armingRow(a), width)
	}
	for _, m := range s.Posture.Masters {
		phrase := status.MasterPhrase(m)
		if phrase == "" {
			continue
		}
		if m.Stale() {
			phrase = styles.WarningText.Render(phrase)
		}
		row := fmt.Sprintf("%s, %s scope: %s", status.HarnessLabel(m.Harness), m.Scope, phrase)
		if o := status.MasterObservedPhrase(m); o != "" {
			row += styles.MutedText.Render(" · " + o)
		}
		postureRow("Guard runner", row, width)
	}
	postureRow("History", corporaLine(s), width)
	postureRow("Guard blocks", fmt.Sprintf("%d", s.Telemetry.AuditFirings), width)
}

func renderReviewer(s *status.SystemStatus, width int) {
	fmt.Println(styles.SectionHeader.Render("Reviewer"))
	postureRow("Reviewer", s.Posture.Reviewer, width)
	postureRow("Review log", s.Posture.Ledger, width)
	postureRow("Produced by", s.Posture.Instrument, width)
}

// postureRow prints one label and its value. A value carrying its own newline —
// an arming row with a warning under it — keeps that break and every line lands
// in the value column.
func postureRow(label, value string, width int) {
	fmt.Print(styles.Field(label, strings.Split(value, "\n"), "  ", postureLabelWidth, width))
}

// armingRow names the harness a registration lives in, because one store serves both
// and a scope alone no longer identifies a registration.
func armingRow(a status.ScopeArming) string {
	row := fmt.Sprintf("%s, %s scope: %s", status.HarnessLabel(a.Harness), a.Scope, armingLabel(a))
	if t := status.TrustPhrase(a.Trust); t != "" && a.Registered {
		row += styles.MutedText.Render(" (" + t + ")")
	}
	if o := status.ObservedPhrase(a); o != "" && a.Registered {
		row += styles.MutedText.Render(" · " + o)
	}
	if r := status.ResolvePhrase(a); r != "" {
		row += "\n" + styles.WarningText.Render(r)
	}
	return row
}

func armingLabel(a status.ScopeArming) string {
	switch {
	case a.Registered && a.Resolvable == status.ResolveMissing:
		return styles.WarningText.Render("broken")
	case a.Effective && a.Timeout > 0:
		return styles.SuccessText.Render(fmt.Sprintf("armed, timeout %ds", a.Timeout))
	case a.Effective:
		return styles.SuccessText.Render("armed")
	case a.Registered:
		return styles.WarningText.Render("inert") + " (registered, not effective)"
	default:
		return styles.MutedText.Render("off")
	}
}

// corporaLine renders the corpus sweep verbatim: the same three corpora the "X of
// N present" count is over, each with its own size, so the count and the detail
// describe one set.
func corporaLine(s *status.SystemStatus) string {
	if len(s.Corpora) == 0 {
		return styles.MutedText.Render("none")
	}
	present := 0
	parts := make([]string, 0, len(s.Corpora))
	for _, c := range s.Corpora {
		if c.Present {
			present++
		}
		parts = append(parts, fmt.Sprintf("%s %d", corpusLabel(c.Corpus), c.Size))
	}
	msg := fmt.Sprintf("%d of %d present — %s", present, len(s.Corpora), strings.Join(parts, ", "))
	if present == 0 {
		return styles.MutedText.Render(msg)
	}
	return msg
}

func corpusLabel(corpus string) string {
	switch corpus {
	case "review-findings":
		return "review findings"
	case "transcript-friction":
		return "session history"
	case "auto-denials":
		return "automatic denials"
	default:
		return "other evidence"
	}
}

// problemIndent is the column a problem's title sits in, and problemFieldIndent
// the one its stacked labels sit in under it. They are the dashboard's own two
// columns, so one entry reads the same in either place.
const (
	problemIndent      = "    "
	problemFieldIndent = "      "
	problemLabelWidth  = 9
)

// renderAttention groups the queue by the action each row asks of the reader,
// rather than by the verdict token, and renders those groups in ActionOrder. Each
// entry is a title and stacked labeled lines, the shape the dashboard's Problems
// panel uses for the same records.
func renderAttention(s *status.SystemStatus, width int) {
	fmt.Println(styles.SectionHeader.Render("Problems"))
	if s.Summary.Total == 0 {
		fmt.Print(styles.Wrap(styles.MutedText.Render("no problems recorded on this machine"), "  ", width))
	} else {
		fmt.Print(styles.Wrap(fmt.Sprintf("%d recorded %s", s.Summary.Total, problemWord(s.Summary.Total)), "  ", width))
	}
	a := s.Attention
	rate := s.Rate
	buckets := map[string][]string{}

	for _, iv := range a.Interventions {
		un := status.Unmeasurable(iv.Verdict, iv.Unit, iv.RequiredAfter, rate)
		act := status.ActionFor(iv.Verdict, un)
		buckets[act] = append(buckets[act], interventionBlock(iv, un, rate, width))
	}
	if s.Posture.Unresolved > 0 {
		buckets[status.ActionAdjudicate] = append(buckets[status.ActionAdjudicate],
			problemEntry(status.OutcomesWaitingPhrase(s.Posture.Unresolved), []problemField{
				{"Next", []string{"read it from your session history — run: " + status.BackfillCommand}},
			}, width))
	}
	for _, c := range a.Candidates {
		buckets[status.ActionAdjudicate] = append(buckets[status.ActionAdjudicate],
			candidateBlock(c, width))
	}

	empty := true
	for _, act := range status.ActionOrder {
		if len(buckets[act]) > 0 {
			empty = false
		}
	}
	if empty && len(a.Unscored) == 0 {
		fmt.Print(styles.Wrap(styles.SuccessText.Render("nothing pending — the loop is quiet"), "  ", width))
		return
	}
	for _, act := range status.ActionOrder {
		rows := buckets[act]
		if len(rows) == 0 {
			continue
		}
		fmt.Print(styles.Wrap(actionHeader(act), "  ", width))
		for _, r := range rows {
			fmt.Print(r)
		}
	}
	if len(a.Unscored) > 0 {
		fmt.Print(styles.Wrap(fmt.Sprintf("%d records are not assessed: this evidence has no reader",
			len(a.Unscored)), "  ", width))
	}
}

// actionHeader colors a section by whether the reader is losing ground.
func actionHeader(act string) string {
	head := status.ActionHeading[act]
	switch act {
	case status.ActionHarmful:
		return styles.WarningText.Render(head)
	case status.ActionAdjudicate, status.ActionRepair:
		return styles.InfoText.Bold(true).Render(head)
	}
	return styles.MutedText.Bold(true).Render(head)
}

// problemField is one labeled line of an entry, carrying every sentence that
// files under that label.
type problemField struct {
	label  string
	values []string
}

// problemEntry renders one entry: its title, then its stacked labels. A field
// with nothing to say prints no label.
func problemEntry(title string, fields []problemField, width int) string {
	out := styles.Wrap(styles.Bold.Render(title), problemIndent, width)
	for _, f := range fields {
		values := make([]string, 0, len(f.values))
		for _, v := range f.values {
			if strings.TrimSpace(v) != "" {
				values = append(values, v)
			}
		}
		if len(values) == 0 {
			continue
		}
		out += styles.Field(f.label, values, problemFieldIndent, problemLabelWidth, width)
	}
	return out
}

// interventionBlock is one recorded problem. It reads the same fields the
// dashboard's summary reads — what the fix is, what the result is, and one reason
// or one step — plus the counts, which the dashboard keeps behind its detail key
// and the terminal has nowhere else to put.
func interventionBlock(iv cadence.Intervention, unmeasurable bool, rate status.ChainRate, width int) string {
	unit := iv.Unit
	if unit == "" {
		unit = "unit"
	}
	v := status.ProblemView{
		Declined:       iv.Tier == "" || iv.Tier == "none",
		Tier:           iv.Tier,
		Target:         iv.Target,
		Intervention:   iv.Intervention,
		Verdict:        iv.Verdict,
		Unit:           unit,
		Unmeasurable:   unmeasurable,
		BeforeCoverage: iv.BeforeCoverage,
		AfterCoverage:  iv.AfterCoverage,
		Attribution:    iv.Attribution,
		Confounds:      iv.Confounds,
	}
	result := []string{styles.MutedText.Render(status.ResultLabel(v))}
	if limit := status.ComparisonLimit("composition", iv.Composition); limit != "" {
		result = append(result, styles.MutedText.Render(limit))
		for _, cause := range status.ComparisonCauses(iv.Composition) {
			result = append(result, styles.MutedText.Render("· "+cause))
		}
	}

	evidence := []string{status.RecurrencePhrase(iv.Before, iv.After, unit)}
	if e := status.EffectClaimPhrase(iv.Verdict, iv.Effect); e != "" {
		evidence = append(evidence, e)
	}
	if a := status.AttributionPhrase(iv.Attribution); a != "" {
		evidence = append(evidence, a)
	}
	if iv.BeforeCoverage.UnknownEvidence > 0 || iv.AfterCoverage.UnknownEvidence > 0 {
		evidence = append(evidence, fmt.Sprintf(
			"%d of the %d earlier %s and %d of the %d later %s carry no recorded decision",
			iv.BeforeCoverage.UnknownEvidence, iv.BeforeCoverage.Total,
			status.UnitName(unit, iv.BeforeCoverage.Total),
			iv.AfterCoverage.UnknownEvidence, iv.AfterCoverage.Total,
			status.UnitName(unit, iv.AfterCoverage.Total)))
	}

	why := []string{status.ProblemWhy(v)}
	if status.ComparisonLimit("confound", iv.Confounds) != "" {
		for _, cause := range status.ComparisonCauses(iv.Confounds) {
			why = append(why, "· "+cause)
		}
	}

	next := []string{status.ProblemNext(v)}
	if r := status.RatePhrase(iv.RequiredAfter, unit, rate); r != "" {
		next = append(next, r)
	} else if r := status.RequiredPhrase(iv.RequiredAfter, unit, iv.Verdict); r != "" {
		next = append(next, r)
	}
	if unmeasurable || iv.Verdict == "harmful" {
		next = append(next, status.RemovalPhrase(iv.Tier, iv.Target),
			styles.MutedText.Render("run: "+status.RetireCommand(iv.ID)))
	}

	return problemEntry(iv.Class, []problemField{
		{"Problem", []string{iv.Problem}},
		{status.FixLabel(v), []string{status.FixSummary(v)}},
		{"Result", result},
		{"Evidence", evidence},
		{"Why", why},
		{"Next", next},
	}, width)
}

// candidateBlock is one queued row: evidence nobody has decided yet. It names the
// same handoff the dashboard names, because neither surface authors a record.
func candidateBlock(c cadence.Candidate, width int) string {
	title := c.Pattern
	next := status.CandidateNext
	if title == "" {
		title = "Review findings not yet grouped into a problem"
		if c.Dump != "" {
			next = "read them with: " + c.Dump
		}
	}
	return problemEntry(title, []problemField{
		{"Evidence", []string{status.CandidateEvidence(c)}},
		{"Next", []string{next}},
	}, width)
}

func problemWord(n int) string {
	if n == 1 {
		return "problem"
	}
	return "problems"
}

func init() {
	rootCmd.AddCommand(statusCmd)
	statusCmd.Flags().BoolVar(&statusJSON, "json", false, "Emit the versioned SystemStatus JSON on stdout")
	statusCmd.Flags().IntVar(&statusDays, "days", 30, "Transcript look-back window in days for the attention refresh")
	statusCmd.Flags().BoolVar(&statusProjectOnly, "project-only", false, "Restrict the transcript scan to the current working directory")
}
