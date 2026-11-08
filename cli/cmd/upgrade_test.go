package cmd

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/claude"
	codexhooks "github.com/steer-sh/steer-cli/internal/codex"
	"github.com/steer-sh/steer-cli/internal/gate/reviewers"
	"github.com/steer-sh/steer-cli/internal/guard"
	"github.com/steer-sh/steer-cli/internal/hookstore"
	"github.com/steer-sh/steer-cli/internal/status"
	"github.com/steer-sh/steer-cli/internal/steerbin"
	"github.com/steer-sh/steer-cli/internal/steerpath"
	"github.com/steer-sh/steer-cli/internal/testhome"
	"github.com/steer-sh/steer-cli/internal/version"
)

// TestVersionFlagNamesTheBuild pins that --version prints the stamps that tell two
// builds apart: every local build shares the version "dev".
func TestVersionFlagNamesTheBuild(t *testing.T) {
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetArgs([]string{"--version"})
	t.Cleanup(func() {
		rootCmd.SetOut(nil)
		rootCmd.SetArgs(nil)
	})
	if err := rootCmd.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{version.Version, version.GitCommit, version.BuildDate} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("--version printed %q, want it to name %q", out.String(), want)
		}
	}
}

// ageStamp rewrites a master's stamp to a runtime no binary carries.
func ageStamp(t *testing.T, scope string) string {
	t.Helper()
	master, err := hookstore.MasterPath(scope)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(master)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, guard.StampPrefix) {
			lines[i] = guard.StampPrefix + `{"runtime":"deadbeef","version":"0.0.1"}`
		}
	}
	if err := os.WriteFile(master, []byte(strings.Join(lines, "\n")), 0o755); err != nil {
		t.Fatal(err)
	}
	return master
}

// TestBinInstallRebuildsOnlyTheGlobalMaster pins the upgrade half of bin install:
// a stale global master is rebuilt with this binary's runtime, and a stale
// project master, which the repository commits, is left as it is.
func TestBinInstallRebuildsOnlyTheGlobalMaster(t *testing.T) {
	testhome.Set(t, t.TempDir())
	withCmdProjectTempDir(t)
	for _, scope := range []string{claude.GlobalScope, claude.ProjectScope} {
		addOrphanFactoryHook(t, scope, "orphan-"+scope, "orphan-"+scope, "never use var in "+scope)
		if _, err := claude.RebuildMaster(scope); err != nil {
			t.Fatal(err)
		}
	}
	ageStamp(t, claude.GlobalScope)
	project := ageStamp(t, claude.ProjectScope)
	before, err := os.ReadFile(project)
	if err != nil {
		t.Fatal(err)
	}

	stale := status.StaleScopes(status.MasterRuntimes(claude.GlobalScope, claude.ProjectScope))
	if len(stale) != 2 {
		t.Fatalf("stale scopes before install = %v, want both", stale)
	}

	var rebuildErr error
	out := captureStdout(t, func() { rebuildErr = rebuildStaleMasters(&installReport{out: os.Stdout}, nil) })
	if rebuildErr != nil {
		t.Fatal(rebuildErr)
	}
	// The rebuild report is the one hooks rebuild prints, which names each
	// harness registration and any approval the changed command voids.
	if !strings.Contains(out, "registered") {
		t.Errorf("bin install printed no registration report:\n%s", out)
	}

	got := status.StaleScopes(status.MasterRuntimes(claude.GlobalScope, claude.ProjectScope))
	if len(got) != 1 || got[0] != claude.ProjectScope {
		t.Fatalf("stale scopes after install = %v, want [project]", got)
	}
	after, err := os.ReadFile(project)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("bin install rewrote the committed project master")
	}
}

// TestStaleMasterLineNamesEveryScope pins that each stale scope gets its rebuild,
// and that a differing plugin copy moves the global rebuild to the bin install:
// a rebuild run before it embeds the stable executable's older runtime again.
func TestStaleMasterLineNamesEveryScope(t *testing.T) {
	labels := []string{"claude-code global scope", "claude-code project scope"}
	both := []string{claude.GlobalScope, claude.ProjectScope}
	cases := []struct {
		scopes        []string
		pluginsDiffer bool
		want, not     []string
	}{
		{both, false, []string{status.RebuildCommand("global"), status.RebuildCommand("project")}, []string{"bin install"}},
		{both, true, []string{"bin install below rebuilds the global master", status.RebuildCommand("project")},
			[]string{status.RebuildCommand("global")}},
		{[]string{claude.GlobalScope}, true, []string{"bin install below rebuilds it"}, []string{"steer hooks rebuild"}},
		{[]string{claude.ProjectScope}, true, []string{"after the bin install below, run: " + status.RebuildCommand("project")}, nil},
	}
	for _, c := range cases {
		line := staleMasterLine(nil, labels, c.scopes, c.pluginsDiffer)
		for _, w := range c.want {
			if !strings.Contains(line, w) {
				t.Errorf("staleMasterLine(%v, %v) = %q, want %q", c.scopes, c.pluginsDiffer, line, w)
			}
		}
		for _, n := range c.not {
			if strings.Contains(line, n) {
				t.Errorf("staleMasterLine(%v, %v) = %q, must not name %q", c.scopes, c.pluginsDiffer, line, n)
			}
		}
	}
}

// TestPluginNoticeFollowsTheDirection: a newer copy names its quoted install, an older
// copy names the plugin update, and a development difference raises no notice.
func TestPluginNoticeFollowsTheDirection(t *testing.T) {
	hooks := status.BuildIdentity{Version: "0.1.4", Commit: "aaa"}
	path := filepath.Join(t.TempDir(), "plugin cache", "steer", "0.1.5")
	newer := status.PluginCopy{Harness: "codex", Path: path, State: status.PluginDifferentBinary, Version: "0.1.5", Commit: "bbb"}
	want := steerbin.Quote(filepath.Join(path, "scripts", "steer")) + " bin install"
	if got := hooksBehindInstall([]status.PluginCopy{newer}, hooks); got != want {
		t.Fatalf("install = %q, want %q", got, want)
	}
	if lines := pluginCopyLines([]status.PluginCopy{newer}, hooks); len(lines) != 1 || !strings.Contains(lines[0], "run: "+want) {
		t.Errorf("newer notice = %q", lines)
	}
	highest := newer
	highest.Path = filepath.Join(filepath.Dir(path), "0.1.6")
	highest.Version = "0.1.6"
	if got := hooksBehindInstall([]status.PluginCopy{newer, highest}, hooks); !strings.Contains(got, "0.1.6") {
		t.Errorf("install = %q, want the highest copy", got)
	}
	older := newer
	older.Version = "0.1.3"
	if got := hooksBehindInstall([]status.PluginCopy{older}, hooks); got != "" {
		t.Errorf("an older copy named install %q, which moves the hooks backward", got)
	}
	if lines := pluginCopyLines([]status.PluginCopy{older}, hooks); len(lines) != 1 ||
		!strings.Contains(lines[0], "update the Steer plugin in Codex") || strings.Contains(lines[0], "bin install") {
		t.Errorf("older notice = %q", lines)
	}
	dev := newer
	dev.Version = "dev"
	if got := status.ActionablePlugins([]status.PluginCopy{dev}, hooks); len(got) != 0 {
		t.Errorf("a development difference raised a notice: %+v", got)
	}
}

// TestPluginLinePrintsWhenTheMasterLineDefersToIt pins that a master line naming
// "the bin install below" always has that line below it, even when the plugin
// line's own key was already nudged.
func TestPluginLinePrintsWhenTheMasterLineDefersToIt(t *testing.T) {
	if !pluginLineDue(false, true, "x bin install") {
		t.Error("a deferring master line printed without the plugin line")
	}
	if pluginLineDue(false, true, "") {
		t.Error("the plugin line printed with no change and no deferral")
	}
	if pluginLineDue(false, false, "x bin install") {
		t.Error("an already-nudged plugin line printed again on its own")
	}
}

// A hook file git cannot run is not an armed channel.
func TestExecutableFileReadsTheBit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pre-commit")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if executableFile(path) {
		t.Error("a file with no executable bit read as runnable")
	}
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if !executableFile(path) {
		t.Error("an executable file read as not runnable")
	}
}

// TestBinInstallLeavesARemovedHarnessUnregistered pins that bin install's rebuild of
// a stale global master re-registers only the harnesses that still hold it: Codex,
// removed through uninstall, gets back neither the master nor the rules section,
// and Claude Code's master is rebuilt whatever --harness names.
func TestBinInstallLeavesARemovedHarnessUnregistered(t *testing.T) {
	home := t.TempDir()
	testhome.Set(t, home)
	t.Setenv("CODEX_HOME", "")
	t.Chdir(home)
	g := claude.GlobalScope
	addOrphanFactoryHook(t, g, "orphan-global", "orphan-global", "never use var globally")
	if _, err := claude.RebuildMaster(g, claude.Registrar{}, codexhooks.Registrar{}); err != nil {
		t.Fatal(err)
	}
	agents, err := codexhooks.Registrar{}.InstructionPath(g)
	if err != nil {
		t.Fatal(err)
	}

	harnessFlag = "codex"
	t.Cleanup(func() { harnessFlag = "" })
	if _, err := runUninstallWith(t, true, false); err != nil {
		t.Fatal(err)
	}
	ageStamp(t, g)

	// --harness still names Codex: the rebuild follows the registrations, not the flag.
	if err := rebuildStaleMasters(&installReport{out: os.Stdout}, nil); err != nil {
		t.Fatal(err)
	}
	if path, err := (codexhooks.Registrar{}).RegisteredMasterPath(g); err != nil || path != "" {
		t.Errorf("Codex master registration after bin install = %q (%v), want none", path, err)
	}
	if doc, err := os.ReadFile(agents); err == nil && strings.Contains(string(doc), "Steer Rules") {
		t.Errorf("bin install wrote the rules section back into %s:\n%s", agents, doc)
	}
	for _, m := range status.MasterRuntimes(g) {
		if m.Harness == "claude-code" && m.State != status.MasterCurrent {
			t.Errorf("Claude Code global master is %s after bin install, want current", m.State)
		}
	}
}

// TestBinInstallRefusesAnUnreadableCodexHooksFile pins that a stale global master
// is not rebuilt while Codex's hooks file cannot be read, since Codex would read
// as unregistered and be left on the old master.
func TestBinInstallRefusesAnUnreadableCodexHooksFile(t *testing.T) {
	home := t.TempDir()
	testhome.Set(t, home)
	t.Setenv("CODEX_HOME", "")
	t.Chdir(home)
	g := claude.GlobalScope
	addOrphanFactoryHook(t, g, "orphan-global", "orphan-global", "never use var globally")
	if _, err := claude.RebuildMaster(g, claude.Registrar{}, codexhooks.Registrar{}); err != nil {
		t.Fatal(err)
	}
	master := ageStamp(t, g)
	before, err := os.ReadFile(master)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".codex", "hooks.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := rebuildStaleMasters(&installReport{out: os.Stdout}, nil); err == nil || !strings.Contains(err.Error(), "Codex") {
		t.Fatalf("err = %v, want a failure naming the Codex registration", err)
	}
	after, err := os.ReadFile(master)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Error("bin install rewrote the master while Codex's registration was unreadable")
	}
}

// updateHome is a home whose stable executable a global commit gate already calls,
// with a Claude Code plugin copy at version whose bundled binary is this process. It
// returns the copy and the stable executable's path.
func updateHome(t *testing.T, version string) (status.PluginCopy, string) {
	t.Helper()
	home := t.TempDir()
	testhome.Set(t, home)
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Chdir(home)
	stable, err := steerbin.Path()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(stable), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stable, []byte("old build"), 0o755); err != nil {
		t.Fatal(err)
	}
	readHooksBuild = func() status.BuildIdentity { return oldHooks }
	t.Cleanup(func() { readHooksBuild = status.HooksBuild })
	quoted := steerbin.Quote(stable)
	if err := claude.RegisterCommitGateAndResolver(claude.GlobalScope, quoted+" commit-gate review", 460,
		quoted+" commit-gate backfill --if-due --apply --quiet", 30); err != nil {
		t.Fatal(err)
	}
	return pluginCopyRunningThis(t, home, version), stable
}

// pluginCopyRunningThis makes a Claude Code plugin copy whose bundled binary links to
// this test process, stamped with version.
func pluginCopyRunningThis(t *testing.T, home, version string) status.PluginCopy {
	t.Helper()
	copy := status.PluginCopy{Harness: "claude-code", State: status.PluginDifferentBinary, Version: version, Commit: "bbb",
		Path: filepath.Join(home, ".claude", "plugins", "cache", "m", "steer", version)}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(copy.Binary()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(exe, copy.Binary()); err != nil {
		t.Fatal(err)
	}
	return copy
}

var oldHooks = status.BuildIdentity{Version: "0.1.7", Commit: "aaa"}

// runsThisBinary reports whether the file at path holds this test process's bytes.
func runsThisBinary(t *testing.T, path string) bool {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	return err == nil && bytes.Equal(got, want)
}

// TestStartupUpdateInstallsTheNewerPluginCopy: the newest plugin copy ahead of the
// hooks, run at session start where a global registration calls the stable
// executable, places itself there and reports one line plus the step it leaves.
func TestStartupUpdateInstallsTheNewerPluginCopy(t *testing.T) {
	copy, stable := updateHome(t, "0.1.8")
	project := t.TempDir()
	if out, err := exec.Command("git", "-C", project, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	t.Chdir(project)
	addOrphanFactoryHook(t, claude.ProjectScope, "orphan-project", "orphan-project", "never use var here")
	if _, err := claude.RebuildMaster(claude.ProjectScope); err != nil {
		t.Fatal(err)
	}
	ageStamp(t, claude.ProjectScope)

	got := updateHooks([]status.PluginCopy{copy}, oldHooks)
	if !got.installed || got.failed {
		t.Fatalf("update = %+v, want installed", got)
	}
	if len(got.lines) == 0 || got.lines[0] != "steer: hooks updated 0.1.7 → 0.1.8" {
		t.Errorf("lines = %q, want the update line first", got.lines)
	}
	if !strings.Contains(strings.Join(got.lines, "\n"), status.RebuildCommand(claude.ProjectScope)) {
		t.Errorf("lines = %q, want the stale project master's rebuild", got.lines)
	}
	if !runsThisBinary(t, stable) {
		t.Error("the stable executable does not carry the plugin copy's build")
	}
}

// TestStartupUpdateLeavesTheHooksAlone covers every reading that must change
// nothing: a development build, an older copy, a copy that is not the newest ahead,
// a machine with no registration left after a full uninstall, and an install
// already running.
func TestStartupUpdateLeavesTheHooksAlone(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T) ([]status.PluginCopy, string)
	}{
		{"development build", func(t *testing.T) ([]status.PluginCopy, string) {
			copy, stable := updateHome(t, "0.1.8")
			copy.Version = "dev"
			return []status.PluginCopy{copy}, stable
		}},
		{"older copy", func(t *testing.T) ([]status.PluginCopy, string) {
			copy, stable := updateHome(t, "0.1.6")
			return []status.PluginCopy{copy}, stable
		}},
		{"another copy is newer", func(t *testing.T) ([]status.PluginCopy, string) {
			copy, stable := updateHome(t, "0.1.8")
			newer := copy
			newer.Harness, newer.Version = "codex", "0.1.9"
			newer.Path = filepath.Join(t.TempDir(), "steer", "0.1.9")
			return []status.PluginCopy{copy, newer}, stable
		}},
		{"full uninstall", func(t *testing.T) ([]status.PluginCopy, string) {
			copy, stable := updateHome(t, "0.1.8")
			if _, err := runUninstallWith(t, true, false); err != nil {
				t.Fatal(err)
			}
			return []status.PluginCopy{copy}, stable
		}},
		{"install running", func(t *testing.T) ([]status.PluginCopy, string) {
			copy, stable := updateHome(t, "0.1.8")
			release, held, err := lockInstall()
			if err != nil || !held {
				t.Fatalf("lock: held=%v err=%v", held, err)
			}
			t.Cleanup(release)
			return []status.PluginCopy{copy}, stable
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			copies, stable := c.setup(t)
			got := updateHooks(copies, oldHooks)
			if got.installed || got.failed || len(got.lines) > 0 {
				t.Errorf("update = %+v, want nothing", got)
			}
			if data, err := os.ReadFile(stable); err != nil || string(data) != "old build" {
				t.Errorf("the stable executable changed (%v)", err)
			}
			if sources := steerbin.Sources(); len(sources) > 0 {
				t.Errorf("the source record = %q, want it empty", sources)
			}
		})
	}
}

// TestStartupUpdateActsFromEitherHarnessOfOneRelease: with the same release cached by
// both harnesses, the copy whose session starts installs it.
func TestStartupUpdateActsFromEitherHarnessOfOneRelease(t *testing.T) {
	copy, stable := updateHome(t, "0.1.8")
	copy.Harness = "codex"
	other := copy
	other.Harness = "claude-code"
	other.Path = filepath.Join(t.TempDir(), "steer", "0.1.8")
	got := updateHooks([]status.PluginCopy{other, copy}, oldHooks)
	if !got.installed {
		t.Fatalf("update = %+v, want installed from the Codex copy", got)
	}
	if !runsThisBinary(t, stable) {
		t.Error("the stable executable does not carry the plugin copy's build")
	}
}

// TestStartupUpdateFailsWhenTheLockCannotBeOpened: a lock file Steer cannot open is a
// failed update, so the notice naming the manual install keeps printing.
func TestStartupUpdateFailsWhenTheLockCannotBeOpened(t *testing.T) {
	copy, stable := updateHome(t, "0.1.8")
	home, err := steerpath.Home()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, installLockName), 0o755); err != nil {
		t.Fatal(err)
	}
	got := updateHooks([]status.PluginCopy{copy}, oldHooks)
	if !got.failed || got.installed {
		t.Fatalf("update = %+v, want failed", got)
	}
	if data, err := os.ReadFile(stable); err != nil || string(data) != "old build" {
		t.Errorf("the stable executable changed (%v)", err)
	}
}

// TestStartupUpdateYieldsToAnInstallThatFinishedFirst: when the stable executable no
// longer carries the build the decision read, another install got there first.
func TestStartupUpdateYieldsToAnInstallThatFinishedFirst(t *testing.T) {
	copy, stable := updateHome(t, "0.1.8")
	readHooksBuild = func() status.BuildIdentity { return status.BuildIdentity{Version: "0.1.9", Commit: "ccc"} }
	got := updateHooks([]status.PluginCopy{copy}, oldHooks)
	if got.installed || got.failed || len(got.lines) > 0 {
		t.Errorf("update = %+v, want nothing", got)
	}
	if data, err := os.ReadFile(stable); err != nil || string(data) != "old build" {
		t.Errorf("the stable executable changed (%v)", err)
	}
}

// TestNoNoticeRegistersARemovedHarnessAgain: after uninstall --harness codex, with
// Claude Code kept and its global master stale, neither the startup notices nor the
// status verdict name a command that registers Codex again.
func TestNoNoticeRegistersARemovedHarnessAgain(t *testing.T) {
	home := t.TempDir()
	testhome.Set(t, home)
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Chdir(home)
	g := claude.GlobalScope
	addOrphanFactoryHook(t, g, "orphan-global", "orphan-global", "never use var globally")
	if _, err := claude.RebuildMaster(g, claude.Registrar{}, codexhooks.Registrar{}); err != nil {
		t.Fatal(err)
	}
	harnessFlag = "codex"
	t.Cleanup(func() { harnessFlag = "" })
	if _, err := runUninstallWith(t, true, false); err != nil {
		t.Fatal(err)
	}
	ageStamp(t, g)
	t.Chdir(t.TempDir())

	key, labels, scopes, rows := staleMasters()
	if key == "" {
		t.Fatal("the Claude Code global master does not read stale")
	}
	line := staleMasterLine(rows, labels, scopes, false)
	if !strings.Contains(line, status.RebuildCommand(g)+" --harness claude-code") || strings.Contains(line, "codex") {
		t.Errorf("stale master line = %q, want a rebuild for Claude Code alone", line)
	}
	if key, labels, _ := status.DetachedMasters(rows); key != "" {
		t.Errorf("the removed harness reads detached: %q", labels)
	}
	if key, labels, _ := status.RulesDrift(status.InstalledRules(g)); key != "" {
		t.Errorf("the removed harness's emptied rules section reads drifted: %q", labels)
	}
	h := status.HealthOf(&status.SystemStatus{Posture: status.Posture{Masters: rows}}, reviewers.Probe{})
	for _, issue := range h.Issues {
		if strings.Contains(issue.Fix, "hooks sync") || strings.Contains(issue.Fix, "--harness codex") ||
			issue.Fix == status.RebuildCommand(g) {
			t.Errorf("issue %q names %q, which registers Codex again", issue.Title, issue.Fix)
		}
	}
}
