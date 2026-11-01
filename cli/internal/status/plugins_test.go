package status

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/steer-sh/steer-cli/internal/steerpath"
	"github.com/steer-sh/steer-cli/internal/testhome"
)

// TestPluginCopiesReadEachCacheAgainstTheStableExecutable: a copy bundling the
// stable executable's bytes reads same-binary, one bundling other bytes of the same
// size reads different-binary, one with no bundled binary reads unknown, and a
// linked copy names its target.
func TestPluginCopiesReadEachCacheAgainstTheStableExecutable(t *testing.T) {
	home := t.TempDir()
	testhome.Set(t, home)
	t.Setenv("CODEX_HOME", "")
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	platform := runtime.GOOS + "-" + runtime.GOARCH
	bundled := func(root string) string { return filepath.Join(root, "bin", platform, steerpath.BinName) }

	stable, err := steerpath.BinPath()
	if err != nil {
		t.Fatal(err)
	}
	write(stable, "build-a")

	claudeCopy := filepath.Join(home, ".claude", "plugins", "cache", "m", "steer", "0.1.0")
	write(bundled(claudeCopy), "build-a")
	codexOld := filepath.Join(home, ".codex", "plugins", "cache", "o", "steer", "0.1.0")
	write(bundled(codexOld), "build-b")
	codexEmpty := filepath.Join(home, ".codex", "plugins", "cache", "m", "steer", "0.2.0")
	write(filepath.Join(codexEmpty, "README.md"), "no binary")
	checkout := filepath.Join(home, "checkout")
	write(bundled(checkout), "build-a")
	codexLinked := filepath.Join(home, ".codex", "plugins", "cache", "n", "steer", "0.1.0")
	if err := os.MkdirAll(filepath.Dir(codexLinked), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(checkout, codexLinked); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	write(filepath.Join(home, ".codex", "plugins", "cache", "m", "other", "0.1.0", "bin", platform, steerpath.BinName), "build-b")

	got := map[string]PluginCopy{}
	for _, p := range PluginCopies() {
		got[p.Path] = p
	}
	if len(got) != 4 {
		t.Fatalf("read %d copies, want 4: %+v", len(got), got)
	}
	want := map[string]string{
		claudeCopy:  PluginSameBinary,
		codexOld:    PluginDifferentBinary,
		codexEmpty:  PluginUnknown,
		codexLinked: PluginSameBinary,
	}
	for path, state := range want {
		if got[path].State != state {
			t.Errorf("%s: state %q, want %q", path, got[path].State, state)
		}
	}
	if got[claudeCopy].Harness != "claude-code" || got[codexOld].Harness != "codex" {
		t.Errorf("harness ids: %+v", got)
	}
	resolved, _ := filepath.EvalSymlinks(checkout)
	if got[codexLinked].Target != resolved || got[codexOld].Target != "" {
		t.Errorf("targets: linked %q, copied %q; want %q and none", got[codexLinked].Target, got[codexOld].Target, resolved)
	}
	if !got[codexOld].Differs() || got[codexEmpty].Differs() {
		t.Error("Differs holds only for a different-binary copy")
	}
}

// TestDifferingPluginsKeysOnlyTheCopiesThatDiffer: the key names each differing
// copy by harness and path in a fixed order, a same-binary or unknown copy adds
// nothing, and no differing copy gives the empty key that nudges nothing.
func TestDifferingPluginsKeysOnlyTheCopiesThatDiffer(t *testing.T) {
	rows := []PluginCopy{
		{Harness: "codex", Path: "/h/.codex/plugins/cache/m/steer/0.1.0", State: PluginDifferentBinary},
		{Harness: "claude-code", Path: "/h/.claude/plugins/cache/m/steer/0.1.0", State: PluginSameBinary},
		{Harness: "claude-code", Path: "/h/.claude/plugins/cache/m/steer/0.2.0", State: PluginDifferentBinary},
		{Harness: "codex", Path: "/h/.codex/plugins/cache/m/steer/0.2.0", State: PluginUnknown},
	}
	key, labels := DifferingPlugins(rows)
	want := "claude-code:/h/.claude/plugins/cache/m/steer/0.2.0|codex:/h/.codex/plugins/cache/m/steer/0.1.0"
	if key != want {
		t.Errorf("key %q, want %q", key, want)
	}
	if len(labels) != 2 || labels[0] != "Codex /h/.codex/plugins/cache/m/steer/0.1.0" {
		t.Errorf("labels %q", labels)
	}
	if key, labels := DifferingPlugins(rows[1:2]); key != "" || labels != nil {
		t.Errorf("a same-binary copy keyed %q with labels %q", key, labels)
	}
}

// TestPluginCopiesSkipAClaudeCopyItsInstallRecordDropped: Claude Code keeps an
// updated plugin's earlier version directory, so only the directory its install
// record names is a copy; with no readable record every cached directory counts.
func TestPluginCopiesSkipAClaudeCopyItsInstallRecordDropped(t *testing.T) {
	home := t.TempDir()
	testhome.Set(t, home)
	t.Setenv("CODEX_HOME", "")
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	platform := runtime.GOOS + "-" + runtime.GOARCH
	stable, err := steerpath.BinPath()
	if err != nil {
		t.Fatal(err)
	}
	write(stable, "build-new")
	cache := filepath.Join(home, ".claude", "plugins", "cache", "m", "steer")
	old, current := filepath.Join(cache, "0.1.1"), filepath.Join(cache, "0.1.2")
	write(filepath.Join(old, "bin", platform, steerpath.BinName), "build-old")
	write(filepath.Join(current, "bin", platform, steerpath.BinName), "build-new")

	paths := func() []string {
		var out []string
		for _, p := range PluginCopies() {
			out = append(out, p.Path)
		}
		return out
	}
	if got := paths(); len(got) != 2 {
		t.Fatalf("with no install record, copies %q, want both directories", got)
	}
	record := filepath.Join(home, ".claude", "plugins", "installed_plugins.json")
	write(record, `{"version":2,"plugins":{"steer@m":[{"scope":"user","installPath":"`+current+`"}],`+
		`"other@m":[{"scope":"user","installPath":"`+old+`"}]}}`)
	if got := paths(); len(got) != 1 || got[0] != current {
		t.Errorf("copies %q, want only the recorded %q", got, current)
	}
	write(record, `{"version":2,"plugins":{}}`)
	if got := paths(); len(got) != 0 {
		t.Errorf("an uninstalled plugin's cache gave copies %q", got)
	}
}

// TestCodexActiveRootsKeepTheVersionCodexLoads: "local" wins, then the highest version,
// compared as numbers, per marketplace.
func TestCodexActiveRootsKeepTheVersionCodexLoads(t *testing.T) {
	got := codexActiveRoots([]string{"/c/m/steer/0.1.10", "/c/m/steer/0.1.9", "/c/n/steer/0.2.0", "/c/n/steer/local"})
	want := []string{"/c/m/steer/0.1.10", "/c/n/steer/local"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("active roots = %v, want %v", got, want)
	}
}

// TestCompareToHooks: releases order by version, dev builds have no order, and one
// version with two commits conflicts.
func TestCompareToHooks(t *testing.T) {
	copyOf := func(v, c string) PluginCopy {
		return PluginCopy{State: PluginDifferentBinary, Version: v, Commit: c}
	}
	release := BuildIdentity{Version: "0.1.4", Commit: "aaa"}
	for name, tc := range map[string]struct {
		copy  PluginCopy
		hooks BuildIdentity
		want  string
	}{
		"same bytes":    {PluginCopy{State: PluginSameBinary}, BuildIdentity{}, BuildSame},
		"older":         {copyOf("0.1.3", "bbb"), release, BuildOlder},
		"newer":         {copyOf("0.1.10", "bbb"), BuildIdentity{Version: "0.1.9", Commit: "aaa"}, BuildNewer},
		"same release":  {copyOf("0.1.4", "aaa"), release, BuildSame},
		"conflict":      {copyOf("0.1.4", "bbb"), release, BuildConflict},
		"dev hooks":     {copyOf("0.1.4", "bbb"), BuildIdentity{Version: "dev", Commit: "aaa"}, BuildUnordered},
		"dev copy":      {copyOf("dev", "bbb"), release, BuildUnordered},
		"no stamps":     {PluginCopy{State: PluginDifferentBinary}, release, BuildUnknown},
		"unknown state": {PluginCopy{State: PluginUnknown}, release, BuildUnknown},
	} {
		if got := tc.copy.CompareToHooks(tc.hooks); got != tc.want {
			t.Errorf("%s: %s, want %s", name, got, tc.want)
		}
	}
}

// A copy that changes build at the same path changes the key, so a new notice prints.
func TestDifferingPluginsKeyFollowsTheBuild(t *testing.T) {
	row := PluginCopy{Harness: "codex", Path: "/p", State: PluginDifferentBinary, Version: "0.1.3", Commit: "a"}
	first, _ := DifferingPlugins([]PluginCopy{row})
	row.Version = "0.1.5"
	second, _ := DifferingPlugins([]PluginCopy{row})
	if first == second {
		t.Errorf("both builds key as %q", first)
	}
}
