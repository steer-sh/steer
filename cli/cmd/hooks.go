package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/guard"
	"github.com/steer-sh/steer-cli/internal/harness"
	"github.com/steer-sh/steer-cli/internal/status"
	"github.com/steer-sh/steer-cli/internal/styles"
	"github.com/steer-sh/steer-cli/internal/utils"
	"github.com/steer-sh/steer-cli/internal/version"
)

var hooksCmd = &cobra.Command{
	Use:   "hooks",
	Short: "Manage the generated master hook",
}

var hooksRebuildCmd = &cobra.Command{
	Use:   "rebuild",
	Short: "Regenerate the master hook from the installed guards and re-register it",
	Long: `rebuild regenerates steer-hooks.py from the guards in the store,
re-registers it in every target harness, and rewrites each harness's managed
rules section from the manifest, as one operation.

A store keeps whatever runtime it was last built with, so a master written by an
older steer records fewer audit fields than the guards it runs deserve. Upgrading
the CLI repairs nothing on its own: nothing rewrites the master until something
mutates the store.

Rebuilding migrates a legacy store to the neutral one, which moves the master's
path. Re-registration is part of this command for that reason: the harness has to
be pointed at the file that was just written.

It is also the repair for an install or a removal that stopped part way through
the harnesses: the manifest names every rule, so the section is rewritten rather
than patched.`,
	Example: `  steer hooks rebuild --scope global
  steer hooks rebuild --scope global --all-harnesses`,
	RunE: func(cmd *cobra.Command, args []string) error {
		targets, err := currentRegistrars()
		if err != nil {
			return err
		}

		scope := currentScope()
		result, err := claude.RebuildMaster(scope, targets...)
		if err != nil {
			return err
		}

		fmt.Println()
		fmt.Println(styles.MainHeader.Render("Rebuild Master Hook"))
		fmt.Println()

		if result.Hooks == 0 {
			utils.Info(fmt.Sprintf("Scope %s holds no guards; the master was removed.", scope))
			for _, target := range result.Targets {
				utils.Info(fmt.Sprintf("%s: deregistered", target.Harness.DisplayName))
			}
			return nil
		}

		utils.Success(fmt.Sprintf("Rebuilt %s from %d guard(s).", result.MasterPath, result.Hooks))
		utils.Info(fmt.Sprintf("Runtime %s | steer %s", result.Stamp.Runtime, result.Stamp.Version))
		if result.Migrated {
			utils.Info(fmt.Sprintf("Store migrated: %s -> %s (the legacy copy is left in place).",
				result.LegacyDir, result.StoreDir))
		}
		for _, target := range result.Targets {
			if target.Previous != "" && target.Previous != target.Path {
				utils.Info(fmt.Sprintf("%s: re-registered %s (was %s)",
					target.Harness.DisplayName, target.Path, target.Previous))
				continue
			}
			utils.Info(fmt.Sprintf("%s: registered %s", target.Harness.DisplayName, target.Path))
		}
		for _, target := range result.Targets {
			if target.CommandChanged && target.Harness.Capabilities.TrustReview {
				utils.Info(fmt.Sprintf("%s: the guard command changed, so re-approve it in %s.",
					target.Harness.DisplayName, target.Harness.DisplayName))
			}
		}
		if result.Stamp.Runtime != guard.RuntimeDigest() || result.Stamp.Version != version.Version {
			utils.Info("The stamp does not match this binary; report it as a bug.")
		}
		printFixtureVerification(result.Verification)
		return nil
	},
}

var hooksSyncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Register the store's existing master and rules in each harness, without regenerating it",
	Long: `sync registers the master a store already holds in every target harness and
writes each harness's managed rules section from the manifest. It writes only
the registrations and sections that differ from the store, and it never writes
the store: the master, the manifest and fixture state stay as they are.

Use it when a store arrived without this machine's registrations — a clone of a
repository whose guards a teammate committed, or a harness installed after the
guards were written. A project store is shared, and rebuild would rewrite its
master with this binary's runtime.

A store with guards must hold a master that embeds exactly those guards. When it
does not, sync refuses and names rebuild, which regenerates the master.`,
	Example: `  steer hooks sync --scope project
  steer hooks sync --scope project --harness codex`,
	RunE: func(cmd *cobra.Command, args []string) error {
		targets, err := currentRegistrars()
		if err != nil {
			return err
		}

		scope := currentScope()
		result, err := claude.SyncStore(scope, targets...)
		if err != nil {
			return err
		}

		fmt.Println()
		fmt.Println(styles.MainHeader.Render("Sync Guard Registrations"))
		fmt.Println()

		if result.Hooks > 0 {
			utils.Info(fmt.Sprintf("Scope %s: %d guard(s) in %s.", scope, result.Hooks, result.MasterPath))
		} else {
			utils.Info(fmt.Sprintf("Scope %s holds no guards; only rules sections are synced.", scope))
		}
		for _, h := range result.Synced {
			utils.Success(fmt.Sprintf("%s: synced", h.DisplayName))
		}
		for _, h := range result.Current {
			utils.Info(fmt.Sprintf("%s: already in sync, nothing written", h.DisplayName))
		}
		for _, m := range status.MasterRuntimes(scope) {
			if m.Stale() && contains(result.Synced, m.Harness) {
				utils.Info(fmt.Sprintf("%s: %s", status.HarnessLabel(m.Harness), status.MasterPhrase(m)))
			}
		}
		return nil
	},
}

// contains reports whether hs holds the harness with this id.
func contains(hs []harness.Harness, id string) bool {
	for _, h := range hs {
		if string(h.ID) == id {
			return true
		}
	}
	return false
}

func init() {
	rootCmd.AddCommand(hooksCmd)
	hooksCmd.AddCommand(hooksRebuildCmd)
	hooksCmd.AddCommand(hooksSyncCmd)
}
