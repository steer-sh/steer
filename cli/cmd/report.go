package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/steer-sh/steer-cli/internal/cadence"
	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/evidence"
	"github.com/steer-sh/steer-cli/internal/harness"
	"github.com/steer-sh/steer-cli/internal/report"
	"github.com/steer-sh/steer-cli/internal/status"
	"github.com/steer-sh/steer-cli/internal/transcript"
)

var (
	reportDays        int
	reportProjectOnly bool
	reportMaxBytes    int
	reportHarness     string
)

var reportCmd = &cobra.Command{
	Use:   "report",
	Short: "Scan agent transcripts for hook-suggestion signals",
	Long: `Report streams session transcripts from the last N days and emits a compact
JSON digest of recurring friction: user corrections, rejected tool calls,
lint/test failures, and hook blocks. It also diffs instruction-file directives
(CLAUDE.md, AGENTS.md) against already-registered steer rules.

Every harness with transcripts on this machine is mined — Claude Code
(~/.claude/projects) and Codex ($CODEX_HOME/sessions). Use --harness to read one
of them alone. A subagent thread is provenance on its root session, never a unit
of its own, so the two harnesses' sessions count the same way.

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
  steer report --harness codex
  steer report --project-only
  steer report --max-digest-bytes 20000`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if reportDays <= 0 {
			return fmt.Errorf("--days must be positive, got %d", reportDays)
		}
		only, err := parseReportHarness()
		if err != nil {
			return err
		}

		projectPaths, err := report.ProjectPaths()
		if err != nil {
			return fmt.Errorf("failed to resolve working directory: %w", err)
		}

		since := time.Now().AddDate(0, 0, -reportDays)
		filter := transcript.Filter{
			Since: since, Projects: projectPaths, AllProjects: !reportProjectOnly,
		}
		adapters := report.Adapters(only)

		scanner, projectNames := report.Scan(adapters, since, filter, func(s transcript.Session, err error) {
			fmt.Fprintf(os.Stderr, "warning: skipping %s: %v\n", filepath.Base(s.Path), err)
		})

		digest := scanner.Finalize(reportDays, projectNames, collectInstructionFiles(only))
		data, err := digest.Marshal(reportMaxBytes)
		if err != nil {
			return fmt.Errorf("failed to marshal digest: %w", err)
		}

		for _, cov := range digest.Stats.Harnesses {
			fmt.Fprintf(os.Stderr, "%s: %d file(s), %d session(s), %d thread(s).\n",
				cov.Harness, cov.FilesScanned, cov.SessionsScanned, cov.ThreadsScanned)
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

// parseReportHarness resolves --harness for a read. An empty flag means every
// harness: a read aggregates, and only a mutation needs one named target.
func parseReportHarness() (harness.ID, error) {
	if strings.TrimSpace(reportHarness) == "" || strings.EqualFold(reportHarness, "all") {
		return harness.Unknown, nil
	}
	return harness.Parse(reportHarness)
}

// collectInstructionFiles reads each harness's project and global instruction
// document. Both scopes are always included: standing user rules — the
// highest-precision hook candidates — live in either. Read failures degrade to
// an empty section, never abort the scan.
func collectInstructionFiles(only harness.ID) []report.InstructionFile {
	out := []report.InstructionFile{}
	cwd, err := os.Getwd()
	if err != nil {
		return out
	}
	for _, h := range harness.All() {
		if only != harness.Unknown && h.ID != only {
			continue
		}
		for _, scope := range []string{harness.ProjectScope, harness.GlobalScope} {
			path := h.InstructionFile
			if scope == harness.GlobalScope {
				dir, err := h.ConfigDir(scope, cwd)
				if err != nil {
					continue
				}
				path = filepath.Join(dir, h.InstructionFile)
			}
			md, err := report.ExtractInstructionFile(h.ID, path)
			if err != nil {
				fmt.Fprintf(os.Stderr, "warning: could not read %s: %v\n", path, err)
				continue
			}
			if len(md.SteerRules) == 0 && len(md.Directives) == 0 {
				continue
			}
			out = append(out, md)
		}
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
work to ~/.steer/state/steer-report/ for the /steer-report skill to finish.

The queue content is deterministic: a second run over the same evidence produces
a byte-identical queue. The refresh timestamp lives in a sibling file, not the
queue, so the skill's nudge can hash the queue for change detection.`,
	Example: `  steer report refresh
  steer report refresh --days 7 --project-only`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := prepareState(); err != nil {
			return err
		}
		// refresh is the one writer of the snapshot store: it rebuilds the session
		// corpus from the harnesses' transcripts before scoring over it.
		ev := evidence.Gather(evidence.Options{
			GateStateDir: gateConfig().StateDir,
			WindowDays:   reportDays,
			AllProjects:  !reportProjectOnly,
			Rebuild:      true,
		})
		q, err := cadence.Refresh(cadence.Options{
			GateStateDir: gateConfig().StateDir,
			WindowDays:   reportDays,
			AllProjects:  !reportProjectOnly,
			Evidence:     ev,
		})
		if err != nil {
			return err
		}
		dir := cadence.StateDir()
		if err := cadence.WriteQueue(dir, q, time.Now()); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr,
			"Wrote queue: %d intervention(s) needing attention, %d candidate(s), %d unscored, "+
				"%d session snapshot(s).\n",
			len(q.Interventions), len(q.Candidates), len(q.Unscored), len(ev.Sessions))
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

	rec := cadence.LoadNudge(dir)
	lines := []string{}

	if queueBytes, err := os.ReadFile(cadence.QueuePath(dir)); err == nil {
		if q, err := cadence.ReadQueue(dir); err == nil {
			hash := cadence.QueueHash(queueBytes)
			emit, next := cadence.ShouldNudge(dir, q, hash, cadence.LastRefresh(dir))
			if emit {
				candidates, ineffective := cadence.PendingCounts(q)
				lines = append(lines, fmt.Sprintf(
					"steer: %d candidate(s) and %d ineffective hook(s) pending — run /steer-report",
					candidates, ineffective))
				rec = next
			}
		}
	}

	// A stale guard master is a standing condition, not a queue change: the store
	// keeps whatever runtime it was last built with, and upgrading the CLI
	// repairs nothing until something rebuilds it.
	key, labels, scope := staleMasters()
	if cadence.ShouldNudgeMaster(dir, key) {
		lines = append(lines, fmt.Sprintf(
			"steer: the guard master runs an older runtime (%s) — run: steer hooks rebuild --scope %s",
			strings.Join(labels, ", "), scope))
	}
	if len(lines) == 0 {
		// Nothing to say, but a repaired master still has to clear its key: a
		// record left behind would suppress the nudge if that same staleness
		// came back.
		if cadence.LoadNudge(dir).Master != key {
			_ = cadence.WriteNudge(dir, rec.WithMaster(key))
		}
		return
	}
	rec = rec.WithMaster(key)

	msg := strings.Join(lines, "\n")
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

// staleMasters names the scopes whose registered guard master carries a runtime
// other than this binary's, and a key that changes with them. An empty key means
// nothing is stale. Every read fails soft: an unreadable configuration reports no
// master rather than a false alarm.
func staleMasters() (key string, labels []string, scope string) {
	parts := []string{}
	for _, m := range status.MasterRuntimes(claude.GlobalScope, claude.ProjectScope) {
		if !m.Stale() {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s/%s:%s:%s", m.Harness, m.Scope, m.State, m.Runtime))
		labels = append(labels, fmt.Sprintf("%s %s scope", m.Harness, m.Scope))
		if scope == "" {
			scope = m.Scope
		}
	}
	if len(parts) == 0 {
		return "", nil, ""
	}
	sort.Strings(parts)
	return strings.Join(parts, "|"), labels, scope
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
	reportCmd.Flags().StringVar(&reportHarness, "harness", "", "Mine one harness only: all (default), claude-code, or codex")
	reportCmd.Flags().IntVar(&reportMaxBytes, "max-digest-bytes", report.DefaultMaxDigestBytes, "Hard cap on digest size; lowest-scoring clusters are evicted to fit")
}
