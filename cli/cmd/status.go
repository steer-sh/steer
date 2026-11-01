package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

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
	Short: "Say which Steer you run and whether it is healthy; --json gives an agent the whole loop",
	Long: `status prints the version you run and one answer: healthy, needs attention, or
could not check everything. Each issue names what clears it. Healthy covers the
installation and its configuration: commit review, guards, instructions, repository
checks and the installed builds. It makes no network request, so it says nothing about
newer releases. A person reads the rest of the loop — fixes, recorded problems,
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
			ReviewerProbe:   reviewerProbe(cfg, resolveErr),
			Addendum:        gateAddendum(root),
			PreCommit:       statusPreCommit(root),
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
		renderStatus(s, s.ReviewerProbe)
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

// statusPreCommit reports the git pre-commit channel armed in this repository.
func statusPreCommit(root string) bool {
	if root == "" {
		return false
	}
	armed, path := preCommitArmedHere()
	if !armed || !executableFile(path) {
		return false
	}
	// The hook exits cleanly with no review when steer does not resolve.
	_, err := exec.LookPath("steer")
	return err == nil
}

// executableFile reports a file git can run; git skips a hook without the bit.
func executableFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode()&0o111 != 0
}

// renderStatus prints the verdict, then each issue with what clears it. A fix prints
// whole on one line, because a wrap inserted into a command breaks the copy.
func renderStatus(s *status.SystemStatus, probe reviewers.Probe) {
	fmt.Print(renderHealth(status.HealthOf(s, probe), styles.Width()))
}

func renderHealth(h status.Health, width int) string {
	head := styles.WarningText.Render(h.Headline)
	if h.Verdict == status.VerdictHealthy {
		head = styles.SuccessText.Render(h.Headline)
	}
	var b strings.Builder
	b.WriteString("\n" + head + "\n")
	for _, line := range h.Ready {
		b.WriteString(styles.Wrap(line, "  ", width))
	}
	for _, issue := range h.Issues {
		b.WriteString("\n" + styles.Wrap(styles.Bold.Render(issue.Title), "", width))
		for _, line := range issue.Detail {
			b.WriteString(styles.Wrap(line, "  ", width))
		}
		if issue.Fix != "" {
			b.WriteString("  " + issue.Fix + "\n")
		}
	}
	if h.Note != "" {
		b.WriteString(styles.Wrap(styles.MutedText.Render(h.Note), "  ", width))
	}
	b.WriteString("\n" + styles.MutedText.Render("Details: steer dashboard") + "\n")
	return b.String()
}

func init() {
	rootCmd.AddCommand(statusCmd)
	statusCmd.Flags().BoolVar(&statusJSON, "json", false, "Emit the versioned status contract as JSON on stdout")
	statusCmd.Flags().IntVar(&statusDays, "days", 30, "Transcript look-back window in days for the attention refresh")
	statusCmd.Flags().BoolVar(&statusProjectOnly, "project-only", false, "Restrict the transcript scan to the current working directory")
	statusCmd.Flags().BoolVar(&statusRepoOnly, "repo-only", false, "Restrict the transcript scan to the current checkout")
}
