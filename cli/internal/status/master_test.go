package status

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/codex"
	"github.com/steer-sh/steer-cli/internal/guard"
	"github.com/steer-sh/steer-cli/internal/testhome"
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

// A Codex runner with no approval record is registered and runs no guard; the
// row says so, while Claude Code, which reviews no hook trust, reads not-applicable.
func TestACodexRunnerWithNoApprovalRecordReadsUnapproved(t *testing.T) {
	projectDir(t)
	t.Setenv("CODEX_HOME", t.TempDir())
	if err := (codex.Registrar{}).RegisterMaster(claude.ProjectScope); err != nil {
		t.Fatalf("register master: %v", err)
	}
	row := rowFor(t, MasterRuntimes(claude.ProjectScope), "codex")
	if row.State == MasterNotRegistered || row.Trust != TrustMissing || !row.RunnerUnapproved() {
		t.Fatalf("codex row = %#v", row)
	}
	if !strings.Contains(RunnerTrustPhrase(row), "/hooks") {
		t.Errorf("phrase = %q", RunnerTrustPhrase(row))
	}
	if other := rowFor(t, MasterRuntimes(claude.ProjectScope), "claude-code"); other.RunnerUnapproved() {
		t.Errorf("an unregistered Claude Code row reads unapproved: %#v", other)
	}
}

// A rule written while only Claude Code was installed reads as missing from the
// Codex document, and a harness that is not installed gets no row at all.
func TestRulesPresenceCoversInstalledHarnessesOnly(t *testing.T) {
	root := projectDir(t)
	testhome.Set(t, root)
	if err := os.MkdirAll(filepath.Join(root, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(root, "no-pi"))

	if _, err := claude.InstallManagedRule(claude.ProjectScope, "config-path",
		"resolve a config path from the repo root", claude.Registrar{}); err != nil {
		t.Fatal(err)
	}

	rows := RulesPresences(claude.ProjectScope, guardRegistrars())
	if len(rows) != 2 {
		t.Fatalf("rows = %#v, want Claude Code and Codex only", rows)
	}
	for _, row := range rows {
		switch row.Harness {
		case "claude-code":
			if row.State != RulesCurrent || row.Expected != 1 {
				t.Errorf("claude-code row = %#v", row)
			}
		case "codex":
			if row.State != RulesDrifted || len(row.Missing) != 1 || row.Missing[0] != "config-path" || !row.NeedsSync() {
				t.Errorf("codex row = %#v", row)
			}
		default:
			t.Errorf("unexpected row for %s", row.Harness)
		}
	}
}

// A clone carries a committed store and none of this machine's registrations.
// Every installed harness then runs none of the store's guards and reads
// detached; a harness that is not installed stays not-registered; and a sync
// brings the row back to the master's own runtime state.
func TestAnInstalledHarnessWithoutTheStoresRegistrationIsDetached(t *testing.T) {
	root := projectDir(t)
	testhome.Set(t, root)
	if err := os.MkdirAll(filepath.Join(root, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(root, "no-pi"))

	content := []byte("#!/usr/bin/env python3\nimport sys\n\ndef main():\n    sys.exit(0)\n\nif __name__ == '__main__':\n    main()\n")
	if _, err := claude.InstallManagedHook(claude.ProjectScope, claude.InstallManagedHookInput{
		HookName: "no-bare-python", Rule: "use python3 instead", Content: content,
		Metadata: claude.HookInstallMetadata{Source: claude.SourceGenerated},
	}, claude.Registrar{}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(".claude", "settings.json")); err != nil {
		t.Fatal(err)
	}

	rows := MasterRuntimes(claude.ProjectScope)
	for _, id := range []string{"claude-code", "codex"} {
		row := rowFor(t, rows, id)
		if !row.Detached() || row.Store == "" || row.Stale() || row.RunnerUnapproved() {
			t.Fatalf("%s row = %#v", id, row)
		}
		if !strings.Contains(MasterPhrase(row), "steer hooks sync --scope project") {
			t.Errorf("%s phrase = %q", id, MasterPhrase(row))
		}
	}
	if row := rowFor(t, rows, "pi"); row.State != MasterNotRegistered {
		t.Fatalf("pi row = %#v", row)
	}
	key, labels, scopes := DetachedMasters(rows)
	if key == "" || len(labels) != 2 || strings.Join(scopes, ",") != "project" {
		t.Fatalf("key %q, labels %q, scopes %q", key, labels, scopes)
	}

	if _, err := claude.SyncStore(claude.ProjectScope, claude.Registrar{}, codex.Registrar{}); err != nil {
		t.Fatal(err)
	}
	rows = MasterRuntimes(claude.ProjectScope)
	for _, id := range []string{"claude-code", "codex"} {
		if row := rowFor(t, rows, id); row.State != MasterCurrent {
			t.Errorf("%s after sync = %#v", id, row)
		}
	}
	if key, _, _ := DetachedMasters(rows); key != "" {
		t.Errorf("key after sync = %q", key)
	}

	// With the master file gone, sync cannot repair the scope, so no row reads
	// detached; the registered row names the rebuild through its own state.
	store := rowFor(t, rows, "claude-code").Path
	if err := os.Remove(store); err != nil {
		t.Fatal(err)
	}
	rows = MasterRuntimes(claude.ProjectScope)
	if key, _, _ := DetachedMasters(rows); key != "" {
		t.Errorf("a store with no master reads detached: %q", key)
	}
	if row := rowFor(t, rows, "claude-code"); !row.Stale() ||
		!strings.Contains(MasterPhrase(row), "steer hooks rebuild") {
		t.Errorf("claude-code row without a master = %#v", row)
	}
}

// The notice names drifted sections only, rebuilds each scope once, and keys on
// what drifted, so a different missing rule nudges again.
func TestRulesDriftNamesOnlyDriftedSections(t *testing.T) {
	rows := []RulesPresence{
		{Harness: "claude-code", Scope: "global", Path: "/h/CLAUDE.md", Expected: 2, State: RulesCurrent},
		{Harness: "codex", Scope: "global", Path: "/h/AGENTS.md", Expected: 2, Missing: []string{"config-path"}, State: RulesDrifted},
		{Harness: "pi", Scope: "global", Path: "/h/pi/AGENTS.md", State: RulesUnknown},
		{Harness: "codex", Scope: "project", Path: "AGENTS.md", Extra: []string{"old line"}, State: RulesDrifted},
		{Harness: "pi", Scope: "project", Path: "AGENTS.md", Extra: []string{"old line"}, State: RulesDrifted},
	}
	key, labels, scopes := RulesDrift(rows)
	if len(labels) != 3 || strings.Join(scopes, ",") != "global,project" {
		t.Fatalf("labels = %q, scopes = %q", labels, scopes)
	}
	if !strings.Contains(labels[0], "is missing 1 of 2") || !strings.Contains(labels[1], "1 line(s)") {
		t.Errorf("labels = %q", labels)
	}

	rows[1].Missing = []string{"other-rule"}
	if next, _, _ := RulesDrift(rows); next == key || next == "" {
		t.Errorf("a different missing rule kept the key %q", key)
	}
	if none, _, _ := RulesDrift(rows[:1]); none != "" {
		t.Errorf("a current section produced the key %q", none)
	}

	// The same relative project path in two repositories is two conditions.
	project := rows[3:4]
	projectDir(t)
	first, _, _ := RulesDrift(project)
	projectDir(t)
	if second, _, _ := RulesDrift(project); second == first {
		t.Errorf("two repositories share the key %q", first)
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
