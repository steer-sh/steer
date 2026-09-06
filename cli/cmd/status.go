package cmd

import (
	"bytes"
	"fmt"
	"os"
	"sort"
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
	statusHTML        bool
	statusOut         string
	statusDays        int
	statusProjectOnly bool
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the steer loop's state — setup, what needs a decision, and results",
	Long: `status composes one read-only snapshot of the steer loop and renders it. The
unit is the failure class and its intervention's measured verdict, not the hook
count. It reads the scored intervention inventory, the recent review chains, the
attention subset the deterministic tier flags, the arming posture, and the corpus
sizes. It writes no state.

--json prints the versioned SystemStatus contract on stdout, progress on stderr —
the same discipline as 'steer report'. An agent runs 'steer status --json' before
installing a hook to see the full class inventory and skip a class already scored.

--html renders the same snapshot as one self-contained HTML file: no server, no
runtime dependency. It writes to --out, or to stdout when --out is absent.`,
	Example: `  steer status
  steer status --json
  steer status --html --out steer-status.html
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
		if statusJSON && statusHTML {
			return fmt.Errorf("--json and --html are separate render targets; pass one")
		}
		if statusHTML {
			return writeStatusHTML(s, reviewerProbe(cfg, resolveErr))
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

// writeStatusHTML renders the snapshot to --out, or to stdout when --out is
// absent. It buffers first, so a render error leaves no half-written file.
//
// The readiness answer is computed here rather than read from the snapshot: the
// reviewer probe reads this process's PATH, so the page states what this machine
// saw at export time and says so on the page.
func writeStatusHTML(s *status.SystemStatus, probe reviewers.Probe) error {
	answer := status.ReadinessOf(s, probe)
	host, err := os.Hostname()
	if err != nil {
		host = ""
	}
	var buf bytes.Buffer
	if err := s.RenderHTML(&buf, status.Export{Readiness: &answer, Host: host}); err != nil {
		return err
	}
	if statusOut == "" {
		_, err := os.Stdout.Write(buf.Bytes())
		return err
	}
	if err := os.WriteFile(statusOut, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("failed to write %s: %w", statusOut, err)
	}
	fmt.Fprintf(os.Stderr, "wrote %s\n", statusOut)
	return nil
}

// renderStatus prints the loop-first human view. It leads with the one question a
// reader arrives with — will a commit here be reviewed, and what stands in the way —
// because the posture inventory below answers it only to a reader who already knows
// how registration, trust and reviewer availability combine.
func renderStatus(s *status.SystemStatus, probe reviewers.Probe) {
	fmt.Println()
	fmt.Println(styles.MainHeader.Render(styles.Logo() + " status"))
	fmt.Println()

	renderReadiness(status.ReadinessOf(s, probe))
	fmt.Println()
	renderPosture(s)
	fmt.Println()
	renderAttention(s)
	fmt.Println()
	renderSummary(s)
}

// renderReadiness prints the short answer and the steps that remain. Each step names
// the command that clears it, so the reader never has to derive one from the
// inventory below.
func renderReadiness(r status.Readiness) {
	head := styles.SuccessText.Render(r.Headline)
	if !r.Reviewing {
		head = styles.WarningText.Render(r.Headline)
	}
	fmt.Println(styles.SectionHeader.Render("Answer"))
	fmt.Printf("  %s\n", head)
	if len(r.Steps) > 0 && r.StepsLead != "" {
		fmt.Printf("  %s\n", r.StepsLead)
	}
	for _, step := range r.Steps {
		fmt.Printf("  · %s\n", step.Title)
		if step.Fix != "" {
			fmt.Printf("    run: %s\n", step.Fix)
		}
	}
	if r.Note != "" {
		fmt.Printf("  %s\n", styles.MutedText.Render(r.Note))
	}
}

func renderPosture(s *status.SystemStatus) {
	fmt.Println(styles.SectionHeader.Render("Posture"))
	fmt.Printf("  Instrument   %s\n", s.Posture.Instrument)
	fmt.Printf("  Reviewer     %s\n", s.Posture.Reviewer)
	fmt.Printf("  Ledger       %s\n", s.Posture.Ledger)
	for _, a := range s.Posture.CommitGate {
		fmt.Printf("  Commit gate  %s\n", armingRow(a))
	}
	for _, a := range s.Posture.Denials {
		fmt.Printf("  Denials      %s\n", armingRow(a))
	}
	for _, a := range s.Posture.Resolver {
		fmt.Printf("  Resolver     %s\n", armingRow(a))
	}
	for _, m := range s.Posture.Masters {
		phrase := status.MasterPhrase(m)
		if phrase == "" {
			continue
		}
		row := fmt.Sprintf("%s, %s scope: %s", m.Harness, m.Scope, phrase)
		if m.Stale() {
			row = fmt.Sprintf("%s, %s scope: %s", m.Harness, m.Scope, styles.WarningText.Render(phrase))
		}
		if o := status.MasterObservedPhrase(m); o != "" {
			row += styles.MutedText.Render(" · " + o)
		}
		fmt.Printf("  Guard master %s\n", row)
	}
	fmt.Printf("  Evidence     %s\n", corporaLine(s))
	fmt.Printf("  Guard blocks %d action(s) blocked\n", s.Telemetry.AuditFirings)
}

// armingRow names the harness a registration lives in, because one store serves both
// and a scope alone no longer identifies a registration.
func armingRow(a status.ScopeArming) string {
	row := fmt.Sprintf("%s, %s scope: %s", a.Harness, a.Scope, armingLabel(a))
	if t := status.TrustPhrase(a.Trust); t != "" && a.Registered {
		row += styles.MutedText.Render(" (" + t + ")")
	}
	if o := status.ObservedPhrase(a); o != "" && a.Registered {
		row += styles.MutedText.Render(" · " + o)
	}
	if r := status.ResolvePhrase(a); r != "" {
		row += "\n" + strings.Repeat(" ", 15) + styles.WarningText.Render(r)
	}
	return row
}

func armingLabel(a status.ScopeArming) string {
	switch {
	case a.Effective && a.Timeout > 0:
		return styles.SuccessText.Render(fmt.Sprintf("armed, timeout %ds", a.Timeout))
	case a.Effective:
		return styles.SuccessText.Render("armed")
	case a.Registered:
		return styles.WarningText.Render("registered but inert")
	default:
		return styles.MutedText.Render("not armed")
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
		parts = append(parts, fmt.Sprintf("%s %d", c.Corpus, c.Size))
	}
	msg := fmt.Sprintf("%d of %d present — %s", present, len(s.Corpora), strings.Join(parts, ", "))
	if present == 0 {
		return styles.MutedText.Render(msg)
	}
	return msg
}

// renderAttention groups the queue by the action each row asks of the reader,
// rather than by the verdict token, and renders those groups in ActionOrder.
func renderAttention(s *status.SystemStatus) {
	fmt.Println(styles.SectionHeader.Render("Attention Required"))
	a := s.Attention
	rate := s.Rate
	buckets := map[string][]string{}

	for _, iv := range a.Interventions {
		un := status.Unmeasurable(iv.Verdict, iv.Unit, iv.RequiredAfter, rate)
		act := status.ActionFor(iv.Verdict, un)
		buckets[act] = append(buckets[act], interventionBlock(iv, un, rate))
	}
	if s.Posture.Unresolved > 0 {
		buckets[status.ActionAdjudicate] = append(buckets[status.ActionAdjudicate],
			fmt.Sprintf("    %d review(s) still owe a decision from the coding agent\n      %s",
				s.Posture.Unresolved,
				styles.MutedText.Render(status.BackfillCommand)))
	}
	for _, c := range a.Candidates {
		buckets[status.ActionAdjudicate] = append(buckets[status.ActionAdjudicate],
			"    "+candidateLine(c))
	}

	empty := true
	for _, act := range status.ActionOrder {
		if len(buckets[act]) > 0 {
			empty = false
		}
	}
	if empty && len(a.Unscored) == 0 {
		fmt.Println(styles.SuccessText.Render("  nothing pending — the loop is quiet"))
		return
	}
	for _, act := range status.ActionOrder {
		rows := buckets[act]
		if len(rows) == 0 {
			continue
		}
		fmt.Printf("  %s\n", actionHeader(act))
		for _, r := range rows {
			fmt.Println(r)
		}
	}
	if len(a.Unscored) > 0 {
		fmt.Printf("  %s: %d record(s) from evidence no engine reads\n",
			styles.MutedText.Render("unscored"), len(a.Unscored))
	}
}

// actionHeader colors a section by whether the reader is losing ground.
func actionHeader(act string) string {
	head := status.ActionHeading[act]
	switch act {
	case status.ActionHarmful:
		return styles.WarningText.Render(head)
	case status.ActionAdjudicate, status.ActionRepair:
		return styles.InfoText.Render(head)
	}
	return styles.MutedText.Render(head)
}

// interventionBlock is one class: its identity line, then the sub-lines naming
// what recurred, what to do, and where the rule lives.
func interventionBlock(iv cadence.Intervention, unmeasurable bool, rate status.ChainRate) string {
	line := fmt.Sprintf("    %-52s %s",
		fmt.Sprintf("%s [%s]", iv.Class, status.TierPhrase(iv.Tier, iv.Target)),
		styles.MutedText.Render(status.VerdictLabel(iv.Verdict, unmeasurable)))
	for _, extra := range interventionNotes(iv, unmeasurable, rate) {
		line += "\n      " + styles.MutedText.Render(extra)
	}
	return line
}

// interventionNotes are the sub-lines under a queued class. The range prints only
// where the scorer made a claim; everywhere else the counts carry the denominator
// in words. Coverage prints only when a window holds units no ledger decided.
func interventionNotes(iv cadence.Intervention, unmeasurable bool, rate status.ChainRate) []string {
	unit := iv.Unit
	if unit == "" {
		unit = "unit"
	}
	notes := status.ProsePhrases(iv.Problem, iv.Intervention, iv.Tier == "" || iv.Tier == "none")
	notes = append(notes, status.RecurrencePhrase(iv.Before, iv.After, unit))
	if e := status.EffectClaimPhrase(iv.Verdict, iv.Effect); e != "" {
		notes = append(notes, e)
	}
	gap := status.EvidenceGap(iv.Verdict, unit, iv.BeforeCoverage, iv.AfterCoverage, iv.Attribution)
	if s := status.GapSentence(gap, iv.Verdict, unmeasurable); s != "" {
		notes = append(notes, s)
	}
	if gap != nil && gap.Command != "" {
		notes = append(notes, gap.Command)
	}
	if r := status.RatePhrase(iv.RequiredAfter, unit, rate); r != "" {
		notes = append(notes, r)
	} else if r := status.RequiredPhrase(iv.RequiredAfter, iv.Verdict); r != "" {
		notes = append(notes, r)
	}
	if a := status.AttributionPhrase(iv.Attribution); a != "" {
		notes = append(notes, a)
	}
	notes = append(notes, status.ConfoundPhrases("confound", iv.Confounds)...)
	notes = append(notes, status.ConfoundPhrases("composition", iv.Composition)...)
	if iv.BeforeCoverage.UnknownEvidence > 0 || iv.AfterCoverage.UnknownEvidence > 0 {
		notes = append(notes, fmt.Sprintf("%d of the %d before-%s and %d of the %d after-%s carry no ledger row",
			iv.BeforeCoverage.UnknownEvidence, iv.BeforeCoverage.Total, plural(unit),
			iv.AfterCoverage.UnknownEvidence, iv.AfterCoverage.Total, plural(unit)))
	}
	if unmeasurable || iv.Verdict == "harmful" {
		notes = append(notes, status.RemovalPhrase(iv.Tier, iv.Target),
			status.RetireCommand(iv.ID))
	}
	return notes
}

func plural(unit string) string { return unit + "s" }

func candidateLine(c cadence.Candidate) string {
	if c.Pattern != "" {
		return fmt.Sprintf("%s — %s (%d hits, %d sessions)", c.Corpus, c.Pattern, c.Count, c.Sessions)
	}
	if c.UndecidedChains > 0 {
		return fmt.Sprintf("%s — %d review sequence(s) carry a finding no problem covers yet: %s", c.Corpus, c.UndecidedChains, c.Dump)
	}
	return c.Corpus
}

func renderSummary(s *status.SystemStatus) {
	fmt.Println(styles.SectionHeader.Render("Recurring Problems"))
	if s.Summary.Total == 0 {
		fmt.Println(styles.MutedText.Render("  no fixes recorded on this machine"))
		return
	}
	fmt.Printf("  %d recorded problem(s)\n", s.Summary.Total)
	verdicts := make([]string, 0, len(s.Summary.Counts))
	for v := range s.Summary.Counts {
		verdicts = append(verdicts, v)
	}
	sort.Strings(verdicts)
	for _, v := range verdicts {
		fmt.Printf("    %-30s %d\n", status.VerdictLabel(v, false), s.Summary.Counts[v])
	}
}

func init() {
	rootCmd.AddCommand(statusCmd)
	statusCmd.Flags().BoolVar(&statusJSON, "json", false, "Emit the versioned SystemStatus JSON on stdout")
	statusCmd.Flags().BoolVar(&statusHTML, "html", false, "Render the snapshot as one self-contained HTML file")
	statusCmd.Flags().StringVar(&statusOut, "out", "", "Destination file for --html (default: stdout)")
	statusCmd.Flags().IntVar(&statusDays, "days", 30, "Transcript look-back window in days for the attention refresh")
	statusCmd.Flags().BoolVar(&statusProjectOnly, "project-only", false, "Restrict the transcript scan to the current working directory")
}
