package cmd

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steer-sh/steer-cli/internal/archive"
	"github.com/steer-sh/steer-cli/internal/checks"
	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/styles"
	"github.com/steer-sh/steer-cli/internal/tier"
)

var archiveCmd = &cobra.Command{
	Use:   "archive",
	Short: "List and restore fixes that a removal archived",
	Long: `Every remove command archives the fix it takes out: a guard with its file,
fixtures and manifest entry, an instruction rule or review addendum entry with its
manifest entry, and a check with its declaration. The entry keeps the removal time
and the reason.

Guards, rules and addendum entries are archived in their hook store's archive/;
checks in the repository's .steer/checks/archive/. A project archive is committed
with its store.`,
}

var archiveListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the archive entries of the project and global stores",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		for _, store := range archiveStores() {
			fmt.Fprintf(out, "%s", store.label)
			if store.err != nil {
				fmt.Fprintf(out, ": not readable: %v\n", store.err)
				continue
			}
			entries, err := archive.List(store.dir)
			if err != nil {
				return fmt.Errorf("%s: %w", store.label, err)
			}
			fmt.Fprintf(out, " (%s)\n", store.dir)
			if len(entries) == 0 {
				fmt.Fprintln(out, "  no entries")
				continue
			}
			for _, e := range entries {
				printArchiveEntry(out, e)
			}
		}
		return nil
	},
}

var archiveRestoreCmd = &cobra.Command{
	Use:   "restore <id>",
	Short: "Put an archived fix back through its tier's installer",
	Long: `Restore installs an archived fix again. A guard returns with its file,
manifest metadata and fixtures, and its fixtures run again. A rule or a review
addendum entry returns with its text. A check replays its cases and is declared
only when they all match. The archive entry stays.

--scope picks the hook store. A project restore also reads the repository's
check archive. When one id holds entries of more than one tier, pass --tier;
when the tier still matches more than one entry, pass --entry with a name from
` + "`steer archive list`" + `.

Restore refuses an id its own tier holds now, and a guard file in the store that
no manifest entry names.`,
	Example: `  steer archive restore no-var-hook
  steer archive restore config-path --tier instruction --scope global
  steer archive restore no-count --tier check --entry 20260914T223509.123456789Z-a1b2c3`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		id := args[0]
		kind, _ := cmd.Flags().GetString("tier")
		name, _ := cmd.Flags().GetString("entry")
		if strings.TrimSpace(kind) != "" {
			k, ok := tier.Of(kind)
			if !ok {
				return fmt.Errorf("unknown tier %q: use one of %s", kind, strings.Join(tier.Accepted(), ", "))
			}
			kind = string(k)
		}

		scope := currentScope()
		if kind == string(tier.Check) && scope != claude.ProjectScope {
			return fmt.Errorf("checks are archived in the project check store only; drop --scope %s", scope)
		}
		candidates, checkRoot, err := restoreCandidates(scope, id)
		if err != nil {
			return err
		}
		s, err := archive.Select(candidates, id, kind, strings.TrimSpace(name))
		if err != nil {
			return err
		}

		if s.Tier == string(tier.Check) {
			d, run, err := checks.Restore(checkRoot, s)
			printValidation(cmd, run)
			if err != nil {
				return fmt.Errorf("check %s was not restored, and its archive entry stays: %w", s.ID, err)
			}
			fmt.Fprintln(out, styles.SuccessBox.Render(fmt.Sprintf("Restored check %s\nFrom: %s", d.ID, s.Dir)))
			printOracleWiring(out, checkRoot, d.ID)
			printRestorePointer(out)
			return nil
		}

		targets, err := currentRegistrars()
		if err != nil {
			return err
		}
		result, err := claude.RestoreArchived(scope, s, targets...)
		if result != nil {
			printFixtureVerification(result.Verification)
		}
		if err != nil {
			return err
		}
		fmt.Fprintln(out, styles.SuccessBox.Render(fmt.Sprintf(
			"Restored %s %s\nScope: %s\nHarness: %s\nFrom: %s", s.Tier, s.ID, scope, harnessNames(targets), s.Dir)))
		if s.Tier == string(tier.Review) && scope == claude.ProjectScope {
			fmt.Fprintf(out, "The commit gate reads project entries from HEAD. Commit %s on its own, before the change "+
				"the restored entry is for: a commit that carries both is reviewed without the entry.\n", projectManifestPath())
		}
		printRestorePointer(out)
		return nil
	},
}

type archiveStore struct {
	label string
	dir   string
	err   error
}

// archiveStores names the three stores that hold archives: the project and
// global hook stores and the repository's check store.
func archiveStores() []archiveStore {
	project, projectErr := claude.GetHooksDir(claude.ProjectScope)
	global, globalErr := claude.GetHooksDir(claude.GlobalScope)
	checkStore := archiveStore{label: "Project check store"}
	if root, err := repoRootOf("."); err != nil {
		checkStore.err = err
	} else {
		checkStore.dir = checks.Dir(root)
	}
	return []archiveStore{
		{label: "Project hook store", dir: project, err: projectErr},
		{label: "Global hook store", dir: global, err: globalErr},
		checkStore,
	}
}

// restoreCandidates reads the entries of id that a restore in scope may act on:
// the scope's hook store and, for the project scope inside a repository, the
// check store. It returns the repository root the check store belongs to.
func restoreCandidates(scope, id string) ([]archive.Stored, string, error) {
	hooksDir, err := claude.GetHooksDir(scope)
	if err != nil {
		return nil, "", err
	}
	candidates, err := archive.ForID(hooksDir, id)
	if err != nil {
		return nil, "", err
	}
	if scope != claude.ProjectScope {
		return candidates, "", nil
	}
	root, err := repoRootOf(".")
	if err != nil {
		return candidates, "", nil
	}
	archivedChecks, err := archive.ForID(checks.Dir(root), id)
	if err != nil {
		return nil, "", err
	}
	return append(candidates, archivedChecks...), root, nil
}

func printArchiveEntry(out io.Writer, e archive.Stored) {
	removed := e.Removed
	if t := e.RemovedAt(); !t.IsZero() {
		removed = t.Local().Format("2006-01-02 15:04")
	}
	fmt.Fprintf(out, "  %s  %s  %s  removed %s\n", e.Tier, e.ID, e.Scope, removed)
	fmt.Fprintf(out, "    entry: %s\n", e.Name())
	if e.Reason != "" {
		fmt.Fprintf(out, "    reason: %s\n", e.Reason)
	}
	if len(e.Missing) > 0 {
		fmt.Fprintf(out, "    missing: %s\n", strings.Join(e.Missing, ", "))
	}
}

// printOracleWiring says when a restored check is declared but the oracle does not
// run it. A removal that took the check's own `check exec <id>` line out of
// check.sh leaves the declaration with nothing to execute it, and a declared check
// that never runs is not a passing check.
func printOracleWiring(out io.Writer, root, id string) {
	if root == "" || checks.OracleRunsEveryCheck(root) || checks.OracleNamesCheck(root, id) {
		return
	}
	fmt.Fprintf(out, "%s runs neither `%s` nor `check exec %s`, so %s is declared and never executes. "+
		"Add one of those lines to %s.\n", checks.OracleName, checks.ExecInvocation, id, id, checks.OracleName)
}

func printRestorePointer(out io.Writer) {
	fmt.Fprintln(out, "A record that measured this fix now shows its removal. "+
		"`steer fix register --supersedes <record-id>` starts a clean window.")
}

func init() {
	rootCmd.AddCommand(archiveCmd)
	archiveCmd.AddCommand(archiveListCmd, archiveRestoreCmd)
	archiveRestoreCmd.Flags().String("tier", "", "the tier to restore when one id holds entries of more than one")
	archiveRestoreCmd.Flags().String("entry", "", "the entry to restore when the tier holds more than one, as `steer archive list` names it")
}
