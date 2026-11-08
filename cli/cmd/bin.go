package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"

	"github.com/spf13/cobra"
	"github.com/steer-sh/steer-cli/internal/claude"
	codexhooks "github.com/steer-sh/steer-cli/internal/codex"
	"github.com/steer-sh/steer-cli/internal/gate"
	"github.com/steer-sh/steer-cli/internal/guardscope"
	"github.com/steer-sh/steer-cli/internal/harness"
	pihooks "github.com/steer-sh/steer-cli/internal/pi"
	"github.com/steer-sh/steer-cli/internal/status"
	"github.com/steer-sh/steer-cli/internal/steerbin"
	"github.com/steer-sh/steer-cli/internal/steerpath"
	"github.com/steer-sh/steer-cli/internal/styles"
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

After a plugin update, the next session start runs this install from the updated
plugin copy when a global registration already names ~/.steer/bin/steer (see
'steer report cadence'). Run it yourself to place the executable the first time,
to keep the hooks after removing the plugin, or when the startup notice names it.`,
	Example: `  steer bin install
  ~/.claude/plugins/cache/<marketplace>/steer/<version>/scripts/steer bin install`,
	RunE: func(cmd *cobra.Command, args []string) error {
		release, held, err := lockInstall()
		if err != nil {
			return err
		}
		if !held {
			return fmt.Errorf("another steer bin install is running; run this again when it finishes")
		}
		defer release()
		fmt.Println()
		fmt.Println(styles.MainHeader.Render("Stable Executable"))
		fmt.Println()
		return installStable(&installReport{out: os.Stdout}, nil)
	},
}

// installReport carries what an install did: its narrative goes to out, and
// actions keeps each step it leaves to the operator.
type installReport struct {
	out     io.Writer
	actions []string
}

func (r *installReport) info(msg string) {
	fmt.Fprintln(r.out, styles.StatusIndicator("info", msg))
}

func (r *installReport) success(msg string) {
	fmt.Fprintln(r.out, styles.StatusIndicator("success", msg))
}

// action reports a step left to the operator.
func (r *installReport) action(msg string) {
	r.info(msg)
	r.keep(msg)
}

// keep records a step left to the operator that was already printed.
func (r *installReport) keep(msg string) {
	if !slices.Contains(r.actions, msg) {
		r.actions = append(r.actions, msg)
	}
}

// installLockName is the file every install of the stable executable locks.
const installLockName = "bin-install.lock"

// lockInstall takes the install lock without waiting; held is false while another
// process installs.
func lockInstall() (release func(), held bool, err error) {
	home, err := steerpath.Home()
	if err != nil {
		return nil, false, err
	}
	if err := os.MkdirAll(home, 0o755); err != nil {
		return nil, false, err
	}
	return gate.TryLockFile(filepath.Join(home, installLockName))
}

// installStable places the running binary at the stable executable, repoints every
// Steer-owned registration at it and rebuilds a stale global master. When the
// executable already exists the binary is placed last, so an install that stops
// early leaves the hooks on their old build and the next one repeats all of it.
// verifyLater, when set, takes each rebuilt scope's fixture rerun off this process.
func installStable(r *installReport, verifyLater func(scope string)) error {
	path, err := steerbin.Path()
	if err != nil {
		return err
	}
	placed := false
	if _, err := os.Stat(path); err != nil {
		// Nothing runs the stable path yet, so the registrations below must not name it
		// before it exists.
		if _, err := steerbin.Ensure(); err != nil {
			return err
		}
		placed = true
	}
	rewritten, err := repointRegistrations(r, path)
	if err != nil {
		return err
	}
	if rewritten == 0 {
		r.info("No registration needed rewriting.")
	} else {
		r.info(fmt.Sprintf("Repointed %d registration(s) at the stable executable.", rewritten))
	}
	if err := rebuildStaleMasters(r, verifyLater); err != nil {
		return err
	}
	if !placed {
		if _, err := steerbin.Ensure(); err != nil {
			return err
		}
	}
	r.success("Installed " + path)
	r.info(versionLine())
	if steerbin.RunningIsStable() {
		// Installing the stable executable over itself is the operator keeping
		// Steer without a plugin, so no removed plugin is named any more.
		return steerbin.ClearSource()
	}
	recordPluginCopies()
	return nil
}

// recordPluginCopies adds each harness's active Steer plugin copy to the source
// record, so a later removal of a plugin this install did not come from is named too.
func recordPluginCopies() {
	for _, c := range status.PluginCopies() {
		if status.PluginInstalled(harness.ID(c.Harness)) {
			steerbin.RecordSource(c.Path)
		}
	}
}

// rebuildStaleMasters rebuilds the global guard master when its runtime is not
// this binary's, and names the rebuild of a stale project master without running
// it, because that master is committed for the whole repository. The rebuild
// re-registers only the harnesses already holding a registration in the scope.
func rebuildStaleMasters(r *installReport, verifyLater func(scope string)) error {
	rows := status.MasterRuntimes(claude.GlobalScope, claude.ProjectScope)
	for _, s := range status.StaleScopes(rows) {
		if s != claude.GlobalScope {
			r.action(fmt.Sprintf("The %s guard master runs an older runtime and is committed; "+
				"to rebuild it for the repository, run: %s", s, status.RebuildRepair(rows, s)))
			continue
		}
		targets, err := registeredTargets(s)
		if err != nil {
			return err
		}
		if len(targets) == 0 {
			continue // RebuildMaster would fall back to registering Claude Code
		}
		rebuild := claude.RebuildMaster
		if verifyLater != nil {
			rebuild = claude.RebuildMasterUnverified
		}
		result, err := rebuild(s, targets...)
		if err != nil {
			return fmt.Errorf("rebuild the %s guard master: %w", s, err)
		}
		if verifyLater != nil {
			verifyLater(s)
		}
		r.info(fmt.Sprintf("The %s guard master ran an older runtime; rebuilt it:", s))
		printRebuild(r.out, s, result)
		for _, phrase := range reapprovals(result) {
			r.keep(phrase)
		}
	}
	return nil
}

// registeredTargets resolves the harnesses holding a master registration in scope,
// whatever --harness names. A registration that cannot be read fails the rebuild
// before it writes, rather than leaving that harness on the old master.
func registeredTargets(scope string) ([]harness.Registrar, error) {
	// Codex reads an unreadable hooks file as holding no registration.
	if path, err := codexhooks.HooksReadable(scope, "."); err != nil {
		return nil, fmt.Errorf("read the Codex guard master registration in %s: %w", path, err)
	}
	all, err := guardscope.HookRegistrars(false)
	if err != nil {
		return nil, err
	}
	out := []harness.Registrar{}
	for _, registrar := range all {
		registered, err := registrar.MasterRegistered(scope)
		if err != nil {
			return nil, fmt.Errorf("read the %s guard master registration: %w", registrar.Harness().DisplayName, err)
		}
		if registered {
			out = append(out, registrar)
		}
	}
	return out, nil
}

// relinkClaude repoints this scope's Claude Code registrations, reporting how many
// changed. A registration already naming the stable path is left untouched, and one
// naming a command that is not steer's own is never rewritten.
func relinkClaude(r *installReport, scope, path string) (int, error) {
	rewritten := 0
	gate := claude.CommitGateHookArmed(scope)
	if next, ok := relinked(gate.Registered, gate.Command, path); ok {
		if err := claude.RegisterCommitGateHook(scope, next, gate.Timeout); err != nil {
			return rewritten, err
		}
		reportRelink(r, "claude-code", scope, "commit gate", next)
		rewritten++
	}
	resolver := claude.BackfillHookArming(scope)
	if next, ok := relinked(resolver.Registered, resolver.Command, path); ok {
		if err := claude.RegisterBackfillHook(scope, next, resolver.Timeout); err != nil {
			return rewritten, err
		}
		reportRelink(r, "claude-code", scope, "resolver", next)
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
		reportRelink(r, "claude-code", scope, "guard master", command)
		rewritten++
	}
	return rewritten, nil
}

// relinkCodexMaster rewrites a Codex guard master registered in an old form, which
// the operator then approves again in /hooks.
func relinkCodexMaster(r *installReport, scope string) (int, error) {
	cwd, err := gate.ProjectDir()
	if err != nil {
		return 0, nil
	}
	stale, err := codexhooks.MasterCommandStale(scope, cwd)
	if err != nil || !stale {
		return 0, err
	}
	if _, err := codexhooks.RegisterMaster(scope, cwd); err != nil {
		return 0, err
	}
	command, err := codexhooks.MasterCommand(scope, cwd)
	if err != nil {
		return 0, err
	}
	reportRelink(r, "codex", scope, "guard master", command)
	r.action(status.GuardCommandChangedPhrase(status.HarnessLabel(string(harness.Codex))))
	return 1, nil
}

// relinkCodex repoints this scope's Codex registrations. The gate and the resolver
// are written together, because Codex's writer arms them in one save.
func relinkCodex(r *installReport, scope, path string) (int, error) {
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
			r.action(fmt.Sprintf("codex, %s scope: the resolver is registered without a gate; "+
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
		reportRelink(r, "codex", scope, "commit gate", nextGate)
		rewritten++
	}
	if resolverChanged {
		reportRelink(r, "codex", scope, "resolver", nextResolver)
		rewritten++
	}
	return rewritten, nil
}

// relinkPi repoints both commands baked into Pi's one commit-gate extension. The
// gate and the resolver live in that one file, so a resolver registered without a
// gate is a state steer never writes and needs no repair of its own.
func relinkPi(r *installReport, scope, path string) (int, error) {
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
		reportRelink(r, "pi", scope, "commit gate", nextGate)
		rewritten++
	}
	if resolverChanged {
		reportRelink(r, "pi", scope, "resolver", nextResolver)
		rewritten++
	}
	return rewritten, nil
}

// repointRegistrations rewrites every steer-owned registration in both scopes and
// reports how many changed. `steer bin install` and `steer commit-gate install` both
// call it: an install repairs the registration it writes, and this repairs the ones
// it does not.
func repointRegistrations(r *installReport, path string) (int, error) {
	rewritten := 0
	for _, scope := range []string{claude.ProjectScope, claude.GlobalScope} {
		n, err := relinkClaude(r, scope, path)
		if err != nil {
			return rewritten, err
		}
		rewritten += n
		n, err = relinkCodex(r, scope, path)
		if err != nil {
			return rewritten, err
		}
		rewritten += n
		n, err = relinkCodexMaster(r, scope)
		if err != nil {
			return rewritten, err
		}
		rewritten += n
		n, err = relinkPi(r, scope, path)
		if err != nil {
			return rewritten, err
		}
		rewritten += n
		n, err = relinkCheckRunners(r, scope, path)
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

func reportRelink(r *installReport, harnessID, scope, channel, command string) {
	r.info(fmt.Sprintf("%s, %s scope: %s -> %s", harnessID, scope, channel, command))
}

func init() {
	rootCmd.AddCommand(binCmd)
	binCmd.AddCommand(binInstallCmd)
}
