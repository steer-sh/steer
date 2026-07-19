package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/report"
)

var (
	reportDays        int
	reportProjectOnly bool
	reportMaxBytes    int
)

var reportCmd = &cobra.Command{
	Use:   "report",
	Short: "Scan Claude Code transcripts for hook-suggestion signals",
	Long: `Report streams Claude Code session transcripts (~/.claude/projects) from the
last N days and emits a compact JSON digest of recurring friction: user
corrections, rejected tool calls, lint/test failures, and hook blocks. It also
diffs CLAUDE.md directives against already-registered steer rules.

All projects are scanned by default — the strongest hook candidates are habits
that repeat across projects. Use --project-only to restrict the scan to
transcripts of the current working directory.

The digest is mechanical evidence only — deciding which signals deserve hooks
is the job of the /steer-report skill, which reads this output. Stdout carries
pure JSON; progress goes to stderr. The digest is capped (--max-digest-bytes)
by evicting the lowest-scoring clusters, and reports its own truncation in
stats.truncated.`,
	Example: `  steer report
  steer report --days 7
  steer report --project-only
  steer report --max-digest-bytes 20000`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if reportDays <= 0 {
			return fmt.Errorf("--days must be positive, got %d", reportDays)
		}

		projectsDir, err := report.ProjectsDir()
		if err != nil {
			return err
		}

		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("failed to resolve working directory: %w", err)
		}
		// Sessions may be keyed under either the literal cwd or its
		// symlink-resolved path, depending on how they were launched.
		projectPaths := []string{cwd}
		if resolved, err := filepath.EvalSymlinks(cwd); err == nil && resolved != cwd {
			projectPaths = append(projectPaths, resolved)
		}

		since := time.Now().AddDate(0, 0, -reportDays)
		files, err := report.FindTranscripts(projectsDir, projectPaths, !reportProjectOnly, since)
		if err != nil {
			return err
		}

		fmt.Fprintf(os.Stderr, "Scanning %d transcript file(s) from the last %d day(s)...\n", len(files), reportDays)

		scanner := report.NewScanner(since)
		projects := map[string]struct{}{}
		for _, f := range files {
			if err := scanner.ScanFile(f); err != nil {
				fmt.Fprintf(os.Stderr, "warning: skipping %s: %v\n", filepath.Base(f.Path), err)
				continue
			}
			projects[f.ProjectDir] = struct{}{}
		}

		projectNames := make([]string, 0, len(projects))
		for name := range projects {
			projectNames = append(projectNames, name)
		}

		claudeMDs := collectClaudeMDs()

		digest := scanner.Finalize(reportDays, projectNames, claudeMDs)
		data, err := digest.Marshal(reportMaxBytes)
		if err != nil {
			return fmt.Errorf("failed to marshal digest: %w", err)
		}

		fmt.Fprintf(os.Stderr, "Scanned %d sessions (%d bytes); emitting %d of %d clusters (truncated=%v).\n",
			digest.Stats.SessionsScanned,
			digest.Stats.BytesScanned,
			digest.Stats.ClustersEmitted,
			digest.Stats.ClustersFound,
			digest.Stats.Truncated,
		)

		fmt.Println(string(data))
		return nil
	},
}

// collectClaudeMDs reads project and global CLAUDE.md files. Both are always
// included: standing user rules (the highest-precision hook candidates) live
// in either. Read failures degrade to an empty section, never abort the scan.
func collectClaudeMDs() []report.ClaudeMD {
	out := []report.ClaudeMD{}
	for _, scope := range []string{claude.ProjectScope, claude.GlobalScope} {
		path, err := claude.GetClaudeMDFile(scope)
		if err != nil {
			continue
		}
		md, err := report.ExtractClaudeMD(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not read %s: %v\n", path, err)
			continue
		}
		if len(md.SteerRules) == 0 && len(md.Directives) == 0 {
			continue
		}
		out = append(out, md)
	}
	return out
}

func init() {
	rootCmd.AddCommand(reportCmd)
	reportCmd.Flags().IntVar(&reportDays, "days", 30, "Look-back window in days")
	reportCmd.Flags().BoolVar(&reportProjectOnly, "project-only", false, "Restrict the scan to transcripts of the current working directory")
	reportCmd.Flags().IntVar(&reportMaxBytes, "max-digest-bytes", report.DefaultMaxDigestBytes, "Hard cap on digest size; lowest-scoring clusters are evicted to fit")
}
