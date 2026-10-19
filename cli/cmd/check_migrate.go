package cmd

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/steer-sh/steer-cli/internal/checks"
	"github.com/steer-sh/steer-cli/internal/checkscope"
	"github.com/steer-sh/steer-cli/internal/claude"
	codexhooks "github.com/steer-sh/steer-cli/internal/codex"
	"github.com/steer-sh/steer-cli/internal/harness"
	"github.com/steer-sh/steer-cli/internal/status"
	"github.com/steer-sh/steer-cli/internal/styles"
)

// What migration found for one approval in the personal gate's list.
const (
	approvalImport   = "import"
	approvalAdopted  = "adopted"
	approvalNoRepo   = "no repository"
	approvalNoOracle = "no check.sh"
	approvalSkipped  = "not selected"
)

// migrationApproval is one line of the personal approval list and what this
// machine's adoption store says about the repository it names.
type migrationApproval struct {
	Line  string
	Root  string
	State string
}

// pending reports an approval the personal gate covers and this tier does not,
// so replacing the hook would leave that repository ungated.
func (a migrationApproval) pending() bool {
	return a.State == approvalImport || a.State == approvalSkipped
}

// migrationGate is one registration of the personal Stop hook.
type migrationGate struct {
	Harness harness.ID
	Scope   string
	Path    string
	Command string
}

var checkMigrateCmd = &cobra.Command{
	Use:   "migrate [repo...]",
	Short: "Import personal check-gate approvals and replace its Stop hook",
	Long: `Reads the personal gate's approval list and its Stop hook registrations, and
reports what moving them into this tier would do. Nothing changes without
--apply.

With --apply, each named approval is adopted the way ` + "`steer check adopt`" + ` adopts
it: its declared checks validate and check.sh runs in isolation first. The
personal Stop hook is then removed where every approved repository with a
check.sh is adopted, and the Steer runner registered in its place; unrelated
hooks and the approval list itself are left alone.

With no arguments every approval is imported. Name repositories to import a
subset; the personal hook then stays, because the rest still depend on it.`,
	Example: `  steer check migrate
  steer check migrate --apply
  steer check migrate ~/src/one ~/src/two --apply`,
	RunE: func(cmd *cobra.Command, args []string) error {
		apply, _ := cmd.Flags().GetBool("apply")
		timeout, _ := cmd.Flags().GetDuration("timeout")
		out := cmd.OutOrStdout()

		approvals, err := migrationApprovals(args)
		if err != nil {
			return err
		}
		listPath, err := checks.PersonalApprovalsPath()
		if err != nil {
			return err
		}
		gates := personalGates()

		if len(approvals) == 0 {
			fmt.Fprintf(out, "No approvals in %s.\n", listPath)
		} else {
			fmt.Fprintf(out, "Approvals in %s:\n", listPath)
			for _, a := range approvals {
				fmt.Fprintf(out, "  %-14s %s\n", a.State, a.Line)
			}
		}
		if len(gates) == 0 {
			fmt.Fprintf(out, "No %s Stop hook is registered on this machine.\n", checks.PersonalGateScript)
		} else {
			fmt.Fprintln(out, "Personal Stop hook:")
			for _, g := range gates {
				fmt.Fprintf(out, "  %s, %s scope: %s (%s)\n",
					status.HarnessLabel(string(g.Harness)), g.Scope, g.Command, g.Path)
			}
		}
		if !apply {
			fmt.Fprintln(out, "\nPreview only; nothing was changed. Run `steer check migrate --apply` to import and replace.")
			return nil
		}
		return applyMigration(cmd, approvals, gates, timeout)
	},
}

// applyMigration adopts each approval it was asked to import, then replaces the
// personal hook wherever no approved repository still depends on it.
func applyMigration(cmd *cobra.Command, approvals []migrationApproval, gates []migrationGate, timeout time.Duration) error {
	out := cmd.OutOrStdout()
	imported := 0
	for i, a := range approvals {
		if a.State != approvalImport {
			continue
		}
		fmt.Fprintf(out, "\nImporting %s\n", a.Root)
		res, err := checks.Adopt(a.Root, checks.AdoptOptions{
			Timeout: timeout, Stdout: os.Stdout, Stderr: os.Stderr, SteerBin: steerBinForOracle(),
		})
		printAdoption(cmd, a.Root, res)
		if err != nil {
			fmt.Fprintf(out, "Not imported: %v\n", err)
			continue
		}
		approvals[i].State = approvalAdopted
		imported++
	}

	var pending []string
	for _, a := range approvals {
		if a.pending() {
			pending = append(pending, a.Line)
		}
	}
	replaced := 0
	for _, g := range gates {
		label := fmt.Sprintf("%s, %s scope", status.HarnessLabel(string(g.Harness)), g.Scope)
		if len(pending) > 0 {
			fmt.Fprintf(out, "Kept the personal hook (%s): %d approved %s not adopted here.\n",
				label, len(pending), plural(len(pending), "repository is", "repositories are"))
			continue
		}
		arming, err := replacePersonalGate(g.Harness, g.Scope)
		if err != nil {
			return err
		}
		if !arming.Registered {
			return fmt.Errorf("wrote the Stop runner to %s, but reading it back found none", arming.Path)
		}
		if _, still := personalGateAt(g.Harness, g.Scope); still {
			return fmt.Errorf("the %s personal hook at %s is still registered after removing it", label, g.Path)
		}
		replaced++
		fmt.Fprintf(out, "Replaced the personal hook (%s) with %s\n", label, arming.Command)
	}

	if imported == 0 && replaced == 0 {
		fmt.Fprintln(out, "\nNothing to migrate.")
		return nil
	}
	fmt.Fprintln(out, styles.SuccessBox.Render(fmt.Sprintf(
		"Imported %d %s and replaced %d personal Stop %s\nKept: the approval list, the personal hook script, and every unrelated hook",
		imported, plural(imported, "approval", "approvals"), replaced, plural(replaced, "hook", "hooks"))))
	return nil
}

// migrationApprovals reads the personal approval list and classifies each entry.
// With selected non-empty, only the repositories it names are imported.
func migrationApprovals(selected []string) ([]migrationApproval, error) {
	lines, err := checks.PersonalApprovals()
	if err != nil {
		return nil, err
	}
	chosen, err := selectedIdentities(selected)
	if err != nil {
		return nil, err
	}
	approvals := make([]migrationApproval, 0, len(lines))
	for _, line := range lines {
		a := migrationApproval{Line: line, State: approvalNoRepo}
		if root, err := repoRootOf(line); err == nil {
			a.Root = root
			identity, err := checks.RepoIdentity(root)
			if err != nil {
				return nil, err
			}
			_, isAdopted, err := checks.AdoptionOf(identity)
			if err != nil {
				return nil, err
			}
			_, oracleErr := os.Stat(checks.OraclePath(identity))
			switch {
			case isAdopted:
				a.State = approvalAdopted
			case oracleErr != nil:
				a.State = approvalNoOracle
			case chosen != nil && !chosen[identity]:
				a.State = approvalSkipped
			default:
				a.State = approvalImport
			}
		}
		approvals = append(approvals, a)
	}
	return approvals, nil
}

// selectedIdentities resolves the repositories named on the command line. It
// returns nil when none were named, which selects every approval.
func selectedIdentities(selected []string) (map[string]bool, error) {
	if len(selected) == 0 {
		return nil, nil
	}
	chosen := map[string]bool{}
	for _, dir := range selected {
		identity, err := adoptionIdentity(dir)
		if err != nil {
			return nil, err
		}
		chosen[identity] = true
	}
	return chosen, nil
}

// personalGates reports every registration of the personal Stop hook this
// machine's harnesses carry.
func personalGates() []migrationGate {
	var gates []migrationGate
	for _, target := range runnerHarnesses {
		for _, scope := range []string{claude.ProjectScope, claude.GlobalScope} {
			if g, ok := personalGateAt(target, scope); ok {
				gates = append(gates, g)
			}
		}
	}
	return gates
}

func personalGateAt(target harness.ID, scope string) (migrationGate, bool) {
	g := migrationGate{Harness: target, Scope: scope}
	if target == harness.Codex {
		cwd, err := codexProjectDir()
		if err != nil && scope != claude.GlobalScope {
			return g, false
		}
		a := codexhooks.StopHookMatching(scope, cwd, checks.IsPersonalGateCommand)
		g.Path, g.Command = a.Path, a.Command
		return g, a.Registered
	}
	a, err := claude.StopHookMatching(scope, checks.IsPersonalGateCommand)
	if err != nil {
		return g, false
	}
	g.Path, _ = claude.GetSettingsFile(scope)
	g.Command = a.Command
	return g, a.Registered
}

// replacePersonalGate registers the Stop runner and removes the personal hook. Codex
// keys a hook's approval by its position, so there both happen in one write that
// puts the runner in the first entry a runner or the personal hook holds alone.
func replacePersonalGate(target harness.ID, scope string) (checkscope.Arming, error) {
	if target == harness.Codex {
		cwd, err := codexProjectDir()
		if err != nil {
			return checkscope.Arming{}, err
		}
		if _, err := codexhooks.ReplaceStopHook(scope, cwd, checkHookCommand(target),
			checks.RegisteredHookTimeout(), checks.IsPersonalGateCommand); err != nil {
			return checkscope.Arming{}, err
		}
		return checkRunnerArming(target, scope)
	}
	if err := claude.RegisterCheckHook(scope, checkHookCommand(target), checks.RegisteredHookTimeout()); err != nil {
		return checkscope.Arming{}, err
	}
	if _, err := claude.RemoveStopHooksMatching(scope, checks.IsPersonalGateCommand); err != nil {
		return checkscope.Arming{}, err
	}
	return checkRunnerArming(target, scope)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func init() {
	checkCmd.AddCommand(checkMigrateCmd)
	checkMigrateCmd.Flags().Bool("apply", false, "make the changes; without it the command only reports them")
	checkMigrateCmd.Flags().Duration("timeout", checks.DefaultExecTimeout, "total bound on each isolated check.sh run")
}
