package cmd

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steer-sh/steer-cli/internal/cadence"
	"github.com/steer-sh/steer-cli/internal/status"
	"github.com/steer-sh/steer-cli/internal/styles"
)

var (
	statusJSON        bool
	statusDays        int
	statusProjectOnly bool
)

// attentionVerdictOrder groups the attention block the way an operator reads it:
// broken interventions first, then a class a refusal let grow.
var attentionVerdictOrder = []string{"ineffective", "class-grew"}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the steer feedback loop's state — posture, attention, verdicts",
	Long: `status composes one read-only snapshot of the steer loop and renders it. The
unit is the failure class and its intervention's measured verdict, not the hook
count. It reads the scored intervention inventory, the recent review chains, the
attention subset the deterministic tier flags, the arming posture, and the corpus
sizes. It writes no state.

--json prints the versioned SystemStatus contract on stdout, progress on stderr —
the same discipline as 'steer report'. An agent runs 'steer status --json' before
installing a hook to see the full class inventory and skip a class already scored.`,
	Example: `  steer status
  steer status --json
  steer status --days 7 --project-only`,
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := status.Collect(status.Options{
			GateStateDir: gateConfig().StateDir,
			Config:       gateConfig(),
			WindowDays:   statusDays,
			AllProjects:  !statusProjectOnly,
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
		renderStatus(s)
		return nil
	},
}

// renderStatus prints the loop-first human view: posture, then the attention
// queue, then the failure-class verdict counts.
func renderStatus(s *status.SystemStatus) {
	fmt.Println()
	fmt.Println(styles.MainHeader.Render(styles.Logo() + " status"))
	fmt.Println()

	renderPosture(s)
	fmt.Println()
	renderAttention(s)
	fmt.Println()
	renderSummary(s)
}

func renderPosture(s *status.SystemStatus) {
	fmt.Println(styles.SectionHeader.Render("Posture"))
	fmt.Printf("  Instrument   %s\n", s.Posture.Instrument)
	fmt.Printf("  Reviewer     %s\n", s.Posture.Reviewer)
	fmt.Printf("  Ledger       %s\n", s.Posture.Ledger)
	for _, a := range s.Posture.CommitGate {
		fmt.Printf("  Commit gate  %s scope: %s\n", a.Scope, armingLabel(a))
	}
	for _, a := range s.Posture.Denials {
		fmt.Printf("  Denials      %s scope: %s\n", a.Scope, armingLabel(a))
	}
	fmt.Printf("  Corpora      %s\n", corporaLine(s))
	fmt.Printf("  Audit log    %d block firing(s)\n", s.Telemetry.AuditFirings)
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

func renderAttention(s *status.SystemStatus) {
	fmt.Println(styles.SectionHeader.Render("Attention Required"))
	a := s.Attention
	if len(a.Interventions) == 0 && len(a.Candidates) == 0 && len(a.Unscored) == 0 {
		fmt.Println(styles.SuccessText.Render("  nothing pending — the loop is quiet"))
		return
	}
	byVerdict := map[string][]string{}
	for _, iv := range a.Interventions {
		line := fmt.Sprintf("    %s [%s → %s]  before %d/%d, after %d/%d",
			iv.Class, iv.Tier, iv.Target,
			iv.Before.Hits, iv.Before.Units, iv.After.Hits, iv.After.Units)
		byVerdict[iv.Verdict] = append(byVerdict[iv.Verdict], line)
	}
	for _, v := range attentionVerdictOrder {
		lines := byVerdict[v]
		if len(lines) == 0 {
			continue
		}
		fmt.Printf("  %s (%d)\n", styles.WarningText.Render(v), len(lines))
		for _, l := range lines {
			fmt.Println(l)
		}
	}
	if len(a.Candidates) > 0 {
		fmt.Printf("  %s (%d)\n", styles.InfoText.Render("candidates pending routing"), len(a.Candidates))
		for _, c := range a.Candidates {
			fmt.Printf("    %s\n", candidateLine(c))
		}
	}
	if len(a.Unscored) > 0 {
		fmt.Printf("  %s: %d session-unit intervention(s) the engine cannot score\n",
			styles.MutedText.Render("unscored"), len(a.Unscored))
	}
}

func candidateLine(c cadence.Candidate) string {
	if c.Pattern != "" {
		return fmt.Sprintf("%s — %s (%d hits, %d sessions)", c.Corpus, c.Pattern, c.Count, c.Sessions)
	}
	if c.UndecidedChains > 0 {
		return fmt.Sprintf("%s — %d undecided chain(s): %s", c.Corpus, c.UndecidedChains, c.Dump)
	}
	return c.Corpus
}

func renderSummary(s *status.SystemStatus) {
	fmt.Println(styles.SectionHeader.Render("Failure Classes"))
	if s.Summary.Total == 0 {
		fmt.Println(styles.MutedText.Render("  no interventions recorded on this machine"))
		return
	}
	fmt.Printf("  %d live intervention(s)\n", s.Summary.Total)
	verdicts := make([]string, 0, len(s.Summary.Counts))
	for v := range s.Summary.Counts {
		verdicts = append(verdicts, v)
	}
	sort.Strings(verdicts)
	for _, v := range verdicts {
		fmt.Printf("    %-18s %d\n", v, s.Summary.Counts[v])
	}
}

func init() {
	rootCmd.AddCommand(statusCmd)
	statusCmd.Flags().BoolVar(&statusJSON, "json", false, "Emit the versioned SystemStatus JSON on stdout")
	statusCmd.Flags().IntVar(&statusDays, "days", 30, "Transcript look-back window in days for the attention refresh")
	statusCmd.Flags().BoolVar(&statusProjectOnly, "project-only", false, "Restrict the transcript scan to the current working directory")
}
