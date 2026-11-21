package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"
	"github.com/steer-sh/steer-cli/internal/checks"
	"github.com/steer-sh/steer-cli/internal/claude"
	codexhooks "github.com/steer-sh/steer-cli/internal/codex"
	"github.com/steer-sh/steer-cli/internal/gate"
	"github.com/steer-sh/steer-cli/internal/gate/cc"
	gatecodex "github.com/steer-sh/steer-cli/internal/gate/codex"
	"github.com/steer-sh/steer-cli/internal/gate/harvests"
	gatepi "github.com/steer-sh/steer-cli/internal/gate/pi"
	"github.com/steer-sh/steer-cli/internal/gate/resolve"
	"github.com/steer-sh/steer-cli/internal/gate/reviewers"
	"github.com/steer-sh/steer-cli/internal/gate/stophook"
	"github.com/steer-sh/steer-cli/internal/harness"
	pihooks "github.com/steer-sh/steer-cli/internal/pi"
	"github.com/steer-sh/steer-cli/internal/settings"
	"github.com/steer-sh/steer-cli/internal/status"
	"github.com/steer-sh/steer-cli/internal/steerbin"
	"github.com/steer-sh/steer-cli/internal/styles"
	"github.com/steer-sh/steer-cli/internal/utils"
)

const (
	commitGatePreCommitMarker = "# steer-managed: commit-gate"
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
	commitGateProvider string
	commitGateModel    string
	commitGateEffort   string
	commitGateRuns     int
	commitGateHarness  string
)

var commitGateCmd = &cobra.Command{
	Use:   "commit-gate",
	Short: "Red-team a commit's diff before it lands",
	Long: `commit-gate runs a panel of independent reviewers over a commit's diff and
blocks the commit while a finding stands, writing an adjudication row for every
decision.

Each harness's hook shares one review engine:
  review        PreToolUse hook for Claude Code — reads a JSON payload on stdin
  review-codex  PreToolUse hook for Codex — reads a JSON payload on stdin`,
	Example: `  steer commit-gate install --harness claude-code --scope global
  steer status`,
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
		cfg.OperatorNotice = status.PluginRemovedNotice()
		cc.Run(context.Background(), cfg, reviewer, os.Stdin, os.Stdout)
		return nil
	},
}

// commitGatePreCommitCmd answers the git pre-commit hook an earlier Steer installed,
// whose last line runs this command. git refuses the commit on a non-zero exit, so it
// exits 0 whatever it is given and names the command that removes the hook.
var commitGatePreCommitCmd = &cobra.Command{
	Use:                "precommit",
	Short:              "Removed: the git pre-commit review",
	Hidden:             true,
	DisableFlagParsing: true,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Fprintln(cmd.ErrOrStderr(), preCommitRemovedNotice)
	},
}

// preCommitRemovedNotice is the stub's one stderr line.
const preCommitRemovedNotice = "steer: the git pre-commit review is removed from Steer, and this hook reviews nothing; " +
	"to remove it, run: steer commit-gate remove --precommit"

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
		cfg.OperatorNotice = status.PluginRemovedNotice()
		gatecodex.Run(context.Background(), cfg, reviewer, os.Stdin, os.Stdout)
		return nil
	},
}

var commitGatePiReviewCmd = &cobra.Command{
	Use:    "review-pi",
	Hidden: true,
	Short:  "Pi tool_call hook: rule on a Bash git commit before it runs",
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
		cfg.OperatorNotice = status.PluginRemovedNotice()
		gatepi.Run(context.Background(), cfg, reviewer, os.Stdin, os.Stdout)
		return nil
	},
}

var commitGateTestReviewerCmd = &cobra.Command{
	Use:   "test-reviewer",
	Short: "Run one review of a sample diff with the configured reviewer",
	Long: `test-reviewer sends one small sample diff to the configured reviewer, with its
provider, model and effort, and checks that the reply follows the review protocol.
It shows that the reviewer CLI runs and answers here. It does not show that a harness
delivers the commit hook: 'steer status' reads that. It writes no ledger row, and it
exits non-zero when the reviewer does not answer.`,
	Example: `  steer commit-gate test-reviewer
  steer commit-gate test-reviewer --provider claude-code --model sonnet`,
	RunE: func(cmd *cobra.Command, args []string) error {
		base, err := providerSwitch(gateConfig(), commitGateProvider, commitGateModel, commitGateEffort)
		if err != nil {
			return err
		}
		cfg, reviewer, err := resolveReviewer(base)
		if err != nil {
			return err
		}
		fmt.Printf("Sending a sample diff to %s (%s%s). One run; this can take up to %s.\n",
			reviewers.ProviderLabel(reviewer.Name()), cfg.ReviewerModel, effortNote(cfg.ReviewerEffort), cfg.ReviewTimeout)
		result, err := gate.TrialReview(context.Background(), cfg, reviewer)
		if err != nil {
			return err
		}
		text, ok := renderTrial(result, cfg)
		fmt.Println()
		fmt.Print(text)
		if !ok {
			os.Exit(1)
		}
		return nil
	},
}

// providerSwitch prepares a trial under a --provider that names another provider
// than the configured one. The configured model names a model of that other
// provider, so it is refused unless --model replaces it; the configured effort gives
// way to the new provider's own unless --effort names one.
func providerSwitch(cfg gate.Config, provider, model, effort string) (gate.Config, error) {
	next, ok := reviewers.FindProvider(provider)
	current, known := reviewers.FindProvider(cfg.ReviewerProvider)
	if provider == "" || !ok || !known || next.ID == current.ID {
		return cfg, nil
	}
	if model == "" && cfg.ReviewerModelOverride != "" {
		return cfg, fmt.Errorf("the configured model %s is a %s model, and --provider %s names another provider; "+
			"name a model for it: steer commit-gate test-reviewer --provider %s --model <model>",
			cfg.ReviewerModelOverride, current.ID, next.ID, next.ID)
	}
	cfg.ReviewerEffort = effort
	return cfg, nil
}

// renderTrial words a trial review's result. ok is false when the reviewer did not
// answer, which is the failure every commit would meet.
func renderTrial(r gate.TrialResult, cfg gate.Config) (string, bool) {
	if r.Answered() {
		found := "no findings"
		if r.Findings == 1 {
			found = "1 finding"
		} else if r.Findings > 1 {
			found = fmt.Sprintf("%d findings", r.Findings)
		}
		text := styles.SuccessBox.Render(fmt.Sprintf(
			"The reviewer answered in %s with %s on the sample diff.\n"+
				"Each commit review runs it %d times in parallel.",
			r.Elapsed.Round(time.Second), found, cfg.ReviewerRuns)) + "\n"
		if t := r.Run.Tools; t != nil && t.Fallback == reviewers.NoViewFallback {
			text += styles.Wrap("The trial's sample repository had no commit view, so this run read only the diff. "+
				"So this test does not show whether the endpoint accepts the tools.", "  ", styles.Width())
		} else if t != nil && t.Fallback != "" {
			text += styles.Wrap("The endpoint refused the request that offered file tools, so this run read only the diff. Endpoint: "+truncateDetail(t.Fallback, 400), "  ", styles.Width()) +
				styles.Wrap("Some endpoints refuse tools together with reasoning; a lower reviewer.effort can let them through.", "  ", styles.Width())
		}
		return text, true
	}
	width := styles.Width()
	detail := string(r.Run.Outcome)
	if r.Run.Detail != "" {
		detail += ": " + truncateDetail(r.Run.Detail, 400)
	}
	hint := "Check that the reviewer CLI is signed in and that the model name is correct: steer config show"
	if cfg.ReviewerProvider == reviewers.APIProviderID {
		hint = apiTrialHint(r.Run.Outcome)
	}
	if os.Getenv(codexNoNetworkEnv) == "1" {
		hint = "This shell runs in Codex's sandbox with the network off, which stops a reviewer that needs its service. " +
			"Codex's commit hook runs outside that sandbox, so run this test again with escalated permissions first. " + hint
	}
	return styles.WarningBox.Render("The reviewer did not answer.\n"+
			"Until it answers, every commit goes through unreviewed.") + "\n" +
			styles.Wrap("Reviewer output: "+detail, "  ", width) +
			styles.Wrap(hint, "  ", width),
		false
}

// apiTrialHint names the likely fix for an API reviewer's failed run.
func apiTrialHint(outcome gate.RunOutcome) string {
	switch outcome {
	case gate.OutcomeRateLimited:
		return "The endpoint refused the request under a rate limit. Wait and run this test again, or choose another model."
	case gate.OutcomeAuth, gate.OutcomeNoKey:
		return "Check where the key comes from and that the endpoint accepts it: steer reviewer key status"
	case gate.OutcomeUnreachable:
		return "No reply came from the endpoint. Check reviewer.api.base_url, and that the server runs and this host reaches it: steer config show"
	}
	return "Check reviewer.api.base_url and that the endpoint serves the model: steer config show. " +
		"Check where the key comes from: steer reviewer key status"
}

// codexNoNetworkEnv is set to "1" by Codex in a command it runs in its sandbox with
// the network off.
const codexNoNetworkEnv = "CODEX_SANDBOX_NETWORK_DISABLED"

func truncateDetail(text string, n int) string {
	text = strings.TrimSpace(text)
	if len(text) <= n {
		return text
	}
	return text[:n] + "…"
}

var commitGateInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Arm the commit gate for one harness",
	Long: `install writes the harness's PreToolUse entry, with a timeout derived from the
reviewer timeout so the pair cannot drift.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := prepareState(); err != nil {
			fmt.Fprintln(os.Stderr, "steer commit-gate:", err)
		}
		return installReviewChannel(gateConfig())
	},
}

func installReviewChannel(cfg gate.Config) error {
	scope := currentScope()
	timeout := cfg.HookTimeout()
	target, err := harness.Parse(commitGateHarness)
	if err != nil {
		return err
	}
	entry, _ := harness.Get(target)
	var path, hookCommand string
	switch target {
	case harness.Codex:
		cwd, cwdErr := gate.ProjectDir()
		if cwdErr != nil {
			return cwdErr
		}
		hookCommand = commitGateCodexCommand()
		path, err = codexhooks.RegisterCommitGateAndResolver(scope, cwd,
			hookCommand, timeout, backfillCommand(), backfillHookTimeout)
	case harness.Pi:
		cwd, cwdErr := gate.ProjectDir()
		if cwdErr != nil {
			return cwdErr
		}
		hookCommand = commitGatePiCommand()
		path, err = pihooks.RegisterCommitGateAndResolver(scope, cwd,
			hookCommand, timeout, backfillCommand(), backfillHookTimeout)
	default:
		// The review hook and the resolver are one Claude Code installation, armed in
		// one write: the review writes the rows the resolver later reconciles from the
		// transcript, so a half-armed install leaves the ledger permanently unresolved.
		hookCommand = commitGateCommand()
		err = claude.RegisterCommitGateAndResolver(scope, hookCommand, timeout, backfillCommand(), backfillHookTimeout)
	}
	if err != nil {
		return err
	}
	// The registration this install writes carries no reviewer flag; one written by an
	// older steer still does, and it outranks the settings file wherever it survives.
	if binPath, binErr := steerbin.Ensure(); binErr == nil {
		if _, repairErr := repointRegistrations(&installReport{out: os.Stdout}, binPath); repairErr != nil {
			utils.Warning("could not repair the other registrations: " + repairErr.Error())
		}
	}
	registered := fmt.Sprintf("Registered: the %s commit gate on PreToolUse\nScope: %s\nTimeout: %ds (reviewer timeout %ds + %ds margin)",
		entry.DisplayName, scope, timeout, int(cfg.ReviewTimeout.Seconds()), gate.HookTimeoutMargin)
	switch target {
	case harness.ClaudeCode:
		registered += "\nReview outcomes: read at the end of each turn (Stop hook)"
	case harness.Codex:
		registered += "\nReview outcomes: read at the end of each turn (Stop and SubagentStop hooks)" +
			"\nHooks: " + path
	case harness.Pi:
		registered += "\nReview outcomes: read at the end of each turn (turn_end handler)" +
			"\nExtension: " + path
	}
	fmt.Println()
	fmt.Println(renderInstallReadiness(installSteps(cfg, target, hookCommand, scope, path), registered))
	return nil
}

// installSteps is what still stands between this registration and the first review.
// Registration is one of four states, and it is the only one the write itself proves:
// the harness must trust the hook, the hook command must resolve, and the reviewer CLI
// must exist. Reporting "armed" on the write alone names the weakest of the four.
func installSteps(cfg gate.Config, target harness.ID, command, scope, path string) []status.Step {
	var steps []status.Step
	_, _, resolveErr := resolveReviewer(cfg)
	if step, ok := status.ReviewerStep(settings.ReviewerProbe(cfg, resolveErr)); ok {
		steps = append(steps, step)
	}
	if step, ok := status.CommandStep(steerbin.Resolve(command)); ok {
		steps = append(steps, step)
	}
	entry, _ := harness.Get(target)
	if entry.Capabilities.TrustReview {
		// The registration block below names the file, so the step does not repeat it.
		trust := status.TrustUnknown
		if target == harness.Pi {
			trust = status.PiTrust(scope, ".", path)
		}
		if step, ok := status.TrustStep(string(target), trust, ""); ok {
			steps = append(steps, step)
		}
	}
	return steps
}

// renderInstallReadiness leads with whatever remains before the first review can
// happen, and puts the registration facts beneath it. A success box over an open step
// reads as a working gate.
func renderInstallReadiness(steps []status.Step, registered string) string {
	return renderReadinessBox(steps, "Ready — the next `git commit` in scope goes to the reviewer.\n"+
		"To check that the reviewer answers: steer commit-gate test-reviewer",
		"Not reviewing yet — %d %s before the first review.", registered)
}

// renderReadinessBox renders an install's readiness box. pending is a format taking
// the step count and "step remains" or "steps remain".
func renderReadinessBox(steps []status.Step, ready, pending, registered string) string {
	if len(steps) == 0 {
		return styles.SuccessBox.Render(ready + "\n\n" + registered)
	}
	word := "steps remain"
	if len(steps) == 1 {
		word = "step remains"
	}
	lines := []string{fmt.Sprintf(pending, len(steps), word), ""}
	for _, step := range steps {
		lines = append(lines, "· "+step.Title)
		if step.Fix != "" {
			lines = append(lines, "  run: "+step.Fix)
		}
	}
	lines = append(lines, "", registered)
	return styles.WarningBox.Render(strings.Join(lines, "\n"))
}

// buildCommitGatePreCommitScript is the git pre-commit hook the removed pre-commit
// review installed. Steer no longer writes it; a removal compares a hook against it.
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
	cwd, _ := gate.ProjectDir()
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

// commitGateRemoveHarness has no default, so a removal always names its harness.
var commitGateRemoveHarness string

// commitGateRemovePreCommit selects this repository's git pre-commit hook instead.
var commitGateRemovePreCommit bool

var commitGateRemoveCmd = &cobra.Command{
	Use:   "remove",
	Short: "Remove one harness's commit gate from one scope; --harness and --scope are required",
	Long: `remove takes one harness's commit review hooks out of one scope, leaving every
other hook in that file.

--precommit instead removes the git pre-commit hook an earlier Steer installed, for
its removed pre-commit review or its removed verify command, in the repository this
runs in. It removes only a hook that is exactly the one Steer wrote.`,
	Example: `  steer commit-gate remove --harness codex --scope global
  steer commit-gate remove --precommit`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if commitGateRemovePreCommit {
			if commitGateRemoveHarness != "" || cmd.Flags().Changed("scope") {
				return errors.New("--precommit removes this repository's git hook and takes no --harness or --scope")
			}
			message, err := removeCommitGatePreCommit()
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), message)
			return nil
		}
		if commitGateRemoveHarness == "" || !cmd.Flags().Changed("scope") || strings.TrimSpace(scope) == "" {
			return errors.New(commitGateRemoveUsage())
		}
		scope := currentScope()
		target, err := harness.Parse(commitGateRemoveHarness)
		if err != nil {
			return err
		}
		cwd, err := gate.ProjectDir()
		if err != nil {
			return err
		}
		var path string
		var removed bool
		switch target {
		case harness.Codex:
			path, removed, err = codexhooks.RemoveCommitGateAndResolver(scope, cwd)
		case harness.Pi:
			path, removed, err = pihooks.RemoveCommitGateAndResolver(scope, cwd)
		default:
			// Both steer-owned Claude Code entries come down in one write, preserving
			// unrelated PreToolUse and Stop hooks.
			removed, err = claude.RemoveCommitGateAndResolver(scope)
			if err == nil {
				path, err = claude.GetSettingsFile(scope)
			}
		}
		if err != nil {
			return err
		}
		fmt.Println(commitGateRemoveResult(status.HarnessLabel(string(target)), scope, path, removed))
		return nil
	},
}

// removeCommitGatePreCommit deletes this repository's git pre-commit hook when it is
// a hook an earlier Steer wrote, and reports what it did. A hook
// without the marker is not Steer's and stays; a marked hook with other lines stays
// too, because deleting it would drop lines an operator added. A hook outside the
// repository's git directory is refused, as uninstall refuses it: other repositories
// may share that hooks directory, or the work tree tracks it.
func removeCommitGatePreCommit() (string, error) {
	dir, err := gitHooksDir()
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "pre-commit")
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "No git pre-commit hook in this repository; nothing changed.", nil
	}
	if err != nil {
		return "", fmt.Errorf("could not read the pre-commit hook: %w", err)
	}
	text := string(raw)
	if !strings.Contains(text, commitGatePreCommitMarker) && !strings.Contains(text, steerPreCommitMarker) {
		return fmt.Sprintf("The pre-commit hook at %s is not Steer's; nothing changed.", path), nil
	}
	if !generatedPreCommit(text) {
		return "", fmt.Errorf("the pre-commit hook at %s carries Steer's marker but differs from the hook Steer "+
			"wrote, so Steer leaves it; delete its steer lines by hand", path)
	}
	if !insideGitDir(path) {
		return "", fmt.Errorf("the pre-commit hook at %s lies outside this repository's git directory, where "+
			"other repositories may share it or the work tree tracks it, so Steer leaves it; remove it by hand", path)
	}
	if err := os.Remove(path); err != nil {
		return "", fmt.Errorf("could not remove the pre-commit hook: %w", err)
	}
	return "Removed Steer's git pre-commit hook: " + path, nil
}

// legacyPreCommitInvocation is the hook's last command as any Steer wrote it: an
// older Steer added --provider and --model, in that order, each with one word.
var legacyPreCommitInvocation = regexp.MustCompile(
	`^steer commit-gate precommit( --provider [A-Za-z0-9._:/@+=-]+)?( --model [A-Za-z0-9._:/@+=-]+)?$`)

// commitGatePreCommitScript reports whether text is a hook the pre-commit review
// wrote, with or without an older Steer's reviewer flags.
func commitGatePreCommitScript(text string) bool {
	const invocation = "steer commit-gate precommit"
	want := strings.Split(buildCommitGatePreCommitScript(), "\n")
	got := strings.Split(text, "\n")
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if want[i] == invocation {
			if !legacyPreCommitInvocation.MatchString(got[i]) {
				return false
			}
		} else if got[i] != want[i] {
			return false
		}
	}
	return true
}

// commitGateRemoveResult says what a removal changed.
func commitGateRemoveResult(label, scope, path string, removed bool) string {
	if !removed {
		return fmt.Sprintf("No %s commit review hooks in %s scope; nothing changed.", label, scope)
	}
	return fmt.Sprintf("Removed %s commit review hooks from %s", label, path)
}

// commitGateRemoveUsage is the refusal for a removal that names no target. It lists
// what could be removed.
func commitGateRemoveUsage() string {
	var found []string
	cwd, _ := gate.ProjectDir()
	for _, scope := range []string{claude.GlobalScope, claude.ProjectScope} {
		if claude.CommitGateHookArmed(scope).Registered || claude.BackfillHookArming(scope).Registered {
			found = append(found, string(harness.ClaudeCode)+" "+scope)
		}
		if codexhooks.CommitGateArming(scope, cwd).Registered || codexhooks.ResolverArmingRow(scope, cwd).Registered {
			found = append(found, string(harness.Codex)+" "+scope)
		}
		if pihooks.CommitGateArming(scope, cwd).Registered {
			found = append(found, string(harness.Pi)+" "+scope)
		}
	}
	registered := "none"
	if len(found) > 0 {
		registered = strings.Join(found, ", ")
	}
	return "commit-gate remove needs both --harness and --scope\n" +
		"Registered: " + registered + "\n" +
		"Example: steer commit-gate remove --harness codex --scope global"
}

var (
	backfillApply     bool
	backfillIfDue     bool
	backfillQuiet     bool
	backfillReresolve bool
	backfillRejoin    bool
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
  --reresolve  re-examine rows already stamped as a miss (never removes a ledger)
  --rejoin     also re-judge every link the resolver made under the current rules;
               a link with no ledger left to read is kept, others may change or
               become a miss

As a Stop hook it reads the payload on stdin. On a root Stop it first checks that
the reply text holds the ledger of each review sequence the gate released in that
turn, and refuses the Stop once, naming each missing ledger, when one is absent.`,
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
			Rejoin:    backfillRejoin,
		}
		out := cmd.OutOrStdout()
		// A root Stop payload gets the ledger backstop first. Its stdout carries one JSON
		// reply, so the resolver's narration goes to stderr.
		if res := stophook.Check(cfg.LogFile(), hookInput(os.Stdin)); res.RootStop {
			checks.WriteStopReply(out, stopReply(res))
			if res.Block {
				return nil
			}
			out = cmd.ErrOrStderr()
		}
		resolve.RunBackfill(cfg, ledgerHarvests(), opts, out, nil)
		return nil
	},
}

const (
	// maxHookInput bounds the hook payload read; a larger one is not a payload.
	maxHookInput = 16 << 20
	// hookInputWait bounds the wait for stdin to close, so a run from a shell whose
	// stdin stays open does not hang.
	hookInputWait = 2 * time.Second
)

// hookInput reads a hook payload from stdin. It returns nil when stdin is a terminal,
// fails, exceeds maxHookInput, or stays open past hookInputWait.
func hookInput(stdin *os.File) []byte {
	info, err := stdin.Stat()
	if err != nil || info.Mode()&os.ModeCharDevice != 0 {
		return nil
	}
	done := make(chan []byte, 1)
	go func() {
		data, err := io.ReadAll(io.LimitReader(stdin, maxHookInput+1))
		if err != nil || len(data) > maxHookInput {
			data = nil
		}
		done <- data
	}()
	select {
	case data := <-done:
		return data
	case <-time.After(hookInputWait):
		return nil
	}
}

// stopReply renders the backstop's result in the Stop hook reply shape, or nil when it
// has nothing to say.
func stopReply(res stophook.Result) *checks.StopReply {
	reply := checks.StopReply{SystemMessage: res.SystemMessage}
	if res.Block {
		reply.Decision, reply.Reason = "block", res.Reason
	}
	if reply == (checks.StopReply{}) {
		return nil
	}
	return &reply
}

// ledgerHarvests is the set both resolve callers read. It lives in its own package
// so the dashboard's Channels panel starts the same resolve this command does.
func ledgerHarvests() []resolve.Harvest { return harvests.All() }

// effortNote renders the effort beside the model, and nothing when it is unset —
// an unset effort is the provider's own, which Steer cannot name.
func effortNote(effort string) string {
	if strings.TrimSpace(effort) == "" {
		return ""
	}
	return " @ " + effort
}

func init() {
	rootCmd.AddCommand(commitGateCmd)
	commitGateCmd.AddCommand(commitGateReviewCmd, commitGateCodexReviewCmd, commitGatePiReviewCmd, commitGatePreCommitCmd,
		commitGateInstallCmd, commitGateRemoveCmd, commitGateBackfillCmd,
		commitGateTestReviewerCmd, commitGateSubagentStartCmd)

	commitGateBackfillCmd.Flags().BoolVar(&backfillApply, "apply", false, "Write the resolved rows")
	commitGateBackfillCmd.Flags().BoolVar(&backfillIfDue, "if-due", false, "Exit early unless a row is due")
	commitGateBackfillCmd.Flags().BoolVar(&backfillQuiet, "quiet", false, "Silence the report")
	commitGateBackfillCmd.Flags().BoolVar(&backfillReresolve, "reresolve", false, "Re-examine stamped misses")
	commitGateBackfillCmd.Flags().BoolVar(&backfillRejoin, "rejoin", false, "Re-judge resolver links and stamped misses under the current rules")

	commitGateCmd.PersistentFlags().StringVar(&commitGateProvider, "provider", "",
		"Reviewer provider ("+strings.Join(reviewers.ProviderNames(), "|")+"); default claude-code")
	commitGateCmd.PersistentFlags().StringVar(&commitGateModel, "model", "",
		"Reviewer model; default is the provider's shipped pin")
	commitGateCmd.PersistentFlags().StringVar(&commitGateEffort, "effort", "",
		"Reviewer reasoning effort; default is the provider's own")
	commitGateCmd.PersistentFlags().IntVar(&commitGateRuns, "runs", 0,
		"Reviewers in the panel; default 3")

	commitGateInstallCmd.Flags().StringVar(&commitGateHarness, "harness", "claude-code",
		"PreToolUse harness (claude-code|codex|pi)")
	commitGateRemoveCmd.Flags().StringVar(&commitGateRemoveHarness, "harness", "",
		"PreToolUse harness (claude-code|codex|pi), required")
	commitGateRemoveCmd.Flags().BoolVar(&commitGateRemovePreCommit, "precommit", false,
		"Remove this repository's git pre-commit hook from Steer's removed pre-commit review")
}
