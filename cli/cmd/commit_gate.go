package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/spf13/cobra"
	"github.com/steer-sh/steer-cli/internal/claude"
	codexhooks "github.com/steer-sh/steer-cli/internal/codex"
	"github.com/steer-sh/steer-cli/internal/gate"
	"github.com/steer-sh/steer-cli/internal/gate/cc"
	gatecodex "github.com/steer-sh/steer-cli/internal/gate/codex"
	"github.com/steer-sh/steer-cli/internal/gate/gitgate"
	gatepi "github.com/steer-sh/steer-cli/internal/gate/pi"
	"github.com/steer-sh/steer-cli/internal/gate/resolve"
	"github.com/steer-sh/steer-cli/internal/harness"
	pihooks "github.com/steer-sh/steer-cli/internal/pi"
	"github.com/steer-sh/steer-cli/internal/settings"
	"github.com/steer-sh/steer-cli/internal/steerbin"
	"github.com/steer-sh/steer-cli/internal/styles"
	transcriptpi "github.com/steer-sh/steer-cli/internal/transcript/pi"
	"github.com/steer-sh/steer-cli/internal/utils"
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

// backfillCommand is the turn-end hook steer registers alongside the review hook:
// harvest late, only when a row is due, silently.
func backfillCommand() string {
	return steerHookCommand("commit-gate backfill --if-due --apply --quiet")
}

var (
	commitGateProvider     string
	commitGateModel        string
	commitGateEffort       string
	commitGateRuns         int
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
  review-pi     tool_call bridge for Pi — reads a JSON payload on stdin
  precommit  git pre-commit hook — reads the index

Arming both channels in one repository reviews every commit twice. Use
"install --both-channels" to say that is intended.`,
	Example: `  steer commit-gate install --scope global
  steer commit-gate status
  steer commit-gate precommit`,
}

// buildReviewer resolves the provider through the one resolver every editor shares,
// after the --provider flag has outranked the settings file.
func buildReviewer(cfg gate.Config) (gate.Reviewer, error) {
	if commitGateProvider != "" {
		cfg.ReviewerProvider = commitGateProvider
	}
	return settings.BuildReviewer(cfg, warnOnce)
}

// warnOnce prints a reviewer warning once per process. One command resolves the
// reviewer several times, and the hook paths resolve it beside gateConfig.
func warnOnce(warning string) {
	effortWarnOnce.Do(func() { fmt.Fprintln(os.Stderr, "steer:", warning) })
}

// configWarnOnce holds a broken settings file to one line per process. One command
// calls gateConfig several times.
var configWarnOnce sync.Once

// effortWarnOnce does the same for an effort no provider documents. buildReviewer runs
// once per entry point, and the hook paths call it beside gateConfig.
var effortWarnOnce sync.Once

func gateConfig() gate.Config {
	cfg, err := gate.Load()
	if err != nil {
		configWarnOnce.Do(func() {
			fmt.Fprintln(os.Stderr, "steer:", err, "— using the shipped defaults")
		})
	}
	if commitGateModel != "" {
		cfg.ReviewerModel = commitGateModel
		cfg.ReviewerModelOverride = commitGateModel
	}
	if commitGateEffort != "" {
		cfg.ReviewerEffort = commitGateEffort
	}
	if commitGateRuns > 0 {
		cfg.ReviewerRuns = commitGateRuns
	}
	return cfg
}

// resolvedGateConfig returns the configuration with ReviewerModel naming the model
// that would ACTUALLY run, beside the reviewer it resolved. An unresolvable provider
// yields the error, no reviewer, and the operator's own configured model where that
// label would have gone.
func resolvedGateConfig() (gate.Config, gate.Reviewer, error) {
	return resolveReviewer(gateConfig())
}

// resolveReviewer is resolvedGateConfig over a configuration the caller holds, which
// is how a preview of an unwritten settings edit names the model that edit would run.
func resolveReviewer(cfg gate.Config) (gate.Config, gate.Reviewer, error) {
	if commitGateProvider != "" {
		cfg.ReviewerProvider = commitGateProvider
	}
	return settings.Resolve(cfg, warnOnce)
}

var commitGateReviewCmd = &cobra.Command{
	Use:   "review",
	Short: "PreToolUse hook: rule on a Bash `git commit` before it runs",
	Long: `review reads a Claude Code PreToolUse payload on stdin and writes the hook's
reply on stdout. It always exits 0: a gate that cannot answer must fail open.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := prepareState(); err != nil {
			fmt.Fprintln(os.Stderr, "steer commit-gate:", err)
		}
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
		if err := prepareState(); err != nil {
			fmt.Fprintln(os.Stderr, "steer commit-gate:", err)
		}
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
	Short: "Codex PreToolUse hook: rule on a Bash git commit before it runs",
	Long: `review-codex reads a Codex PreToolUse payload on stdin and writes Codex's
native hook reply on stdout. It always exits 0 so adapter failures fail open.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := prepareState(); err != nil {
			fmt.Fprintln(os.Stderr, "steer commit-gate:", err)
		}
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

var commitGatePiReviewCmd = &cobra.Command{
	Use:   "review-pi",
	Short: "Pi tool_call hook: rule on a Bash git commit before it runs",
	Long: `review-pi reads the Pi bridge's PreToolUse payload on stdin and writes Pi's
native tool_call reply on stdout. It always exits 0 so adapter failures fail open.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := prepareState(); err != nil {
			fmt.Fprintln(os.Stderr, "steer commit-gate:", err)
		}
		cfg := gateConfig()
		reviewer, err := buildReviewer(cfg)
		if err != nil {
			fmt.Fprintln(os.Stderr, "steer commit-gate:", err)
			return nil
		}
		gatepi.Run(context.Background(), cfg, reviewer, os.Stdin, os.Stdout)
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
		if err := prepareState(); err != nil {
			fmt.Fprintln(os.Stderr, "steer commit-gate:", err)
		}
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
	switch target {
	case harness.Codex:
		cwd, cwdErr := codexProjectDir()
		if cwdErr != nil {
			return cwdErr
		}
		path, err = codexhooks.RegisterCommitGateAndResolver(scope, cwd,
			commitGateCodexCommand(), timeout, backfillCommand(), backfillHookTimeout)
	case harness.Pi:
		cwd, cwdErr := codexProjectDir()
		if cwdErr != nil {
			return cwdErr
		}
		path, err = pihooks.RegisterCommitGateAndResolver(scope, cwd,
			commitGatePiCommand(), timeout, backfillCommand(), backfillHookTimeout)
	default:
		// The review hook and the resolver are one Claude Code installation, armed in
		// one write: the review writes the rows the resolver later reconciles from the
		// transcript, so a half-armed install leaves the ledger permanently unresolved.
		err = claude.RegisterCommitGateAndResolver(scope, commitGateCommand(), timeout, backfillCommand(), backfillHookTimeout)
	}
	if err != nil {
		return err
	}
	// The registration this install writes carries no reviewer flag; one written by an
	// older steer still does, and it outranks the settings file wherever it survives.
	if binPath, binErr := steerbin.Ensure(); binErr == nil {
		if _, repairErr := repointRegistrations(binPath); repairErr != nil {
			utils.Warning("could not repair the other registrations: " + repairErr.Error())
		}
	}
	fmt.Println()
	message := fmt.Sprintf("Armed the %s commit gate on PreToolUse\nScope: %s\nTimeout: %ds (reviewer timeout %ds + %ds margin)",
		entry.DisplayName, scope, timeout, int(cfg.ReviewTimeout.Seconds()), commitGateTimeoutMargin)
	if target == harness.ClaudeCode {
		message += "\nResolver: Stop hook armed (steer commit-gate backfill)"
	}
	if target == harness.Codex {
		message += "\nResolver: Stop and SubagentStop hooks armed (steer commit-gate backfill)" +
			"\nHooks: " + path + "\nTrust: restart Codex and approve these definitions in /hooks"
	}
	if target == harness.Pi {
		message += "\nResolver: turn_end handler armed (steer commit-gate backfill)" +
			"\nExtension: " + path + "\nTrust: project extensions run after Pi trusts the project"
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
// It forwards no arguments. git invokes a pre-commit hook with none, and the reviewer
// is read from ~/.steer/config.json at firing time: a flag written here would outrank
// that file and pin the reviewer this install happened to name.
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
		"steer commit-gate precommit",
		"",
	}, "\n")
}

// commandNote warns when a registered hook command does not resolve from here. A
// harness that cannot resolve a hook command runs the tool anyway and reports
// nothing, so this line is the only place the failure is visible.
func commandNote(registered bool, command string) string {
	if !registered || steerbin.Resolve(command) != steerbin.Missing {
		return ""
	}
	return "\n               the command does not resolve here — the hook is skipped " +
		"silently; run: steer bin install"
}

func commitGateCommand() string {
	return steerHookCommand("commit-gate review")
}

func commitGateCodexCommand() string {
	return steerHookCommand("commit-gate review-codex")
}

func commitGatePiCommand() string {
	return steerHookCommand("commit-gate review-pi")
}

// reviewerFlags is the shared list the dashboard's Reviewer panel also scans.
var reviewerFlags = settings.ReviewerFlags

// pinnedRegistration is one installed command that still names a reviewer.
type pinnedRegistration struct {
	// Label names the channel a reader would recognise it by.
	Label string
	// Command is the command as registered.
	Command string
	// Repair is the command that rewrites it.
	Repair string
}

// pinnedRegistrations reports every installed commit-gate command still carrying a
// reviewer flag. Steer wrote these before the reviewer moved into the settings file;
// each one silently overrides that file until it is stripped.
func pinnedRegistrations() []pinnedRegistration {
	var pinned []pinnedRegistration
	add := func(label, command, repair string) {
		if steerbin.HasFlags(command, reviewerFlags...) {
			pinned = append(pinned, pinnedRegistration{Label: label, Command: command, Repair: repair})
		}
	}
	for _, scope := range []string{claude.ProjectScope, claude.GlobalScope} {
		if arming := claude.CommitGateHookArmed(scope); arming.Registered {
			add("claude-code "+scope+" scope", arming.Command, "steer bin install")
		}
	}
	cwd, _ := codexProjectDir()
	for _, scope := range []string{claude.ProjectScope, claude.GlobalScope} {
		if arming := pihooks.CommitGateArming(scope, cwd); arming.Registered {
			add("pi "+scope+" scope", arming.Command, "steer bin install")
		}
	}
	for _, scope := range []string{claude.ProjectScope, claude.GlobalScope} {
		if arming := codexhooks.CommitGateArming(scope, cwd); arming.Registered {
			add("codex "+scope+" scope", arming.Command, "steer bin install")
		}
	}
	if armed, path := preCommitArmedHere(); armed {
		if raw, err := os.ReadFile(path); err == nil {
			for _, line := range strings.Split(string(raw), "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "steer commit-gate precommit") {
					add("pre-commit "+path, line, "steer commit-gate install --precommit --force")
				}
			}
		}
	}
	return pinned
}

// reportPinnedRegistrations names every registration that overrides the settings
// file. A silent override is the failure this reporting exists to remove, so it is
// never reachable without a line saying so.
func reportPinnedRegistrations() {
	pinned := pinnedRegistrations()
	if len(pinned) == 0 {
		return
	}
	utils.Warning(fmt.Sprintf("%d registration(s) name the reviewer on the command line, "+
		"which outranks ~/.steer/config.json:", len(pinned)))
	for _, row := range pinned {
		fmt.Printf("    %-28s %s\n", row.Label, row.Command)
		fmt.Printf("    %-28s %s\n", "", styles.MutedText.Render("repair: "+row.Repair))
	}
	fmt.Println()
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
	for _, scope := range []string{claude.ProjectScope, claude.GlobalScope} {
		if pihooks.CommitGateArming(scope, cwd).Registered {
			return true, "pi " + scope
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
		switch target {
		case harness.Codex:
			cwd, err := codexProjectDir()
			if err != nil {
				return err
			}
			path, err := codexhooks.RemoveCommitGateAndResolver(scope, cwd)
			if err == nil {
				fmt.Println("Removed Codex commit gate from " + path)
			}
			return err
		case harness.Pi:
			cwd, err := codexProjectDir()
			if err != nil {
				return err
			}
			path, err := pihooks.RemoveCommitGateAndResolver(scope, cwd)
			if err == nil {
				fmt.Println("Removed Pi commit gate from " + path)
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
		// A dry run writes nothing, and migrating the corpus is a write.
		if backfillApply {
			if err := prepareState(); err != nil {
				fmt.Fprintln(os.Stderr, "steer commit-gate:", err)
			}
		}
		cfg := gateConfig()
		opts := resolve.BackfillOptions{
			Apply:     backfillApply,
			IfDue:     backfillIfDue,
			Quiet:     backfillQuiet,
			Reresolve: backfillReresolve,
		}
		resolve.RunBackfill(cfg, ledgerHarvests(), opts, os.Stdout, nil)
		return nil
	},
}

// ledgerHarvests is one harvest per harness whose transcripts are readable here. A
// harness with no tree contributes nothing rather than failing the run.
func ledgerHarvests() []resolve.Harvest {
	var harvests []resolve.Harvest
	if home, err := os.UserHomeDir(); err == nil {
		harvests = append(harvests, cc.Harvest(filepath.Join(home, ".claude", "projects")))
	}
	if root, err := gatecodex.SessionsRoot(); err == nil {
		harvests = append(harvests, gatecodex.Harvest(root))
	}
	if root, err := transcriptpi.New().Root(); err == nil {
		harvests = append(harvests, gatepi.Harvest(root))
	}
	return harvests
}

var commitGateStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Report which channels are armed and where the ledger lives",
	RunE: func(cmd *cobra.Command, args []string) error {
		// This view prints the provider's own name, so an unresolvable one is a
		// refusal here rather than a dropped field.
		cfg, reviewer, err := resolvedGateConfig()
		if err != nil {
			return err
		}
		fmt.Println()
		fmt.Println(styles.MainHeader.Render("Commit Gate"))
		fmt.Println()
		fmt.Printf("  Instrument   %s\n", cfg.InstrumentID())
		fmt.Printf("  Ledger       %s\n", cfg.LogFile())
		fmt.Printf("  Reviewers    %d x %s%s via %s, %s each\n",
			cfg.ReviewerRuns, cfg.ReviewerModel, effortNote(cfg.ReviewerEffort),
			reviewer.Name(), cfg.ReviewTimeout)
		fmt.Printf("  Cap          %d firings\n", cfg.MaxFirings)
		cwd, _ := codexProjectDir()
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
			fmt.Printf("  Claude       %s scope: %s%s\n", scope, state,
				commandNote(arming.Registered, arming.Command))
		}
		for _, scope := range []string{claude.ProjectScope, claude.GlobalScope} {
			arming := pihooks.CommitGateArming(scope, cwd)
			state := "not armed"
			if arming.Registered {
				state = fmt.Sprintf("registered, timeout %ds, trust state unavailable", arming.Timeout)
			}
			fmt.Printf("  Pi           %s scope: %s%s\n", scope, state,
				commandNote(arming.Registered, arming.Command))
			resolver := pihooks.ResolverArming(scope, cwd)
			resolverState := "not armed"
			if resolver.Registered {
				resolverState = fmt.Sprintf("turn_end armed, timeout %ds", resolver.Timeout)
			}
			fmt.Printf("  Resolver     pi %s scope: %s%s\n", scope, resolverState,
				commandNote(resolver.Registered, resolver.Command))
		}
		for _, scope := range []string{claude.ProjectScope, claude.GlobalScope} {
			resolver := claude.BackfillHookArming(scope)
			state := "not armed"
			if resolver.Registered {
				state = fmt.Sprintf("armed, timeout %ds", resolver.Timeout)
			}
			fmt.Printf("  Resolver     %s scope: %s%s\n", scope, state,
				commandNote(resolver.Registered, resolver.Command))
		}
		if unresolved := resolve.CountUnresolved(cfg); unresolved > 0 {
			fmt.Printf("  Unresolved   %d row(s) owe a ledger — run: steer commit-gate backfill --apply\n", unresolved)
		}
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
				state = fmt.Sprintf("REGISTERED BUT INERT — matcher %q does not match %q, "+
					"so commits are NOT reviewed; re-run install", arming.Matcher, codexhooks.CommitGateMatcher)
			}
			fmt.Printf("  Codex        %s scope: %s%s\n", scope, state,
				commandNote(arming.Registered, arming.Command))
			resolver := codexhooks.ResolverArmingRow(scope, cwd)
			resolverState := "not armed"
			if resolver.Registered {
				resolverState = fmt.Sprintf("Stop and SubagentStop armed, timeout %ds", resolver.Timeout)
			}
			fmt.Printf("  Resolver     codex %s scope: %s%s\n", scope, resolverState,
				commandNote(resolver.Registered, resolver.Command))
		}
		armed, path := preCommitArmedHere()
		if armed {
			fmt.Printf("  pre-commit   armed: %s\n", path)
		} else {
			fmt.Printf("  pre-commit   not armed in this repository\n")
		}
		fmt.Println()
		reportPinnedRegistrations()
		return nil
	},
}

// effortNote renders the effort beside the model, and nothing when it is unset —
// an unset effort is the provider's own, which Steer cannot name.
func effortNote(effort string) string {
	if strings.TrimSpace(effort) == "" {
		return ""
	}
	return " @ " + effort
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
	commitGateCmd.AddCommand(commitGateReviewCmd, commitGateCodexReviewCmd, commitGatePiReviewCmd, commitGatePreCommitCmd,
		commitGateInstallCmd, commitGateRemoveCmd, commitGateStatusCmd, commitGateBackfillCmd)

	commitGateBackfillCmd.Flags().BoolVar(&backfillApply, "apply", false, "Write the resolved rows")
	commitGateBackfillCmd.Flags().BoolVar(&backfillIfDue, "if-due", false, "Exit early unless a row is due")
	commitGateBackfillCmd.Flags().BoolVar(&backfillQuiet, "quiet", false, "Silence the report")
	commitGateBackfillCmd.Flags().BoolVar(&backfillReresolve, "reresolve", false, "Re-examine stamped misses")

	commitGateCmd.PersistentFlags().StringVar(&commitGateProvider, "provider", "",
		"Reviewer provider (agy|codex|claude-code); default agy")
	commitGateCmd.PersistentFlags().StringVar(&commitGateModel, "model", "",
		"Reviewer model; default is the provider's shipped pin")
	commitGateCmd.PersistentFlags().StringVar(&commitGateEffort, "effort", "",
		"Reviewer reasoning effort; default is the provider's own")
	commitGateCmd.PersistentFlags().IntVar(&commitGateRuns, "runs", 0,
		"Reviewers in the panel; default 3")

	commitGateInstallCmd.Flags().BoolVar(&commitGatePreCommitChannel, "precommit", false,
		"Install the git pre-commit hook instead of the PreToolUse entry")
	commitGateInstallCmd.Flags().BoolVar(&commitGateBothChannels, "both-channels", false,
		"Arm this channel even though the other one is already armed for this repository")
	commitGateInstallCmd.Flags().BoolVar(&commitGateForce, "force", false,
		"Overwrite an existing non-steer pre-commit hook")
	commitGateInstallCmd.Flags().StringVar(&commitGateHarness, "harness", "claude-code",
		"PreToolUse harness (claude-code|codex|pi)")
	commitGateRemoveCmd.Flags().StringVar(&commitGateHarness, "harness", "claude-code",
		"PreToolUse harness (claude-code|codex|pi)")
}
