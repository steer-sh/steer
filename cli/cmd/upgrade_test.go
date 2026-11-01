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
