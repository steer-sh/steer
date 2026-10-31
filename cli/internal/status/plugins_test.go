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
	codexOld := filepath.Join(home, ".codex", "plugins", "cache", "m", "steer", "0.1.0")
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
