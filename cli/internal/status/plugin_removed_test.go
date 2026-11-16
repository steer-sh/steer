package status

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/codex"
	"github.com/steer-sh/steer-cli/internal/harness"
	"github.com/steer-sh/steer-cli/internal/testhome"
)

// pluginHome makes a home whose stable executable was installed from source.
func pluginHome(t *testing.T, source func(home string) string) string {
	t.Helper()
	home := t.TempDir()
	testhome.Set(t, home)
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	if err := os.MkdirAll(filepath.Join(home, ".steer", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".steer", "bin", "source"), []byte(source(home)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := claude.RegisterCommitGateAndResolver(harness.GlobalScope, "steer commit-gate review", 460,
		"steer commit-gate backfill --if-due --apply --quiet", 30); err != nil {
		t.Fatal(err)
	}
	if _, err := codex.RegisterCommitGateAndResolver(harness.GlobalScope, home, "steer commit-gate review-codex", 460,
		"steer commit-gate backfill --if-due --apply --quiet", 30); err != nil {
		t.Fatal(err)
	}
	return home
}

func writeFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func removed(id harness.ID) bool {
	for _, got := range RemovedPlugins() {
		if got == id {
			return true
		}
	}
	return false
}

// TestRemovedPluginReadsTheHarnessRecord: a source in a harness's plugin cache is a
// removed plugin exactly when that harness's install record no longer names Steer.
func TestRemovedPluginReadsTheHarnessRecord(t *testing.T) {
	home := pluginHome(t, func(home string) string {
		return filepath.Join(home, ".claude", "plugins", "cache", "m", "steer", "0.1.7", "bin", "darwin-arm64", "steer")
	})
	if !removed(harness.ClaudeCode) {
		t.Error("no install record: Claude Code not read as removed")
	}
	if !strings.Contains(PluginRemovedNotice(), "uninstall --harness claude-code --apply") {
		t.Errorf("notice = %q", PluginRemovedNotice())
	}
	writeFile(t, filepath.Join(home, ".claude", "plugins", "installed_plugins.json"),
		`{"plugins":{"steer@m":[{"installPath":"`+filepath.Join(home, ".claude", "plugins", "cache", "m", "steer", "0.1.7")+`"}]}}`)
	if removed(harness.ClaudeCode) {
		t.Error("an install record naming Steer read as a removed plugin")
	}

	home = pluginHome(t, func(home string) string {
		return filepath.Join(home, ".codex", "plugins", "cache", "m", "steer", "0.1.7", "bin", "linux-amd64", "steer")
	})
	writeFile(t, filepath.Join(home, ".codex", "config.toml"), "[plugins.\"other@m\"]\nenabled = true\n")
	if !removed(harness.Codex) {
		t.Error("no Codex plugin entry: Codex not read as removed")
	}
	writeFile(t, filepath.Join(home, ".codex", "config.toml"), "[plugins.'steer@m']\nenabled = true\n")
	if removed(harness.Codex) {
		t.Error("a single-quoted Codex plugin key for Steer read as a removed plugin")
	}

	pluginHome(t, func(home string) string { return filepath.Join(home, "src", "steer", "cli", "bin", "steer") })
	if len(RemovedPlugins()) != 0 || PluginRemovedNotice() != "" {
		t.Error("a source outside every plugin cache read as a removed plugin")
	}
}

// TestRemovedPluginsReadsEverySource: an earlier install from one harness's plugin is
// still named after a later install from the other harness's plugin.
func TestRemovedPluginsReadsEverySource(t *testing.T) {
	home := pluginHome(t, func(home string) string {
		return filepath.Join(home, ".claude", "plugins", "cache", "m", "steer", "0.1.7", "bin", "x", "steer") + "\n" +
			filepath.Join(home, ".codex", "plugins", "cache", "m", "steer", "0.1.7", "bin", "x", "steer")
	})
	writeFile(t, filepath.Join(home, ".codex", "config.toml"), "[plugins.\"steer@m\"]\nenabled = true\n")
	if !removed(harness.ClaudeCode) || removed(harness.Codex) {
		t.Errorf("RemovedPlugins() = %v, want only claude-code", RemovedPlugins())
	}
	if err := ForgetPluginSource(harness.ClaudeCode); err != nil {
		t.Fatal(err)
	}
	if len(RemovedPlugins()) != 0 || len(PluginSources()) != 1 {
		t.Errorf("after forgetting Claude Code: removed %v, sources %v", RemovedPlugins(), PluginSources())
	}
}

// TestRemovedPluginNeedsARegistrationThatStillRuns: a removed plugin whose harness no
// longer calls Steer raises no notice, because nothing of Steer's still runs there.
func TestRemovedPluginNeedsARegistrationThatStillRuns(t *testing.T) {
	pluginHome(t, func(home string) string {
		return filepath.Join(home, ".claude", "plugins", "cache", "m", "steer", "0.1.7", "bin", "x", "steer")
	})
	if !removed(harness.ClaudeCode) {
		t.Fatal("setup: Claude Code not read as removed")
	}
	if _, err := claude.RemoveCommitGateAndResolver(harness.GlobalScope); err != nil {
		t.Fatal(err)
	}
	if removed(harness.ClaudeCode) {
		t.Error("a harness with no Steer registration read as a removed plugin whose hooks still run")
	}
}
