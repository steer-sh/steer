package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steer-sh/steer-cli/internal/checks"
	"github.com/steer-sh/steer-cli/internal/claude"
	codexhooks "github.com/steer-sh/steer-cli/internal/codex"
	"github.com/steer-sh/steer-cli/internal/gate"
	"github.com/steer-sh/steer-cli/internal/guardscope"
	"github.com/steer-sh/steer-cli/internal/harness"
	"github.com/steer-sh/steer-cli/internal/instructions"
	pihooks "github.com/steer-sh/steer-cli/internal/pi"
	"github.com/steer-sh/steer-cli/internal/status"
	"github.com/steer-sh/steer-cli/internal/steerbin"
	"github.com/steer-sh/steer-cli/internal/steerpath"
)

var (
	uninstallApply bool
	uninstallPurge bool
)

// uninstallItem is one Steer registration an uninstall plan names.
type uninstallItem struct {
	// Harness is the registration's harness; harness.Unknown for a Git hook.
	Harness harness.ID
	// Shared marks an instruction document that several harnesses read, so a
	// one-harness removal keeps it.
	Shared bool
	What   string
	// Path is the file the removal changes, or "" when no one file names it.
	Path string
	// Remove takes the registration out. A project-scope item is never removed.
	Remove func() error
	// Command removes a project-scope item one component at a time, or "".
	Command string
}

var uninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Remove every global Steer registration; prints the plan, --apply runs it",
	Long: `uninstall finds every registration Steer owns in each agent's global
configuration and prints the plan to remove it. --apply runs the plan, then reads
the configuration again and fails if a registration is left:

  - the commit gate and its resolver
  - the turn-end check runner
  - the guard runner
  - the Steer Rules section of the global instruction document
  - Steer's status line; a status line Steer wrapped comes back as it was

Registrations that are not Steer's stay as they are. --apply also removes this
repository's Git pre-commit hook when Steer wrote it, because Git does not commit
that file.

--harness limits the plan to one agent's registrations and leaves the Git hook,
which serves every agent.

A project-scope registration is committed for the whole repository, so uninstall
names the ones in this repository and changes none of them. It cannot see other
repositories.

~/.steer stays: review ledgers, guards, rules and reviewer settings. --purge with
--apply deletes it after every global registration is gone.

The registrations call ~/.steer/bin/steer, not the plugin, so removing the plugin
removes none of them. Run uninstall first, then remove the plugin.`,
	Example: `  ~/.steer/bin/steer uninstall
  ~/.steer/bin/steer uninstall --apply
  ~/.steer/bin/steer uninstall --harness claude-code --apply
  ~/.steer/bin/steer uninstall --apply --purge`,
	Args:         cobra.NoArgs,
	SilenceUsage: true,
	RunE:         runUninstall,
}

func runUninstall(cmd *cobra.Command, _ []string) error {
	out := cmd.OutOrStdout()
	home, err := steerpath.Home()
	if err != nil {
		return err
	}
	target, err := uninstallTarget()
	if err != nil {
		return err
	}
	all, err := uninstallItems(claude.GlobalScope)
	if err != nil {
		return err
	}
	global, others := splitByHarness(all, target)
	root, project, local, err := repositoryUninstallItems(all)
	if err != nil {
		return err
	}
	// A Git hook has no harness. One kept for any reason stays in the plan.
	var hooks, scoped []uninstallItem
	for _, item := range project {
		if item.Harness == harness.Unknown && !item.Shared {
			hooks = append(hooks, item)
		} else {
			scoped = append(scoped, item)
		}
	}
	scoped, _ = splitByHarness(scoped, target)
	if target != harness.Unknown {
		// A Git hook serves every agent, so a one-agent removal keeps it.
		for _, item := range local {
			item.Command = "rm " + steerbin.Quote(item.Path)
			hooks = append(hooks, item)
		}
		local = nil
	} else if adoption, ok := checkAdoptionItem(root); ok {
		// An adoption is machine-local and serves every agent, like the Git hook.
		local = append(local, adoption)
	}
	project = append(scoped, hooks...)
	for _, item := range others {
		if item.Shared {
			item.What += " (another agent reads this document too)"
			project = append(project, item)
		}
	}
	if uninstallPurge && len(others) > 0 {
		return fmt.Errorf("--purge deletes ~/.steer, which %d other Steer registration(s) still need, "+
			"starting with %s; run without --harness to remove them all", len(others), others[0].What)
	}
	if uninstallPurge && len(hooks) > 0 {
		// A steer git-hooks pre-commit exits 1 once steer is gone, so it blocks commits.
		return fmt.Errorf("--purge deletes ~/.steer, which the %s at %s still runs; remove the hook first",
			hooks[0].What, hooks[0].Path)
	}
	printUninstallPlan(out, home, root, global, local, project)
	if !uninstallApply {
		if len(global) == 0 && len(local) == 0 && !uninstallPurge {
			return nil
		}
		next := "steer uninstall --apply"
		if target != harness.Unknown {
			next += " --harness " + string(target)
		}
		if uninstallPurge {
			next += " --purge"
		}
		fmt.Fprintf(out, "\nNothing changed. To run this plan: %s\n", next)
		return nil
	}

	fmt.Fprintln(out)
	var failed []string
	reported := map[string]bool{}
	for _, item := range append(global, local...) {
		if err := item.Remove(); err != nil {
			failed = append(failed, fmt.Sprintf("%s: %v", item.What, err))
			reported[item.What] = true
			continue
		}
		fmt.Fprintf(out, "Removed: %s\n", item.What)
	}
	remaining, err := uninstallItems(claude.GlobalScope)
	if err != nil {
		return err
	}
	left, _ := splitByHarness(remaining, target)
	for _, item := range left {
		if !reported[item.What] {
			failed = append(failed, fmt.Sprintf("%s: still registered in %s", item.What, item.Path))
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("uninstall did not finish; ~/.steer was kept:\n  %s", strings.Join(failed, "\n  "))
	}
	forget := steerbin.ClearSource
	if target != harness.Unknown {
		forget = func() error { return status.ForgetPluginSource(target) }
	}
	if err := forget(); err != nil {
		return err
	}
	if uninstallPurge {
		if err := os.RemoveAll(home); err != nil {
			return fmt.Errorf("delete %s: %w", home, err)
		}
		fmt.Fprintf(out, "Deleted: %s\n", home)
	}
	if target == harness.Unknown {
		fmt.Fprintln(out, "\nNo global Steer registration is left. Remove the Steer plugin from each agent next.")
	} else {
		kept := ""
		if len(project) > 0 {
			kept = ", apart from what the plan keeps"
		}
		fmt.Fprintf(out, "\nNo global %s registration of Steer is left%s.\n", status.HarnessLabel(string(target)), kept)
	}
	return nil
}

// uninstallTarget parses --harness; harness.Unknown means every harness.
func uninstallTarget() (harness.ID, error) {
	if strings.TrimSpace(harnessFlag) == "" {
		return harness.Unknown, nil
	}
	return harness.Parse(harnessFlag)
}

// splitByHarness returns the items of target and the rest. harness.Unknown takes all.
func splitByHarness(items []uninstallItem, target harness.ID) (match, rest []uninstallItem) {
	for _, item := range items {
		if target == harness.Unknown || (item.Harness == target && !item.Shared) {
			match = append(match, item)
		} else {
			rest = append(rest, item)
		}
	}
	return match, rest
}

// uninstallItems reads every Steer registration at one scope through the readers
// status uses.
func uninstallItems(scope string) ([]uninstallItem, error) {
	cwd, _ := gate.ProjectDir()
	var items []uninstallItem
	add := func(id harness.ID, part, path string, remove func() error, command string) {
		items = append(items, uninstallItem{
			Harness: id,
			What:    fmt.Sprintf("%s, %s scope: %s", status.HarnessLabel(string(id)), scope, part),
			Path:    path,
			Remove:  remove,
			Command: command,
		})
	}
	gateCommand := func(id harness.ID) string {
		return fmt.Sprintf("steer commit-gate remove --harness %s --scope %s", id, scope)
	}

	// A reader that cannot load a file reports nothing registered there, so an
	// unreadable file is an item of its own: removing it fails, and so does the run.
	broken := map[harness.ID]bool{}
	addUnreadable := func(id harness.ID, part, path string, err error) {
		if !broken[id] {
			broken[id] = true
			add(id, part, path, unreadable(err), "")
		}
	}
	if _, err := claude.LoadSettings(scope); err != nil {
		path, _ := claude.GetSettingsFile(scope)
		addUnreadable(harness.ClaudeCode, "settings that cannot be read", path, err)
	}
	if path, err := codexhooks.HooksReadable(scope, cwd); err != nil {
		addUnreadable(harness.Codex, "hooks file that cannot be read", path, err)
	}

	if claude.CommitGateHookArmed(scope).Registered || claude.BackfillHookArming(scope).Registered {
		path, _ := claude.GetSettingsFile(scope)
		add(harness.ClaudeCode, "commit gate", path, func() error {
			_, err := claude.RemoveCommitGateAndResolver(scope)
			return err
		}, gateCommand(harness.ClaudeCode))
	}
	codexGate, codexResolver := codexhooks.CommitGateArming(scope, cwd), codexhooks.ResolverArmingRow(scope, cwd)
	if codexGate.Registered || codexResolver.Registered {
		add(harness.Codex, "commit gate", firstPath(codexGate.Path, codexResolver.Path), func() error {
			_, _, err := codexhooks.RemoveCommitGateAndResolver(scope, cwd)
			return err
		}, gateCommand(harness.Codex))
	}
	piGate := pihooks.CommitGateArming(scope, cwd)
	if piGate.Registered || pihooks.ResolverArming(scope, cwd).Registered {
		add(harness.Pi, "commit gate", piGate.Path, func() error {
			_, _, err := pihooks.RemoveCommitGateAndResolver(scope, cwd)
			return err
		}, gateCommand(harness.Pi))
	}

	for _, target := range runnerHarnesses {
		arming, err := checkRunnerArming(target, scope)
		if err != nil || !arming.Registered {
			continue
		}
		remove := func() error { _, err := claude.RemoveCheckHook(scope); return err }
		if target == harness.Codex {
			remove = func() error { _, _, err := codexhooks.RemoveCheckHook(scope, cwd); return err }
		}
		add(target, "check runner", arming.Path, remove,
			fmt.Sprintf("steer check uninstall --harness %s --scope %s", target, scope))
	}

	documents := map[string]int{}
	for _, entry := range harness.All() {
		if !entry.Capabilities.HookRegistration {
			continue
		}
		registrar, err := guardscope.RegistrarFor(entry.ID)
		if err != nil {
			return nil, err
		}
		registered, err := registrar.MasterRegistered(scope)
		if err != nil {
			addUnreadable(entry.ID, "guard runner registration that cannot be read", "", err)
		}
		if registered {
			add(entry.ID, "guard runner", masterConfigPath(entry.ID, scope, cwd), func() error {
				return registrar.RemoveMaster(scope)
			}, "")
		}
		document, err := registrar.InstructionPath(scope)
		if err != nil {
			continue
		}
		key := document
		if real, err := filepath.EvalSymlinks(document); err == nil {
			key = real
		}
		if at, seen := documents[key]; seen {
			if at >= 0 {
				items[at].Shared = true
			}
			continue
		}
		documents[key] = -1
		present, err := instructions.HasSection(document)
		if err != nil {
			addUnreadable(entry.ID, "instruction document that cannot be read", document, err)
		}
		if present {
			add(entry.ID, "Steer Rules section", document, func() error {
				return registrar.SetRules(nil, scope)
			}, "")
			documents[key] = len(items) - 1
		}
	}

	if line, present, mine := claude.StatusLineArmed(scope); present && mine {
		part := "status line"
		if wrapped, ok := claude.WrappedCommand(line.Command); ok {
			part += " (gives back: " + wrapped + ")"
		}
		path, _ := claude.GetSettingsFile(scope)
		add(harness.ClaudeCode, part, path, func() error {
			_, err := claude.RemoveStatusLine(scope)
			return err
		}, "steer statusline remove --scope "+scope)
	}
	return items, nil
}

// repositoryUninstallItems reads the repository the command runs in: its committed
// project-scope registrations, which uninstall leaves, and a pre-commit hook Steer
// wrote outside the work tree, which it removes. An item whose file is also a
// global item's is not repeated. Outside a repository it returns nothing.
func repositoryUninstallItems(global []uninstallItem) (root string, project, local []uninstallItem, err error) {
	root, rootErr := repoRootOf(".")
	if rootErr != nil {
		return "", nil, nil, nil
	}
	globalFiles := map[string]bool{}
	for _, item := range global {
		globalFiles[item.Path] = true
	}
	found, err := uninstallItems(claude.ProjectScope)
	if err != nil {
		return root, nil, nil, err
	}
	for _, item := range found {
		if item.Path == "" || !globalFiles[item.Path] {
			project = append(project, item)
		}
	}
	if hook, ok := steerPreCommitHook(); ok {
		quoted := steerbin.Quote(hook.Path)
		switch {
		case hook.unreadable:
			hook.Command = "rm " + quoted + "  # cannot be read, so Steer cannot tell what it runs"
			project = append(project, hook.uninstallItem)
		case insideWorkTree(root, hook.Path):
			hook.Command = "git rm " + quoted
			project = append(project, hook.uninstallItem)
		case !insideGitDir(hook.Path):
			hook.Command = "rm " + quoted + "  # a hooks directory other repositories can share"
			project = append(project, hook.uninstallItem)
		case !generatedPreCommit(hook.text):
			hook.Command = "rm " + quoted + "  # differs from the hook Steer writes"
			project = append(project, hook.uninstallItem)
		default:
			local = append(local, hook.uninstallItem)
		}
	}
	return root, project, local, nil
}

type preCommitHook struct {
	uninstallItem
	text       string
	unreadable bool
}

// steerPreCommitHook finds this repository's pre-commit hook when it carries the
// marker of the commit gate or of steer git-hooks.
func steerPreCommitHook() (preCommitHook, bool) {
	dir, err := gitHooksDir()
	if err != nil {
		return preCommitHook{}, false
	}
	path := filepath.Join(dir, "pre-commit")
	item := uninstallItem{What: "Git pre-commit hook", Path: path, Remove: func() error { return os.Remove(path) }}
	raw, err := os.ReadFile(path)
	if err != nil {
		// A hook that exists but cannot be read may still be Steer's; it is kept.
		if _, statErr := os.Lstat(path); statErr == nil {
			return preCommitHook{uninstallItem: item, unreadable: true}, true
		}
		return preCommitHook{}, false
	}
	text := string(raw)
	if !strings.Contains(text, commitGatePreCommitMarker) && !strings.Contains(text, steerPreCommitMarker) {
		return preCommitHook{}, false
	}
	return preCommitHook{uninstallItem: item, text: text}, true
}

// checkAdoptionItem names this repository's check adoption, the machine-local
// record that lets a Stop runner run its check.sh.
func checkAdoptionItem(root string) (uninstallItem, bool) {
	if root == "" {
		return uninstallItem{}, false
	}
	identity, err := checks.RepoIdentity(root)
	if err != nil {
		return uninstallItem{}, false
	}
	_, adopted, err := checks.AdoptionOf(identity)
	if err != nil {
		return uninstallItem{What: "check adoption record that cannot be read", Path: root, Remove: unreadable(err)}, true
	}
	if !adopted {
		return uninstallItem{}, false
	}
	return uninstallItem{
		What: "check adoption of this repository",
		Path: root,
		Remove: func() error {
			_, err := checks.Unadopt(identity)
			return err
		},
	}, true
}

// generatedPreCommit reports whether text is exactly a hook Steer writes, so a
// removal deletes no line an operator added.
func generatedPreCommit(text string) bool {
	for _, script := range []string{buildCommitGatePreCommitScript(),
		buildSteerPreCommitScript(false), buildSteerPreCommitScript(true)} {
		if text == script {
			return true
		}
	}
	return false
}

// insideGitDir reports whether path lies in this repository's own git directory,
// where no other repository runs hooks.
func insideGitDir(path string) bool {
	dir, err := runGit("rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return false
	}
	return insideTree(strings.TrimSpace(dir), path)
}

func unreadable(err error) func() error {
	return func() error { return fmt.Errorf("fix or remove the file, then run uninstall again: %w", err) }
}

// insideWorkTree reports whether path is a file Git can commit: under root and not
// under root/.git.
func insideWorkTree(root, path string) bool {
	if !insideTree(root, path) {
		return false
	}
	rel, _ := filepath.Rel(gate.Canonical(root), gate.Canonical(path))
	return rel != ".git" && !strings.HasPrefix(rel, ".git"+string(filepath.Separator))
}

func insideTree(root, path string) bool {
	rel, err := filepath.Rel(gate.Canonical(root), gate.Canonical(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// masterConfigPath names the configuration file that registers a harness's guard
// runner, or "" for a harness that keeps it elsewhere.
func masterConfigPath(id harness.ID, scope, cwd string) string {
	switch id {
	case harness.ClaudeCode:
		path, _ := claude.GetSettingsFile(scope)
		return path
	case harness.Codex:
		return codexhooks.MasterArming(scope, cwd).Path
	}
	return ""
}

func firstPath(paths ...string) string {
	for _, p := range paths {
		if p != "" {
			return p
		}
	}
	return ""
}

func printUninstallPlan(out io.Writer, home, root string, global, local, project []uninstallItem) {
	fmt.Fprintln(out, "Steer uninstall plan")
	if len(global) == 0 && len(local) == 0 {
		fmt.Fprintln(out, "\nSteer found no global registration to remove on this machine.")
	}
	printItems(out, "Remove from the global configuration:", global, false)
	printItems(out, "Remove from this repository (Git does not commit it):", local, false)
	printItems(out, fmt.Sprintf("Leave as they are (committed in %s, or shared; remove them yourself):", root),
		project, true)

	fmt.Fprintln(out)
	if uninstallPurge {
		fmt.Fprintf(out, "Delete %s: review ledgers, guards, rules, reviewer settings and the stable executable.\n", home)
		if len(project) > 0 {
			fmt.Fprintln(out, "The project-scope registrations above then call a missing executable on this machine.")
		}
		return
	}
	fmt.Fprintf(out, "Keep %s: review ledgers, guards, rules and reviewer settings. --purge deletes it.\n", home)
}

func printItems(out io.Writer, heading string, items []uninstallItem, withCommand bool) {
	if len(items) == 0 {
		return
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, heading)
	for _, item := range items {
		line := "  " + item.What
		if item.Path != "" {
			line += "  " + item.Path
		}
		fmt.Fprintln(out, line)
		if withCommand && item.Command != "" {
			fmt.Fprintln(out, "      "+item.Command)
		}
	}
}

func init() {
	rootCmd.AddCommand(uninstallCmd)
	uninstallCmd.Flags().BoolVar(&uninstallApply, "apply", false, "run the plan")
	uninstallCmd.Flags().BoolVar(&uninstallPurge, "purge", false, "with --apply, also delete ~/.steer")
}
