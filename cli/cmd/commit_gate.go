package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steer-sh/steer-cli/internal/claude"
	codexhooks "github.com/steer-sh/steer-cli/internal/codex"
	"github.com/steer-sh/steer-cli/internal/gate"
	"github.com/steer-sh/steer-cli/internal/gate/cc"
	gatecodex "github.com/steer-sh/steer-cli/internal/gate/codex"
	"github.com/steer-sh/steer-cli/internal/gate/gitgate"
	"github.com/steer-sh/steer-cli/internal/gate/reviewers"
	"github.com/steer-sh/steer-cli/internal/harness"
	"github.com/steer-sh/steer-cli/internal/styles"
)

const (
	commitGatePreCommitMarker = "# steer-managed: commit-gate"
	// The PreToolUse timeout must EXCEED the per-reviewer wall clock, or a slow review
	// is killed mid-flight and the commit lands ungated with no message AND no row. A
	// value equal to the review timeout does exactly that, so the margin is not
	// decoration.
	commitGateTimeoutMargin = 60
	// The resolver reads transcripts and rewrites the shard; 30s clears the largest
	// local corpus with headroom.
	backfillHookTimeout = 30
)

// backfillCommand is the Stop hook steer registers alongside the Claude Code review
// hook: harvest late, only when a row is due, silently.
func backfillCommand() string {
	return "steer commit-gate backfill --if-due --apply --quiet"
}

var (
	commitGateProvider     string
	commitGateModel        string
	commitGateBothChannels bool
	commitGateForce        bool
	commitGateHarness      string
)

var commitGateCmd = &cobra.Command{
	Use:   "commit-gate",
	Short: "Red-team a commit's diff before it lands",
	Long: `commit-gate runs a panel of independent reviewers over a commit's diff and
blocks the commit while a finding stands, writing an adjudication row for every
decision.

Three channels share one review engine:
  review        PreToolUse hook for Claude Code — reads a JSON payload on stdin
  review-codex  PreToolUse hook for Codex — reads a JSON payload on stdin
  precommit  git pre-commit hook — reads the index

Arming both channels in one repository reviews every commit twice. Use
"install --both-channels" to say that is intended.`,
	Example: `  steer commit-gate install --scope global
  steer commit-gate status
  steer commit-gate precommit`,
}

// buildReviewer resolves the provider. An empty model means "the provider's own
// default", which is why the agy default is not handed to Codex or Claude Code — it is
// a Gemini pin and names nothing on either of them.
func buildReviewer(cfg gate.Config) (gate.Reviewer, error) {
	switch strings.ToLower(strings.TrimSpace(commitGateProvider)) {
	case "", "agy":
		// cfg.ReviewerModel already carries the flag and the environment override.
		return reviewers.NewAgy(cfg.ReviewerModel), nil
	case "codex":
		return reviewers.NewCodex(chosenModel()), nil
	case "claude", "claude-code":
		return reviewers.NewClaudeCode(chosenModel()), nil
	default:
		return nil, fmt.Errorf("unknown reviewer provider %q: expected agy, codex or claude-code", commitGateProvider)
	}
}

// chosenModel is the model the operator asked for, by flag or by environment, and
// empty when they asked for none. cfg.ReviewerModel cannot answer this: it falls back
// to the agy pin, which is a Gemini name and selects nothing on the other providers.
func chosenModel() string {
	if commitGateModel != "" {
		return commitGateModel
	}
	return strings.TrimSpace(os.Getenv("COMMIT_GATE_MODEL"))
}

func gateConfig() gate.Config {
	cfg := gate.Load()
	if commitGateModel != "" {
		cfg.ReviewerModel = commitGateModel
	}
	return cfg
}

var commitGateReviewCmd = &cobra.Command{
	Use:   "review",
	Short: "PreToolUse hook: rule on a Bash `git commit` before it runs",
	Long: `review reads a Claude Code PreToolUse payload on stdin and writes the hook's
reply on stdout. It always exits 0: a gate that cannot answer must fail open.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := gateConfig()
		reviewer, err := buildReviewer(cfg)
		if err != nil {
			// The fail-open contract covers the gate's own misconfiguration. A
			// non-zero exit here reports a hook error on every Bash tool call.
			fmt.Fprintln(os.Stderr, "steer commit-gate:", err)
			return nil
		}
		cc.Run(context.Background(), cfg, reviewer, os.Stdin, os.Stdout)
		return nil
	},
}

var commitGatePreCommitCmd = &cobra.Command{
	Use:   "precommit",
	Short: "git pre-commit hook: rule on the staged tree",
	Long: `precommit reviews the staged tree and exits 2 with the reviews on stderr when
a finding stands. Any other failure exits 0, because a gate that cannot answer must
fail open.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := gateConfig()
		reviewer, err := buildReviewer(cfg)
		if err != nil {
			// Any non-zero exit from this hook aborts the change being made, so
			// returning the error blocks work the reviewers never saw.
			fmt.Fprintln(os.Stderr, "steer commit-gate:", err)
			return nil
		}
		cwd, err := os.Getwd()
		if err != nil {
			return nil
		}
		result := gitgate.Run(context.Background(), cfg, reviewer, cwd)
		result.Write(os.Stderr)
		if result.Block {
			os.Exit(2)
		}
		return nil
	},
}

var commitGateCodexReviewCmd = &cobra.Command{
	Use:   "review-codex",
	Short: "Codex PreToolUse hook: rule on an exec_command git commit before it runs",
	Long: `review-codex reads a Codex PreToolUse payload on stdin and writes Codex's
native hook reply on stdout. It always exits 0 so adapter failures fail open.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := gateConfig()
		reviewer, err := buildReviewer(cfg)
		if err != nil {
			fmt.Fprintln(os.Stderr, "steer commit-gate:", err)
			return nil
		}
		gatecodex.Run(context.Background(), cfg, reviewer, os.Stdin, os.Stdout)
		return nil
	},
}

var commitGateInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Arm the commit gate on one channel",
	Long: `install writes the PreToolUse entry into settings.json, with a timeout derived
from the reviewer timeout so the pair cannot drift. Use --precommit to install the git
pre-commit hook in this repository instead.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := gateConfig()
		if commitGatePreCommitChannel {
			return installPreCommitChannel(cfg)
		}
		return installReviewChannel(cfg)
	},
}

var commitGatePreCommitChannel bool

func installReviewChannel(cfg gate.Config) error {
	scope := currentScope()
	if !commitGateBothChannels {
		if armed, path := preCommitArmedHere(); armed {
			return fmt.Errorf("the pre-commit channel is already armed in this repository (%s); "+
				"arming both reviews every commit twice — pass --both-channels to do it anyway", path)
		}
	}
	timeout := int(cfg.ReviewTimeout.Seconds()) + commitGateTimeoutMargin
	target, err := harness.Parse(commitGateHarness)
	if err != nil {
		return err
	}
	entry, _ := harness.Get(target)
	var path string
	if target == harness.Codex {
		cwd, cwdErr := codexProjectDir()
		if cwdErr != nil {
			return cwdErr
		}
		path, err = codexhooks.RegisterCommitGate(scope, cwd, commitGateCodexCommand(), timeout)
	} else {
		// The review hook and the resolver are one Claude Code installation, armed in
		// one write: the review writes the rows the resolver later reconciles from the
		// transcript, so a half-armed install leaves the ledger permanently unresolved.
		err = claude.RegisterCommitGateAndResolver(scope, commitGateCommand(), timeout, backfillCommand(), backfillHookTimeout)
	}
	if err != nil {
		return err
	}
	fmt.Println()
	message := fmt.Sprintf("Armed the %s commit gate on PreToolUse\nScope: %s\nTimeout: %ds (reviewer timeout %ds + %ds margin)",
		entry.DisplayName, scope, timeout, int(cfg.ReviewTimeout.Seconds()), commitGateTimeoutMargin)
	if target == harness.ClaudeCode {
		message += "\nResolver: Stop hook armed (steer commit-gate backfill)"
	}
	if target == harness.Codex {
		message += "\nHooks: " + path + "\nTrust: restart Codex and approve this definition in /hooks"
	}
	fmt.Println(styles.SuccessBox.Render(message))
	return nil
}

func installPreCommitChannel(cfg gate.Config) error {
	if !commitGateBothChannels {
		if armed, scope := reviewChannelArmed(); armed {
			return fmt.Errorf("the PreToolUse channel is already armed (%s scope); "+
				"arming both reviews every commit twice — pass --both-channels to do it anyway", scope)
		}
	}
	hooksDir, err := gitHooksDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		return fmt.Errorf("failed to create hooks directory: %w", err)
	}
	path := filepath.Join(hooksDir, "pre-commit")
	existing, readErr := os.ReadFile(path)
	if readErr == nil {
		// Only THIS hook is safe to replace. `steer git-hooks install` writes its own
		// pre-commit doing a different job, and overwriting it silently disarms the
		// verification a repository already had.
		if !strings.Contains(string(existing), commitGatePreCommitMarker) && !commitGateForce {
			return fmt.Errorf("pre-commit hook already exists and is not the commit gate's: %s (use --force to overwrite)", path)
		}
	} else if !os.IsNotExist(readErr) {
		return fmt.Errorf("failed to read existing pre-commit hook: %w", readErr)
	}
	if err := os.WriteFile(path, []byte(buildCommitGatePreCommitScript()), 0o755); err != nil {
		return fmt.Errorf("failed to write pre-commit hook: %w", err)
	}
	fmt.Println()
	fmt.Println(styles.SuccessBox.Render("Armed the commit gate on the pre-commit channel\nPath: " + path))
	return nil
}

// buildCommitGatePreCommitScript writes the wrapper git runs.
//
// It forwards no arguments, because git invokes a pre-commit hook with none. It DOES
// carry the provider and model, or the installed channel would silently fall back to
// the default reviewer while `status` reported the configured one.
func buildCommitGatePreCommitScript() string {
	return strings.Join([]string{
		"#!/usr/bin/env bash",
		"set -uo pipefail",
		"",
		commitGatePreCommitMarker,
		"if ! command -v steer >/dev/null 2>&1; then",
		"  echo \"steer commit-gate: 'steer' is not in PATH.\" >&2",
		"  exit 0",
		"fi",
		"",
		"steer commit-gate precommit" + commitGateProviderArgs(),
		"",
	}, "\n")
}

func commitGateCommand() string {
	return "steer commit-gate review" + commitGateProviderArgs()
}

func commitGateCodexCommand() string {
	return "steer commit-gate review-codex" + commitGateProviderArgs()
}

// commitGateProviderArgs renders the reviewer selection both channels must persist.
func commitGateProviderArgs() string {
	var parts []string
	if commitGateProvider != "" {
		parts = append(parts, " --provider ", commitGateProvider)
	}
	if commitGateModel != "" {
		parts = append(parts, " --model ", commitGateModel)
	}
	return strings.Join(parts, "")
}

func preCommitArmedHere() (bool, string) {
	hooksDir, err := gitHooksDir()
	if err != nil {
		return false, ""
	}
	path := filepath.Join(hooksDir, "pre-commit")
	raw, err := os.ReadFile(path)
	if err != nil {
		return false, ""
	}
	return strings.Contains(string(raw), commitGatePreCommitMarker), path
}

func reviewChannelArmed() (bool, string) {
	for _, scope := range []string{claude.ProjectScope, claude.GlobalScope} {
		if claude.CommitGateHookArmed(scope).Registered {
			return true, scope
		}
	}
	cwd, _ := codexProjectDir()
	for _, scope := range []string{claude.ProjectScope, claude.GlobalScope} {
		if codexhooks.CommitGateArming(scope, cwd).Registered {
			return true, "codex " + scope
		}
	}
	return false, ""
}

var commitGateRemoveCmd = &cobra.Command{
	Use:   "remove",
	Short: "Remove one harness's PreToolUse commit gate",
	RunE: func(cmd *cobra.Command, args []string) error {
		scope := currentScope()
		target, err := harness.Parse(commitGateHarness)
		if err != nil {
			return err
		}
		if target == harness.Codex {
			cwd, err := codexProjectDir()
			if err != nil {
				return err
			}
			path, err := codexhooks.RemoveCommitGate(scope, cwd)
			if err == nil {
				fmt.Println("Removed Codex commit gate from " + path)
			}
			return err
		}
		// Both steer-owned Claude Code entries come down in one write, preserving
		// unrelated PreToolUse and Stop hooks.
		return claude.RemoveCommitGateAndResolver(scope)
	},
}

var (
	backfillApply     bool
	backfillIfDue     bool
	backfillQuiet     bool
	backfillReresolve bool
)

var commitGateBackfillCmd = &cobra.Command{
	Use:   "backfill",
	Short: "Reconcile ledger_found for retry rows from local transcripts",
	Long: `backfill harvests the agent's self-reported ledger from ~/.claude/projects and
stamps ledger_found on this machine's shard. The PreToolUse hook fires before the
retry commit runs, so the ledger lands after the row; this resolves it late. It is
idempotent and, without --apply, writes nothing.

  --apply      write the resolved rows (default is a dry run)
  --if-due     exit early unless some row is unresolved and old enough to resolve
  --quiet      silence the report
  --reresolve  re-examine rows already stamped as a miss (never removes a ledger)`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := gateConfig()
		home, err := os.UserHomeDir()
		if err != nil {
			return nil
		}
		opts := cc.BackfillOptions{
			Apply:     backfillApply,
			IfDue:     backfillIfDue,
			Quiet:     backfillQuiet,
			Reresolve: backfillReresolve,
		}
		cc.RunBackfill(cfg, filepath.Join(home, ".claude", "projects"), opts, os.Stdout, nil)
		return nil
	},
}

var commitGateStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Report which channels are armed and where the ledger lives",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := gateConfig()
		// Resolve the reviewer so status reports the model that would ACTUALLY run.
		// Reading the config alone prints the default pin under every provider.
		reviewer, err := buildReviewer(cfg)
		if err != nil {
			return err
		}
		cfg.ReviewerModel = reviewer.ModelLabel()
		fmt.Println()
		fmt.Println(styles.MainHeader.Render("Commit Gate"))
		fmt.Println()
		fmt.Printf("  Instrument   %s\n", cfg.InstrumentID())
		fmt.Printf("  Ledger       %s\n", cfg.LogFile())
		fmt.Printf("  Reviewers    %d x %s via %s, %s each\n",
			cfg.ReviewerRuns, cfg.ReviewerModel, reviewer.Name(), cfg.ReviewTimeout)
		fmt.Printf("  Cap          %d firings\n", cfg.MaxFirings)
		for _, scope := range []string{claude.ProjectScope, claude.GlobalScope} {
			arming := claude.CommitGateHookArmed(scope)
			state := "not armed"
			switch {
			case arming.Effective():
				state = fmt.Sprintf("armed, timeout %ds", arming.Timeout)
			case arming.Registered:
				// Present but never invoked on a commit. Naming it plainly is the whole
				// point: the config looks armed and nothing is being reviewed.
				state = fmt.Sprintf("REGISTERED BUT INERT — matcher %q does not match %q, "+
					"so commits are NOT reviewed; re-run install", arming.Matcher, claude.CommitGateMatcher)
			}
			fmt.Printf("  Claude       %s scope: %s\n", scope, state)
		}
		for _, scope := range []string{claude.ProjectScope, claude.GlobalScope} {
			armed, timeout := claude.BackfillHookArmed(scope)
			state := "not armed"
			if armed {
				state = fmt.Sprintf("armed, timeout %ds", timeout)
			}
			fmt.Printf("  Resolver     %s scope: %s\n", scope, state)
		}
		if unresolved := cc.CountUnresolved(cfg); unresolved > 0 {
			fmt.Printf("  Unresolved   %d row(s) owe a ledger — run: steer commit-gate backfill --apply\n", unresolved)
		}
		cwd, _ := codexProjectDir()
		for _, scope := range []string{claude.ProjectScope, claude.GlobalScope} {
			arming := codexhooks.CommitGateArming(scope, cwd)
			state := "not armed"
			switch {
			case arming.Effective():
				trust := "no trust record — approval required"
				if !arming.TrustStateKnown {
					trust = "trust state unavailable"
				} else if arming.TrustRecordPresent {
					trust = "trust record present, handler hash unverified"
				}
				state = fmt.Sprintf("registered, timeout %ds, %s", arming.Timeout, trust)
			case arming.Registered:
				state = fmt.Sprintf("REGISTERED BUT INERT — matcher %q does not match %q", arming.Matcher, codexhooks.CommitGateMatcher)
			}
			fmt.Printf("  Codex        %s scope: %s\n", scope, state)
		}
		armed, path := preCommitArmedHere()
		if armed {
			fmt.Printf("  pre-commit   armed: %s\n", path)
		} else {
			fmt.Printf("  pre-commit   not armed in this repository\n")
		}
		return nil
	},
}

func codexProjectDir() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	if repo := gate.Toplevel(cwd); repo != "" {
		return repo, nil
	}
	return cwd, nil
}

func init() {
	rootCmd.AddCommand(commitGateCmd)
	commitGateCmd.AddCommand(commitGateReviewCmd, commitGateCodexReviewCmd, commitGatePreCommitCmd,
		commitGateInstallCmd, commitGateRemoveCmd, commitGateStatusCmd, commitGateBackfillCmd)

	commitGateBackfillCmd.Flags().BoolVar(&backfillApply, "apply", false, "Write the resolved rows")
	commitGateBackfillCmd.Flags().BoolVar(&backfillIfDue, "if-due", false, "Exit early unless a row is due")
	commitGateBackfillCmd.Flags().BoolVar(&backfillQuiet, "quiet", false, "Silence the report")
	commitGateBackfillCmd.Flags().BoolVar(&backfillReresolve, "reresolve", false, "Re-examine stamped misses")

	commitGateCmd.PersistentFlags().StringVar(&commitGateProvider, "provider", "",
		"Reviewer provider (agy|codex|claude-code); default agy")
	commitGateCmd.PersistentFlags().StringVar(&commitGateModel, "model", "",
		"Reviewer model; default is the provider's shipped pin")

	commitGateInstallCmd.Flags().BoolVar(&commitGatePreCommitChannel, "precommit", false,
		"Install the git pre-commit hook instead of the PreToolUse entry")
	commitGateInstallCmd.Flags().BoolVar(&commitGateBothChannels, "both-channels", false,
		"Arm this channel even though the other one is already armed for this repository")
	commitGateInstallCmd.Flags().BoolVar(&commitGateForce, "force", false,
		"Overwrite an existing non-steer pre-commit hook")
	commitGateInstallCmd.Flags().StringVar(&commitGateHarness, "harness", "claude-code",
		"PreToolUse harness (claude-code|codex)")
	commitGateRemoveCmd.Flags().StringVar(&commitGateHarness, "harness", "claude-code",
		"PreToolUse harness (claude-code|codex)")
}
