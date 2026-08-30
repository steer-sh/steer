package cmd

import (
	"bytes"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steer-sh/steer-cli/internal/cadence"
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
	Short: "Show the steer feedback loop's state — posture, attention, verdicts",
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
		s, err := status.Collect(status.Options{
			GateStateDir: gateConfig().StateDir,
			Config:       gateConfig(),
			WindowDays:   statusDays,
			AllProjects:  !statusProjectOnly,
			Scope:        currentScope(),
		})
		if err != nil {
			return err
		}
		if statusJSON && statusHTML {
			return fmt.Errorf("--json and --html are separate render targets; pass one")
		}
		if statusHTML {
			return writeStatusHTML(s)
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

// writeStatusHTML renders the snapshot to --out, or to stdout when --out is
// absent. It buffers first, so a render error leaves no half-written file.
func writeStatusHTML(s *status.SystemStatus) error {
	var buf bytes.Buffer
	if err := s.RenderHTML(&buf); err != nil {
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
	for _, a := range s.Posture.Resolver {
		fmt.Printf("  Resolver     %s scope: %s\n", a.Scope, armingLabel(a))
	}
	if s.Posture.Unresolved > 0 {
		fmt.Printf("  Unresolved   %s\n", styles.WarningText.Render(
			fmt.Sprintf("%d row(s) owe a ledger — run: steer commit-gate backfill --apply", s.Posture.Unresolved)))
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
		line := fmt.Sprintf("    %s [%s → %s]  before %d/%d, after %d/%d   %s",
			iv.Class, iv.Tier, iv.Target,
			iv.Before.Hits, iv.Before.Units, iv.After.Hits, iv.After.Units,
			status.EffectPhrase(iv.Effect))
		for _, extra := range interventionNotes(iv) {
			line += "\n      " + styles.MutedText.Render(extra)
		}
		byVerdict[iv.Verdict] = append(byVerdict[iv.Verdict], line)
	}
	for _, v := range cadence.AttentionVerdicts {
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

// interventionNotes are the sub-lines under a queued class: its scope, its window
// accounting, and any confound or composition shift.
func interventionNotes(iv cadence.Intervention) []string {
	notes := []string{
		"scope: " + status.ScopePhrase(iv.Scope),
		"coverage: before " + status.CoveragePhrase(iv.BeforeCoverage) +
			", after " + status.CoveragePhrase(iv.AfterCoverage),
	}
	if r := status.RequiredPhrase(iv.RequiredAfter, iv.Verdict); r != "" {
		notes = append(notes, r)
	}
	notes = append(notes, status.ConfoundPhrases("confound", iv.Confounds)...)
	notes = append(notes, status.ConfoundPhrases("composition", iv.Composition)...)
	return notes
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
	statusCmd.Flags().BoolVar(&statusHTML, "html", false, "Render the snapshot as one self-contained HTML file")
	statusCmd.Flags().StringVar(&statusOut, "out", "", "Destination file for --html (default: stdout)")
	statusCmd.Flags().IntVar(&statusDays, "days", 30, "Transcript look-back window in days for the attention refresh")
	statusCmd.Flags().BoolVar(&statusProjectOnly, "project-only", false, "Restrict the transcript scan to the current working directory")
}
