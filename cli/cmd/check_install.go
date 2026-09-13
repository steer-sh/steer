package cmd

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steer-sh/steer-cli/internal/checks"
	"github.com/steer-sh/steer-cli/internal/checkscope"
	"github.com/steer-sh/steer-cli/internal/claude"
	codexhooks "github.com/steer-sh/steer-cli/internal/codex"
	"github.com/steer-sh/steer-cli/internal/harness"
	"github.com/steer-sh/steer-cli/internal/status"
	"github.com/steer-sh/steer-cli/internal/steerbin"
	"github.com/steer-sh/steer-cli/internal/styles"
)

// runnerHarnesses are the harnesses whose root Stop event can block on check.sh.
var runnerHarnesses = []harness.ID{harness.ClaudeCode, harness.Codex}

// blockingHarness parses a --harness value for the Stop runner.
func blockingHarness(name string) (harness.ID, error) {
	target, err := harness.Parse(name)
	if err != nil {
		return harness.Unknown, err
	}
	if target != harness.ClaudeCode && target != harness.Codex {
		return harness.Unknown, fmt.Errorf("blocking checks support claude-code and codex, not %s", target)
	}
	return target, nil
}

func checkHookCommand(target harness.ID) string {
	return steerHookCommand("check hook --harness " + string(target))
}

// checkRunnerArming reads one harness's Stop runner registration at one scope,
// resolving the project directory Codex reads.
func checkRunnerArming(target harness.ID, scope string) (checkscope.Arming, error) {
	cwd := ""
	if target == harness.Codex {
		dir, err := codexProjectDir()
		if err != nil && scope != claude.GlobalScope {
			return checkscope.Arming{}, err
		}
		cwd = dir
	}
	return checkscope.RunnerArming(target, scope, cwd)
}

var checkInstallCmd = &cobra.Command{
	Use:   "install --harness <claude-code|codex>",
	Short: "Register the Stop runner so adopted repositories run check.sh at turn end",
	Long: `install registers ` + "`steer check hook --harness <id>`" + ` on the harness's root Stop
event at --scope, beside every other Stop hook. Its timeout exceeds the run
deadline, so the harness does not stop the hook while it records the run.

A registration authorizes nothing: check.sh runs only in a repository adopted on
this machine (steer check adopt). Pi is not supported.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		name, _ := cmd.Flags().GetString("harness")
		target, err := blockingHarness(name)
		if err != nil {
			return err
		}
		scope := currentScope()
		arming, err := registerCheckRunner(target, scope)
		if err != nil {
			return err
		}
		entry, _ := harness.Get(target)
		registered := fmt.Sprintf("Registered: the %s Stop runner on root Stop\nScope: %s\n"+
			"Timeout: %ds (run deadline %ds + time to record)\nCommand: %s\nFile: %s",
			entry.DisplayName, scope, arming.Timeout, int(checks.DefaultExecTimeout.Seconds()), arming.Command, arming.Path)
		fmt.Fprintln(cmd.OutOrStdout())
		fmt.Fprintln(cmd.OutOrStdout(), renderReadinessBox(checkInstallSteps(target, arming),
			"Ready — the next root Stop in an adopted repository runs check.sh.",
			"Not running yet — %d %s before the next Stop runs check.sh.", registered))
		return nil
	},
}

// registerCheckRunner writes the Stop runner for one harness at one scope and
// returns the registration it reads back.
func registerCheckRunner(target harness.ID, scope string) (checkscope.Arming, error) {
	command := checkHookCommand(target)
	timeout := checks.RegisteredHookTimeout()
	var err error
	if target == harness.Codex {
		cwd, cwdErr := codexProjectDir()
		if cwdErr != nil {
			return checkscope.Arming{}, cwdErr
		}
		_, err = codexhooks.RegisterCheckHook(scope, cwd, command, timeout)
	} else {
		err = claude.RegisterCheckHook(scope, command, timeout)
	}
	if err != nil {
		return checkscope.Arming{}, err
	}
	arming, err := checkRunnerArming(target, scope)
	if err != nil {
		return arming, err
	}
	if !arming.Registered {
		return arming, fmt.Errorf("wrote the Stop runner to %s, but reading it back found none", arming.Path)
	}
	return arming, nil
}

// checkInstallSteps is what still stands between this registration and a Stop
// that runs check.sh: a command that resolves, a harness that trusts the hook,
// and, when the current repository has a check.sh, its adoption.
func checkInstallSteps(target harness.ID, arming checkscope.Arming) []status.Step {
	var steps []status.Step
	if step, ok := status.CommandStep(steerbin.Resolve(arming.Command)); ok {
		steps = append(steps, step)
	}
	if entry, _ := harness.Get(target); entry.Capabilities.TrustReview {
		if step, ok := status.TrustStep(string(target), status.TrustUnknown, arming.Path); ok {
			steps = append(steps, step)
		}
	}
	if root, err := repoRootOf("."); err == nil {
		if _, err := os.Stat(checks.OraclePath(root)); err != nil {
			return steps
		}
		if identity, err := checks.RepoIdentity(root); err == nil {
			if _, adopted, err := checks.AdoptionOf(identity); err == nil && !adopted {
				steps = append(steps, status.Step{
					Title: "this repository is not adopted on this machine, so its check.sh does not run",
					Fix:   "steer check adopt",
				})
			}
		}
	}
	return steps
}

var checkUninstallCmd = &cobra.Command{
	Use:   "uninstall --harness <claude-code|codex>",
	Short: "Remove the Stop runner registration, keeping adoptions and check evidence",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		name, _ := cmd.Flags().GetString("harness")
		target, err := blockingHarness(name)
		if err != nil {
			return err
		}
		scope := currentScope()
		var path string
		var removed bool
		if target == harness.Codex {
			cwd, cwdErr := codexProjectDir()
			if cwdErr != nil {
				return cwdErr
			}
			path, removed, err = codexhooks.RemoveCheckHook(scope, cwd)
		} else {
			path, _ = claude.GetSettingsFile(scope)
			removed, err = claude.RemoveCheckHook(scope)
		}
		if err != nil {
			return err
		}
		entry, _ := harness.Get(target)
		out := cmd.OutOrStdout()
		if !removed {
			fmt.Fprintf(out, "No %s Stop runner is registered at %s scope (%s).\n", entry.DisplayName, scope, path)
			return nil
		}
		fmt.Fprintln(out)
		fmt.Fprintln(out, styles.SuccessBox.Render(fmt.Sprintf(
			"Removed the %s Stop runner\nScope: %s\nFile: %s\nKept: adoptions and check evidence",
			entry.DisplayName, scope, path)))
		return nil
	},
}

// printRunnerWiring prints where the Stop runner is registered on this machine,
// naming a command that does not resolve and a timeout shorter than a run needs.
func printRunnerWiring(out io.Writer) {
	var parts []string
	for _, target := range runnerHarnesses {
		for _, scope := range []string{claude.ProjectScope, claude.GlobalScope} {
			a, err := checkRunnerArming(target, scope)
			if err != nil || !a.Registered {
				continue
			}
			phrase := fmt.Sprintf("%s, %s scope", status.HarnessLabel(string(target)), scope)
			if steerbin.Resolve(a.Command) == steerbin.Missing {
				phrase += " (the command does not resolve: steer bin install)"
			}
			if a.Timeout > 0 && a.Timeout < checks.RegisteredHookTimeout() {
				phrase += fmt.Sprintf(" (timeout %ds is shorter than a run: steer check install)", a.Timeout)
			}
			parts = append(parts, phrase)
		}
	}
	if len(parts) == 0 {
		fmt.Fprintln(out, "Stop runner: not registered (steer check install --harness claude-code|codex)")
		return
	}
	fmt.Fprintf(out, "Stop runner: %s\n", strings.Join(parts, "; "))
}

// relinkCheckRunners repoints this scope's Stop runner registrations at the
// stable executable, keeping each timeout, and reports how many changed.
func relinkCheckRunners(scope, path string) (int, error) {
	rewritten := 0
	if a, err := claude.CheckHookArming(scope); err == nil {
		if next, ok := relinked(a.Registered, a.Command, path); ok {
			if err := claude.RegisterCheckHook(scope, next, a.Timeout); err != nil {
				return rewritten, err
			}
			reportRelink("claude-code", scope, "check runner", next)
			rewritten++
		}
	}
	cwd, err := codexProjectDir()
	if err != nil && scope != claude.GlobalScope {
		return rewritten, nil
	}
	a := codexhooks.CheckHookArming(scope, cwd)
	if next, ok := relinked(a.Registered, a.Command, path); ok {
		if _, err := codexhooks.RegisterCheckHook(scope, cwd, next, a.Timeout); err != nil {
			return rewritten, err
		}
		reportRelink("codex", scope, "check runner", next)
		rewritten++
	}
	return rewritten, nil
}

func init() {
	checkCmd.AddCommand(checkInstallCmd, checkUninstallCmd)
	for _, c := range []*cobra.Command{checkInstallCmd, checkUninstallCmd} {
		c.Flags().String("harness", "", "the harness to register in: claude-code or codex")
		_ = c.MarkFlagRequired("harness")
	}
}
