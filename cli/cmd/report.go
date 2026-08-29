package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"github.com/steer-sh/steer-cli/internal/cadence"
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

var reportRefreshCmd = &cobra.Command{
	Use:   "refresh",
	Short: "Run the deterministic tier of the feedback loop and write the work queue",
	Long: `refresh is the deterministic tier of the steer feedback loop. It spends no
model tokens and runs in seconds. It scores the review-corpus interventions the
local engine can score, sweeps every corpus present on this machine, applies the
thresholds each tier can compute without a model, and writes a queue of pending
work to ~/.claude/state/steer-report/ for the /steer-report skill to finish.

The queue content is deterministic: a second run over the same evidence produces
a byte-identical queue. The refresh timestamp lives in a sibling file, not the
queue, so the skill's nudge can hash the queue for change detection.`,
	Example: `  steer report refresh
  steer report refresh --days 7 --project-only`,
	RunE: func(cmd *cobra.Command, args []string) error {
		q, err := cadence.Refresh(cadence.Options{
			GateStateDir: gateConfig().StateDir,
			WindowDays:   reportDays,
			AllProjects:  !reportProjectOnly,
		})
		if err != nil {
			return err
		}
		dir := cadence.StateDir()
		if err := cadence.WriteQueue(dir, q, time.Now()); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr,
			"Wrote queue: %d intervention(s) needing attention, %d candidate(s), %d unscored.\n",
			len(q.Interventions), len(q.Candidates), len(q.Unscored))
		fmt.Println(cadence.QueuePath(dir))
		return nil
	},
}

// sessionStartOutput is the SessionStart hook's stdout contract. The docs do not
// confirm SessionStart shows systemMessage ("some events discard it"), so the nudge
// rides hookSpecificOutput.additionalContext — the documented SessionStart channel,
// guaranteed to deliver. That puts one actionable line in the model's context, the
// plan's accepted fallback. systemMessage is kept as a bonus operator line where the
// harness honors it; it costs nothing where it does not.
type sessionStartOutput struct {
	SystemMessage      string                 `json:"systemMessage,omitempty"`
	HookSpecificOutput sessionStartHookOutput `json:"hookSpecificOutput"`
}

type sessionStartHookOutput struct {
	HookEventName     string `json:"hookEventName"`
	AdditionalContext string `json:"additionalContext"`
}

var reportCadenceCmd = &cobra.Command{
	Use:   "cadence",
	Short: "SessionStart entrypoint: refresh the queue in the background, nudge if work is pending",
	Long: `cadence is the plugin-native SessionStart entrypoint of the feedback loop. It
must never block startup and fails open on every error — a cadence check that
cannot read state emits nothing and exits 0.

It does two things:

  1. If the last refresh is older than 7 days, it spawns 'steer report refresh'
     detached in the background. This session proceeds; the next session sees the
     result.
  2. If the queue holds pending work, it emits one line naming the counts and the
     command to run. The line rides hookSpecificOutput.additionalContext, the
     documented SessionStart channel, so delivery is guaranteed; it also sets
     systemMessage for an operator line where the harness honors it. It re-emits
     only when the queue changes or a new refresh cycle has run — never every
     session.`,
	Example: `  steer report cadence`,
	// SilenceErrors/Usage: this runs as a hook. Any failure must be silent and
	// exit 0, never a stderr dump into the session's startup.
	SilenceErrors: true,
	SilenceUsage:  true,
	RunE: func(cmd *cobra.Command, args []string) error {
		runCadence()
		return nil
	},
}

// runCadence is the fail-open body of the cadence hook. Every step degrades to
// "emit nothing" on error; it never returns a non-nil error to the caller.
func runCadence() {
	dir := cadence.StateDir()

	if time.Since(cadence.LastRefresh(dir)) > cadence.StaleAfter {
		spawnRefresh()
	}

	queueBytes, err := os.ReadFile(cadence.QueuePath(dir))
	if err != nil {
		return // no queue yet, or unreadable — nothing to nudge about
	}
	q, err := cadence.ReadQueue(dir)
	if err != nil {
		return
	}
	refresh := cadence.LastRefresh(dir)
	hash := cadence.QueueHash(queueBytes)

	emit, rec := cadence.ShouldNudge(dir, q, hash, refresh)
	if !emit {
		return
	}

	candidates, ineffective := cadence.PendingCounts(q)
	msg := fmt.Sprintf("steer: %d candidate(s) and %d ineffective hook(s) pending — run /steer-report",
		candidates, ineffective)
	out, err := json.Marshal(sessionStartOutput{
		SystemMessage: msg,
		HookSpecificOutput: sessionStartHookOutput{
			HookEventName:     "SessionStart",
			AdditionalContext: msg,
		},
	})
	if err != nil {
		return
	}
	fmt.Println(string(out))
	// Persist only after a successful emit, so a failed print re-nudges next time.
	_ = cadence.WriteNudge(dir, rec)
}

// spawnRefresh starts 'steer report refresh' detached, so a stale queue refreshes
// without blocking startup. Failure is silent — the worst case is a queue one
// cycle older than 7 days, which the next session retries.
func spawnRefresh() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	c := exec.Command(exe, "report", "refresh")
	c.Stdin, c.Stdout, c.Stderr = nil, nil, nil
	c.SysProcAttr = detachAttr()
	_ = c.Start() // do not Wait — the refresh outlives this process
}

func init() {
	rootCmd.AddCommand(reportCmd)
	reportCmd.AddCommand(reportRefreshCmd)
	reportCmd.AddCommand(reportCadenceCmd)
	reportRefreshCmd.Flags().IntVar(&reportDays, "days", 30, "Transcript look-back window in days")
	reportRefreshCmd.Flags().BoolVar(&reportProjectOnly, "project-only", false, "Restrict the transcript scan to the current working directory")
	reportCmd.Flags().IntVar(&reportDays, "days", 30, "Look-back window in days")
	reportCmd.Flags().BoolVar(&reportProjectOnly, "project-only", false, "Restrict the scan to transcripts of the current working directory")
	reportCmd.Flags().IntVar(&reportMaxBytes, "max-digest-bytes", report.DefaultMaxDigestBytes, "Hard cap on digest size; lowest-scoring clusters are evicted to fit")
}
