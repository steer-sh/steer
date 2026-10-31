package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/guard"
	"github.com/steer-sh/steer-cli/internal/hookstore"
	"github.com/steer-sh/steer-cli/internal/status"
	"github.com/steer-sh/steer-cli/internal/steerbin"
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
	out := captureStdout(t, func() { rebuildErr = rebuildStaleMasters() })
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
		line := staleMasterLine(labels, c.scopes, c.pluginsDiffer)
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

// TestPluginInstallNamesOnlyTheRunningCopy pins that the nudge names the install
// of the copy the harness runs, quoted, and never an older version directory the
// cache kept beside it.
func TestPluginInstallNamesOnlyTheRunningCopy(t *testing.T) {
	cache := filepath.Join(t.TempDir(), "plugin cache")
	old := filepath.Join(cache, "steer", "0.1.0")
	current := filepath.Join(cache, "steer", "0.1.1")
	copies := []status.PluginCopy{
		{Harness: "claude-code", Path: old, State: status.PluginDifferentBinary},
		{Harness: "claude-code", Path: current, State: status.PluginDifferentBinary},
	}
	for _, dir := range []string{old, current} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	running, err := filepath.EvalSymlinks(current)
	if err != nil {
		t.Fatal(err)
	}
	running = filepath.Join(running, "bin", "darwin-arm64", "steer")

	install := runningCopyInstall(copies, running)
	want := steerbin.Quote(filepath.Join(current, "scripts", "steer")) + " bin install"
	if install != want {
		t.Fatalf("install = %q, want %q", install, want)
	}
	if runningCopyInstall(copies, "/usr/local/bin/steer") != "" {
		t.Fatal("a binary outside every copy named an install")
	}

	_, labels := status.DifferingPlugins(copies)
	if line := pluginCopyLine(labels, install); !strings.Contains(line, want) {
		t.Errorf("line = %q, want it to name %q", line, want)
	}
	if line := pluginCopyLine(labels, ""); strings.Contains(line, "bin install") {
		t.Errorf("line = %q names an install with no running copy", line)
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
