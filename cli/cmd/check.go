package cmd

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steer-sh/steer-cli/internal/checks"
	"github.com/steer-sh/steer-cli/internal/styles"
)

var checkCmd = &cobra.Command{
	Use:   "check",
	Short: "Manage repository checks — executable tests the turn-end oracle runs",
	Long: `A repository check is an executable test of repository content. The
repository's check.sh runs every blocking check; Steer manages individual checks
inside it, declared in .steer/checks/manifest.json.

Read the authoring guide before writing a check: steer check guide`,
}

var checkGuideCmd = &cobra.Command{
	Use:   "guide",
	Short: "Print the instructions for authoring repository checks",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Fprint(cmd.OutOrStdout(), checks.Guide())
		return nil
	},
}

var checkInitCmd = &cobra.Command{
	Use:   "init [repo]",
	Short: "Create the repository's check manifest, keeping an existing one",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		dir := "."
		if len(args) == 1 {
			dir = args[0]
		}
		root, err := repoRootOf(dir)
		if err != nil {
			return err
		}
		m, created, err := checks.Init(root)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, checks.ManifestPath(root))
		if created {
			fmt.Fprintln(cmd.OutOrStdout(), styles.SuccessBox.Render(fmt.Sprintf(
				"Created %s\nRepository: %s", rel, root)))
			return nil
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s already exists in %s with %d checks; left unchanged.\n",
			rel, root, len(m.Checks))
		return nil
	},
}

// repoRootOf returns the top level of the Git work tree containing dir.
func repoRootOf(dir string) (string, error) {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s is not inside a git repository: %s", dir, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func init() {
	rootCmd.AddCommand(checkCmd)
	checkCmd.AddCommand(checkGuideCmd, checkInitCmd)
}
