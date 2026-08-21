package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/gate"
	"github.com/steer-sh/steer-cli/internal/gate/cc"
	"github.com/steer-sh/steer-cli/internal/gate/gitgate"
	"github.com/steer-sh/steer-cli/internal/gate/reviewers"
	"github.com/steer-sh/steer-cli/internal/styles"
)

const (
	commitGatePreCommitMarker = "# steer-managed: commit-gate"
	// The PreToolUse timeout must EXCEED the per-reviewer wall clock, or a slow review
	// is killed mid-flight and the commit lands ungated with no message AND no row. A
	// value equal to the review timeout does exactly that, so the margin is not
	// decoration.
	commitGateTimeoutMargin = 60
)

var (
	commitGateProvider     string
	commitGateModel        string
	commitGateBothChannels bool
	commitGateForce        bool
)

var commitGateCmd = &cobra.Command{
	Use:   "commit-gate",
	Short: "Red-team a commit's diff before it lands",
	Long: `commit-gate runs a panel of independent reviewers over a commit's diff and
blocks the commit while a finding stands, writing an adjudication row for every
decision.

Two channels share one review engine:
  review     PreToolUse hook for Claude Code — reads a JSON payload on stdin
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
	if err := claude.RegisterCommitGateHook(scope, commitGateCommand(), timeout); err != nil {
		return err
	}
	fmt.Println()
	fmt.Println(styles.SuccessBox.Render(fmt.Sprintf(
		"Armed the commit gate on the PreToolUse channel\nScope: %s\nTimeout: %ds (reviewer timeout %ds + %ds margin)",
		scope, timeout, int(cfg.ReviewTimeout.Seconds()), commitGateTimeoutMargin)))
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
	return false, ""
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
			fmt.Printf("  PreToolUse   %s scope: %s\n", scope, state)
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

func init() {
	rootCmd.AddCommand(commitGateCmd)
	commitGateCmd.AddCommand(commitGateReviewCmd, commitGatePreCommitCmd,
		commitGateInstallCmd, commitGateStatusCmd)

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
}
