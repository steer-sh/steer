package cmd

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/gate"
	"github.com/steer-sh/steer-cli/internal/hookstore"
)

var addendumText string

var commitGateAddendumCmd = &cobra.Command{
	Use:   "addendum",
	Short: "Manage the review addendum — entries the commit gate adds to its stock review prompt",
	Long: `A review addendum entry is text the commit gate adds after its stock review
prompt, most often a recurring false positive the reviewer should stop raising. The
stock prompt is fixed; entries only add to it.

Entries live in the shared store's steer-manifest.json. Global entries apply to every
repository. Project entries apply to one repository, and the gate reads them from the
committed HEAD, so a commit cannot change the review it is under: a project entry
takes effect once the manifest is committed.

A change to the addendum changes the reviewer, so the ledger's instrument records
its digest beside the prompt's.`,
}

var commitGateAddendumAddCmd = &cobra.Command{
	Use:     "add <id> --text <text>",
	Short:   "Write a review addendum entry into this scope's store",
	Example: `  steer commit-gate addendum add no-style-nits --text "naming and formatting preferences are not findings"`,
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		scope := currentScope()
		if existing, err := claude.ListAddendum(scope); err == nil && len(existing) > 0 {
			fmt.Printf("Addendum entries already in the %s store:\n", scope)
			for _, e := range existing {
				fmt.Printf("  %s  %s\n", e.ID, e.Text)
			}
			fmt.Println()
		}
		result, err := claude.InstallAddendum(scope, args[0], addendumText)
		if err != nil {
			return err
		}
		verb := "Added"
		if result.Overwritten {
			verb = "Reworded"
		}
		fmt.Printf("%s addendum entry %s in the %s store.\n", verb, result.ID, scope)
		if result.Overwritten {
			fmt.Printf("  was: %s\n", result.PreviousText)
		}
		if scope == claude.ProjectScope {
			fmt.Printf("The commit gate reads project entries from HEAD. Commit %s for this entry to take effect.\n",
				projectManifestPath())
		}
		return nil
	},
}

var commitGateAddendumListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the review addendum entries in this scope's store",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		scope := currentScope()
		entries, err := claude.ListAddendum(scope)
		if err != nil {
			return err
		}
		if len(entries) == 0 {
			fmt.Printf("No review addendum entries in the %s store.\n", scope)
			return nil
		}
		var atHead map[string]string
		if scope == claude.ProjectScope {
			atHead = projectEntriesAtHead()
		}
		for _, e := range entries {
			fmt.Printf("%s  %s\n", e.ID, e.Text)
			line := fmt.Sprintf("  text_hash %s  created %s", claude.HashRule(e.Text)[:16], e.Created)
			if scope == claude.ProjectScope {
				line += "  " + headState(atHead, e.ID, e.Text)
			}
			fmt.Println(line)
		}
		return nil
	},
}

var commitGateAddendumRemoveCmd = &cobra.Command{
	Use:   "remove <id>",
	Short: "Delete a review addendum entry from this scope's store",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		scope := currentScope()
		text, err := claude.RemoveAddendum(scope, args[0])
		if err != nil {
			return err
		}
		fmt.Printf("Removed addendum entry %s from the %s store: %s\n", args[0], scope, text)
		if scope == claude.ProjectScope {
			fmt.Printf("The commit gate reads project entries from HEAD. Commit %s for the removal to take effect.\n",
				projectManifestPath())
		}
		return nil
	},
}

func projectManifestPath() string {
	dir, err := hookstore.Resolve(claude.ProjectScope)
	if err != nil {
		return hookstore.ManifestName
	}
	return filepath.Join(dir, hookstore.ManifestName)
}

// projectEntriesAtHead is the project entries the commit gate would read now, by id.
func projectEntriesAtHead() map[string]string {
	out := map[string]string{}
	for _, e := range gate.LoadAddendum(gate.Toplevel(".")).Entries {
		if e.Scope == claude.ProjectScope {
			out[e.ID] = e.Text
		}
	}
	return out
}

func headState(atHead map[string]string, id, text string) string {
	committed, ok := atHead[id]
	switch {
	case !ok:
		return "not at HEAD — the gate does not read it yet"
	case committed != strings.Join(strings.Fields(text), " "):
		return "differs from HEAD — the gate reads the committed text"
	default:
		return "live at HEAD"
	}
}

func init() {
	commitGateCmd.AddCommand(commitGateAddendumCmd)
	commitGateAddendumCmd.AddCommand(commitGateAddendumAddCmd, commitGateAddendumListCmd, commitGateAddendumRemoveCmd)
	commitGateAddendumAddCmd.Flags().StringVar(&addendumText, "text", "", "the entry's text (required)")
	_ = commitGateAddendumAddCmd.MarkFlagRequired("text")
}
