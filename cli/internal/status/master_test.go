package status

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/guard"
)

// projectDir moves into an empty repository root, so a scope read sees only what
// the test writes.
func projectDir(t *testing.T) string {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to read the working directory: %v", err)
	}
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("failed to enter the temp directory: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	return dir
}

// registerMaster writes a settings.json invoking master at path.
func registerMaster(t *testing.T, root, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, ".claude"), 0o755); err != nil {
		t.Fatalf("failed to create .claude: %v", err)
	}
	settings := []byte(`{"hooks":{"PreToolUse":[{"matcher":"Edit","hooks":[{"type":"command","command":"python3 ` +
		filepath.ToSlash(path) + `"}]}]}}`)
	if err := os.WriteFile(filepath.Join(root, ".claude", "settings.json"), settings, 0o644); err != nil {
		t.Fatalf("failed to write settings.json: %v", err)
	}
}

// TestMasterRuntimeReadsTheRegisteredFile pins the split a migrated machine
// creates: two masters on disk, and the state has to describe the one the
// harness actually invokes.
func TestMasterRuntimeReadsTheRegisteredFile(t *testing.T) {
	root := projectDir(t)

	stale := filepath.Join(root, "legacy", claude.MasterHookName)
	fresh := filepath.Join(root, "neutral", claude.MasterHookName)
	for path, line := range map[string]string{
		stale: guard.StampPrefix + `{"runtime":"deadbeef","version":"0.0.1"}` + "\n",
		fresh: guard.StampLine("9.9.9"),
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("failed to create %s: %v", path, err)
		}
		if err := os.WriteFile(path, []byte("#!/usr/bin/env python3\n"+line), 0o755); err != nil {
			t.Fatalf("failed to write %s: %v", path, err)
		}
	}

	registerMaster(t, root, stale)
	rows := MasterRuntimes(claude.ProjectScope)
	row := rowFor(t, rows, "claude-code")
	if row.State != MasterStale {
		t.Fatalf("state: got %q, want %q", row.State, MasterStale)
	}
	if !row.Stale() {
		t.Error("a stale master does not report as stale")
	}
	if row.Runtime != "deadbeef" || row.Expected != guard.RuntimeDigest() {
		t.Errorf("runtime %q, expected %q", row.Runtime, row.Expected)
	}
	if MasterPhrase(row) == "" {
		t.Error("a stale master renders no phrase")
	}

	registerMaster(t, root, fresh)
	row = rowFor(t, MasterRuntimes(claude.ProjectScope), "claude-code")
	if row.State != MasterCurrent {
		t.Fatalf("state after registering the rebuilt master: got %q, want %q", row.State, MasterCurrent)
	}
	if row.Stale() {
		t.Error("a current master reports as stale")
	}
}

// TestAnUnstampedMasterIsUnknown pins that absence never reads as current: every
// store built before the stamp existed is unstamped, and those are exactly the
// machines that need the rebuild.
func TestAnUnstampedMasterIsUnknown(t *testing.T) {
	root := projectDir(t)
	master := filepath.Join(root, "hooks", claude.MasterHookName)
	if err := os.MkdirAll(filepath.Dir(master), 0o755); err != nil {
		t.Fatalf("failed to create the store: %v", err)
	}
	if err := os.WriteFile(master, []byte("#!/usr/bin/env python3\nimport os\n"), 0o755); err != nil {
		t.Fatalf("failed to write the master: %v", err)
	}
	registerMaster(t, root, master)

	row := rowFor(t, MasterRuntimes(claude.ProjectScope), "claude-code")
	if row.State != MasterUnknown {
		t.Fatalf("state: got %q, want %q", row.State, MasterUnknown)
	}
	if !row.Stale() {
		t.Error("an unstamped master does not report as needing a rebuild")
	}
}

// TestAnUnregisteredHarnessReportsNoMaster keeps a harness that was never
// installed into out of the stale count.
func TestAnUnregisteredHarnessReportsNoMaster(t *testing.T) {
	projectDir(t)
	row := rowFor(t, MasterRuntimes(claude.ProjectScope), "codex")
	if row.State != MasterNotRegistered {
		t.Fatalf("state: got %q, want %q", row.State, MasterNotRegistered)
	}
	if row.Stale() {
		t.Error("an unregistered harness reports as stale")
	}
	if MasterPhrase(row) != "" {
		t.Error("an unregistered harness renders a phrase")
	}
}

func rowFor(t *testing.T, rows []MasterRuntime, harness string) MasterRuntime {
	t.Helper()
	for _, row := range rows {
		if row.Harness == harness {
			return row
		}
	}
	t.Fatalf("no row for harness %q", harness)
	return MasterRuntime{}
}
