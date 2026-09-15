package cmd

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/gate"
	"github.com/steer-sh/steer-cli/internal/hookstore"
	"github.com/steer-sh/steer-cli/internal/status"
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
			fmt.Printf("The commit gate reads project entries from HEAD. Commit %s on its own, before the change "+
				"this entry is for: a commit that carries both is reviewed without the entry.\n", projectManifestPath())
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
		onDisk := map[string]claude.ManagedAddendumEntry{}
		for _, e := range entries {
			onDisk[e.ID] = e
		}
		head := gate.LoadAddendum(gate.Toplevel("."))
		var rows []status.AddendumInfo
		for _, row := range status.AddendumInventory(&head) {
			if row.Scope == scope && row.ID != "" {
				rows = append(rows, row)
			}
		}
		if len(rows) == 0 {
			fmt.Printf("No review addendum entries in the %s store.\n", scope)
			return nil
		}
		for _, row := range rows {
			text := row.Text
			if text == "" {
				text = row.HeadText
			}
			fmt.Printf("%s  %s\n", row.ID, text)
			line := "  " + status.AddendumPhrase(row)
			if e, ok := onDisk[row.ID]; ok {
				line = fmt.Sprintf("  text_hash %s  created %s  %s",
					claude.HashRule(e.Text)[:16], e.Created, status.AddendumPhrase(row))
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
		reason, _ := cmd.Flags().GetString("reason")
		result, err := claude.RemoveAddendum(scope, args[0], strings.TrimSpace(reason))
		if err != nil {
			return err
		}
		fmt.Printf("Removed addendum entry %s from the %s store: %s\n", args[0], scope, result.Text)
		fmt.Printf("Archived: %s\n", result.ArchivePath)
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

func init() {
	commitGateCmd.AddCommand(commitGateAddendumCmd)
	commitGateAddendumCmd.AddCommand(commitGateAddendumAddCmd, commitGateAddendumListCmd, commitGateAddendumRemoveCmd)
	commitGateAddendumAddCmd.Flags().StringVar(&addendumText, "text", "", "the entry's text (required)")
	_ = commitGateAddendumAddCmd.MarkFlagRequired("text")
	commitGateAddendumRemoveCmd.Flags().String("reason", "", "why the entry is removed; the archive entry keeps it")
}
