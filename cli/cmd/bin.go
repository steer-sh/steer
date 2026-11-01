package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/steer-sh/steer-cli/internal/claude"
	codexhooks "github.com/steer-sh/steer-cli/internal/codex"
	"github.com/steer-sh/steer-cli/internal/gate"
	pihooks "github.com/steer-sh/steer-cli/internal/pi"
	"github.com/steer-sh/steer-cli/internal/status"
	"github.com/steer-sh/steer-cli/internal/steerbin"
	"github.com/steer-sh/steer-cli/internal/styles"
	"github.com/steer-sh/steer-cli/internal/utils"
)

// steerHookCommand renders a hook command naming the stable executable, placing
// that executable first. A failure falls back to the bare word and says so: a
// registration resolved on PATH beats no registration at all.
func steerHookCommand(args string) string {
	command, err := steerbin.HookCommand(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "steer: could not install the stable executable, "+
			"registering the PATH form instead:", err)
	}
	return command
}

var binCmd = &cobra.Command{
	Use:   "bin",
	Short: "Manage the stable executable hook registrations name",
}

var binInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Place the running binary at ~/.steer/bin/steer and repoint every registration",
	Long: `install copies the running binary to ~/.steer/bin/steer and rewrites every
steer-owned hook registration to name that absolute path.

A registration naming the bare word "steer" is resolved on PATH at hook time.
Neither harness reports a command it cannot resolve: the tool call runs, the hook
is skipped, and nothing says so. The gate reads as armed while the commits land
unreviewed.

The copy and the re-registration are one operation, because a path written into a
config is stranded the moment the file it names moves.

A guard master embeds the runtime it was built with, so install then rebuilds a
global master whose runtime differs from this binary's. A project master is
committed and shared: install names its rebuild command and leaves the file as
it is.

After a plugin update, run install from the updated plugin's launcher; the
plugin update alone changes neither the stable executable nor any master.`,
	Example: `  steer bin install
  ~/.claude/plugins/cache/<marketplace>/steer/<version>/scripts/steer bin install`,
	RunE: func(cmd *cobra.Command, args []string) error {
		path, err := steerbin.Ensure()
		if err != nil {
			return err
		}
		fmt.Println()
		fmt.Println(styles.MainHeader.Render("Stable Executable"))
		fmt.Println()
		utils.Success("Installed " + path)

		utils.Info(versionLine())

		rewritten, err := repointRegistrations(path)
		if err != nil {
			return err
		}
		if rewritten == 0 {
			utils.Info("No registration needed rewriting.")
		} else {
			utils.Info(fmt.Sprintf("Repointed %d registration(s) at the stable executable.", rewritten))
		}
		return rebuildStaleMasters()
	},
}

// rebuildStaleMasters rebuilds the global guard master when its runtime is not
// this binary's, and names the rebuild of a stale project master without running
// it, because that master is committed for the whole repository.
func rebuildStaleMasters() error {
	for _, s := range status.StaleScopes(status.MasterRuntimes(claude.GlobalScope, claude.ProjectScope)) {
		if s != claude.GlobalScope {
			utils.Info(fmt.Sprintf("The %s guard master runs an older runtime and is committed; "+
				"to rebuild it for the repository, run: %s", s, status.RebuildCommand(s)))
			continue
		}
		targets, err := currentRegistrars()
		if err != nil {
			return err
		}
		result, err := claude.RebuildMaster(s, targets...)
		if err != nil {
			return fmt.Errorf("rebuild the %s guard master: %w", s, err)
		}
		utils.Info(fmt.Sprintf("The %s guard master ran an older runtime; rebuilt it:", s))
		printRebuild(s, result)
	}
	return nil
}

// relinkClaude repoints this scope's Claude Code registrations, reporting how many
// changed. A registration already naming the stable path is left untouched, and one
// naming a command that is not steer's own is never rewritten.
func relinkClaude(scope, path string) (int, error) {
	rewritten := 0
	gate := claude.CommitGateHookArmed(scope)
	if next, ok := relinked(gate.Registered, gate.Command, path); ok {
		if err := claude.RegisterCommitGateHook(scope, next, gate.Timeout); err != nil {
			return rewritten, err
		}
		reportRelink("claude-code", scope, "commit gate", next)
		rewritten++
	}
	resolver := claude.BackfillHookArming(scope)
	if next, ok := relinked(resolver.Registered, resolver.Command, path); ok {
		if err := claude.RegisterBackfillHook(scope, next, resolver.Timeout); err != nil {
			return rewritten, err
		}
		reportRelink("claude-code", scope, "resolver", next)
		rewritten++
	}
	stale, err := claude.MasterCommandStale(scope)
	if err != nil {
		return rewritten, err
	}
	if stale {
		if err := claude.RegisterMasterHook(scope); err != nil {
			return rewritten, err
		}
		command, err := claude.GetMasterHookCommand(scope)
		if err != nil {
			return rewritten, err
		}
		reportRelink("claude-code", scope, "guard master", command)
		rewritten++
	}
	return rewritten, nil
}

// relinkCodex repoints this scope's Codex registrations. The gate and the resolver
// are written together, because Codex's writer arms them in one save.
func relinkCodex(scope, path string) (int, error) {
	// Only the project scope needs a working directory: the global registration
	// lives under CODEX_HOME. So an unreadable cwd skips project scope alone.
	cwd, err := gate.ProjectDir()
	if err != nil && scope != claude.GlobalScope {
		return 0, nil
	}
	gate := codexhooks.CommitGateArming(scope, cwd)
	resolver := codexhooks.ResolverArmingRow(scope, cwd)
	if !gate.Registered {
		// Codex's writer arms the gate and the resolver together and refuses a save
		// with no gate command, so a resolver left behind by a removed gate cannot be
		// repaired here. Naming it beats rewriting it into a state steer never writes.
		if _, changed := relinked(resolver.Registered, resolver.Command, path); changed {
			utils.Info(fmt.Sprintf("codex, %s scope: the resolver is registered without a gate; "+
				"run: steer commit-gate install --harness codex --scope %s", scope, scope))
		}
		return 0, nil
	}
	nextGate, gateChanged := relinked(gate.Registered, gate.Command, path)
	nextResolver, resolverChanged := relinked(resolver.Registered, resolver.Command, path)
	if !gateChanged && !resolverChanged {
		return 0, nil
	}
	if !gateChanged {
		nextGate = gate.Command
	}
	if !resolverChanged {
		nextResolver = resolver.Command
	}
	if _, err := codexhooks.RegisterCommitGateAndResolver(scope, cwd,
		nextGate, gate.Timeout, nextResolver, resolver.Timeout); err != nil {
		return 0, err
	}
	rewritten := 0
	if gateChanged {
		reportRelink("codex", scope, "commit gate", nextGate)
		rewritten++
	}
	if resolverChanged {
		reportRelink("codex", scope, "resolver", nextResolver)
		rewritten++
	}
	return rewritten, nil
}

// relinkPi repoints both commands baked into Pi's one commit-gate extension. The
// gate and the resolver live in that one file, so a resolver registered without a
// gate is a state steer never writes and needs no repair of its own.
func relinkPi(scope, path string) (int, error) {
	// Only the project scope needs a working directory: the global extension lives
	// under the Pi config directory. So an unreadable cwd skips project scope alone.
	cwd, err := gate.ProjectDir()
	if err != nil && scope != claude.GlobalScope {
		return 0, nil
	}
	gate := pihooks.CommitGateArming(scope, cwd)
	resolver := pihooks.ResolverArming(scope, cwd)
	if !gate.Registered {
		return 0, nil
	}
	nextGate, gateChanged := relinked(gate.Registered, gate.Command, path)
	nextResolver, resolverChanged := relinked(resolver.Registered, resolver.Command, path)
	if !gateChanged && !resolverChanged {
		return 0, nil
	}
	if !gateChanged {
		nextGate = gate.Command
	}
	if !resolverChanged {
		nextResolver = resolver.Command
	}
	if _, err := pihooks.RegisterCommitGateAndResolver(scope, cwd,
		nextGate, gate.Timeout, nextResolver, resolver.Timeout); err != nil {
		return 0, err
	}
	rewritten := 0
	if gateChanged {
		reportRelink("pi", scope, "commit gate", nextGate)
		rewritten++
	}
	if resolverChanged {
		reportRelink("pi", scope, "resolver", nextResolver)
		rewritten++
	}
	return rewritten, nil
}

// repointRegistrations rewrites every steer-owned registration in both scopes and
// reports how many changed. `steer bin install` and `steer commit-gate install` both
// call it: an install repairs the registration it writes, and this repairs the ones
// it does not.
func repointRegistrations(path string) (int, error) {
	rewritten := 0
	for _, scope := range []string{claude.ProjectScope, claude.GlobalScope} {
		n, err := relinkClaude(scope, path)
		if err != nil {
			return rewritten, err
		}
		rewritten += n
		n, err = relinkCodex(scope, path)
		if err != nil {
			return rewritten, err
		}
		rewritten += n
		n, err = relinkPi(scope, path)
		if err != nil {
			return rewritten, err
		}
		rewritten += n
		n, err = relinkCheckRunners(scope, path)
		if err != nil {
			return rewritten, err
		}
		rewritten += n
	}
	return rewritten, nil
}

// relinked answers what a registered command becomes, and whether that differs from
// what it is now. It names the stable executable AND drops the reviewer flags: a flag
// outranks the settings file, so one left here makes every later `steer config set`
// silently inert.
func relinked(registered bool, command, path string) (string, bool) {
	if !registered {
		return "", false
	}
	next := steerbin.StripFlags(steerbin.Relink(command, path), reviewerFlags...)
	return next, next != command
}

func reportRelink(harnessID, scope, channel, command string) {
	utils.Info(fmt.Sprintf("%s, %s scope: %s -> %s", harnessID, scope, channel, command))
}

func init() {
	rootCmd.AddCommand(binCmd)
	binCmd.AddCommand(binInstallCmd)
}
