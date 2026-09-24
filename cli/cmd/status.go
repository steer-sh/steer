package cmd

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/spf13/cobra"
	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/evidence"
	"github.com/steer-sh/steer-cli/internal/gate"
	"github.com/steer-sh/steer-cli/internal/gate/reviewers"
	"github.com/steer-sh/steer-cli/internal/status"
	"github.com/steer-sh/steer-cli/internal/styles"
)

var (
	statusJSON        bool
	statusDays        int
	statusProjectOnly bool
	statusRepoOnly    bool
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Say whether commits here are reviewed; --json gives an agent the whole loop",
	Long: `status answers whether a commit here will be reviewed, names each step that
stands in the way with the command that clears it, and reports a guard list Steer
cannot read. A person reads the rest of the loop — fixes, recorded problems,
arming — in 'steer dashboard'. It writes no state.

--json prints the versioned status contract on stdout, progress on stderr — the
same discipline as 'steer report'. That contract keeps the machine's own words: one
summary row per fix, and guards and built-in guards from both scopes. An agent runs
'steer status --json' before installing a hook, to read the full inventory and skip
a problem already decided; 'steer fix score --json' carries each fix's detail.`,
	Example: `  steer status
  steer status --json
  steer status --days 7 --project-only
  steer status --repo-only`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// status never writes: it reads the stored snapshots rather than rebuilding
		// them, so the one writer stays `steer report refresh`.
		// A snapshot renders under an unresolvable provider: it names no provider,
		// so the configured model is the honest value to print.
		scope, err := scanScope(statusRepoOnly, statusProjectOnly)
		if err != nil {
			return err
		}
		cfg, _, resolveErr := resolvedGateConfig()
		root := repoRootOrNone()
		s, err := status.Collect(status.Options{
			GateStateDir: cfg.StateDir,
			Config:       cfg,
			WindowDays:   statusDays,
			Scope:        currentScope(),
			// The command lists every scope's guards; only the TUI, which mutates
			// the scope it lists, reads one.
			InventoryScopes: []string{claude.ProjectScope, claude.GlobalScope},
			ScanScope:       scope,
			RepoRoot:        root,
			Addendum:        gateAddendum(root),
			Evidence: evidence.Gather(evidence.Options{
				GateStateDir: cfg.StateDir,
				ScanScope:    scope,
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

// repoRootOrNone resolves the repository whose check tier a read reports, "" when
// the working directory is outside one. Steer runs everywhere; a repository is
// the unit a check tier exists in.
// gateAddendum is the review addendum the commit gate reads in root.
func gateAddendum(root string) *gate.Addendum {
	a := gate.LoadAddendum(root)
	return &a
}

func repoRootOrNone() string {
	root, err := repoRootOf(".")
	if err != nil {
		return ""
	}
	return root
}

// renderStatus prints the one answer a reader without the dashboard needs: will a
// commit here be reviewed, and what stands in the way. The rest of the loop is the
// dashboard's, for a person, and --json's, for an agent.
func renderStatus(s *status.SystemStatus, probe reviewers.Probe) {
	width := styles.Width()
	fmt.Println()
	fmt.Println(styles.MainHeader.Render(styles.Logo() + " status"))
	fmt.Println()

	renderReadiness(status.ReadinessOf(s, probe), width)
	fmt.Println()
	renderStoreProblems(s, width)
	fmt.Print(styles.Wrap(styles.MutedText.Render("Fixes, recorded problems and arming: steer dashboard"), "  ", width))
	fmt.Print(styles.Wrap(styles.MutedText.Render("An agent reads the whole loop: steer status --json"), "  ", width))
}

// renderStoreProblems prints the stores Steer refused to read. Nothing else in
// this report would say so: every inventory below fails soft, so a refused store
// reads exactly like a store holding nothing while its guards still run.
func renderStoreProblems(s *status.SystemStatus, width int) {
	if len(s.StoreProblems) == 0 {
		return
	}
	fmt.Println(styles.SectionHeader.Render("Guard files"))
	for _, p := range s.StoreProblems {
		fmt.Print(problemEntry(
			styles.WarningText.Render("Steer cannot read the "+p.Scope+" guard list"),
			[]problemField{
				{"Why", []string{p.Error}},
				{"Next", []string{"edit the entry named above; the guards already installed keep running"}},
			}, width))
	}
	fmt.Println()
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

// problemIndent is the column a problem's title sits in, and problemFieldIndent
// the one its stacked labels sit in under it. They are the dashboard's own two
// columns, so one entry reads the same in either place.
const (
	problemIndent      = "    "
	problemFieldIndent = "      "
	problemLabelWidth  = 9
)

// problemField is one labeled line of an entry, carrying every sentence that
// files under that label.
type problemField struct {
	label  string
	values []string
}

// problemEntry renders one entry: its title, then its stacked labels. A field
// with nothing to say prints no label.
func problemEntry(title string, fields []problemField, width int) string {
	name := []rune(title)
	for i, r := range name {
		if unicode.IsLetter(r) {
			name[i] = unicode.ToUpper(r)
			break
		}
	}
	out := "\n" + styles.Wrap(styles.BrandText.Render(string(name)), problemIndent, width)
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

func init() {
	rootCmd.AddCommand(statusCmd)
	statusCmd.Flags().BoolVar(&statusJSON, "json", false, "Emit the versioned status contract as JSON on stdout")
	statusCmd.Flags().IntVar(&statusDays, "days", 30, "Transcript look-back window in days for the attention refresh")
	statusCmd.Flags().BoolVar(&statusProjectOnly, "project-only", false, "Restrict the transcript scan to the current working directory")
	statusCmd.Flags().BoolVar(&statusRepoOnly, "repo-only", false, "Restrict the transcript scan to the current checkout")
}
