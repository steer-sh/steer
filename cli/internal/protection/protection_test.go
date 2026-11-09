package protection

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/archive"
	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/config"
	"github.com/steer-sh/steer-cli/internal/harness"
	"github.com/steer-sh/steer-cli/internal/testhome"
)

// withHomeAndRepo points the home at a fresh directory and enters a fresh
// repository, so both scopes' stores are the test's own.
func withHomeAndRepo(t *testing.T) (home, repo string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home, repo = filepath.Join(root, "home"), filepath.Join(root, "repo")
	for _, dir := range []string{filepath.Join(home, ".claude"), filepath.Join(repo, ".git")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	testhome.Set(t, home)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	t.Chdir(repo)
	return home, repo
}

var claudeOnly = []harness.Registrar{claude.Registrar{}}

func storeState(t *testing.T) string {
	t.Helper()
	state, err := StoreState(claude.GlobalScope)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func installLegacy(t *testing.T, scope string) {
	t.Helper()
	if _, err := claude.InstallManagedHook(scope, claude.InstallManagedHookInput{
		HookName: HookName, Rule: "Block mutations to factory-managed hooks | Use steer preset sync",
		Content: []byte("def main():\n    return\n"),
		Metadata: claude.HookInstallMetadata{Source: claude.SourceFactory, SourceID: HookName,
			SourceVersion: "v1.0.1", HookVersion: "factory-hook-v3"},
	}, claudeOnly...); err != nil {
		t.Fatal(err)
	}
}

func archived(t *testing.T, scope string) int {
	t.Helper()
	dir, err := claude.GetHooksDir(scope)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := archive.List(dir)
	if err != nil {
		t.Fatal(err)
	}
	return len(entries)
}

// fireMaster sends a Bash call to the global master and returns its reason when it denies.
func fireMaster(t *testing.T, repo, command string) string {
	t.Helper()
	master, err := claude.GetMasterHookPath(claude.GlobalScope)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{"hook_event_name": "PreToolUse", "tool_name": "Bash",
		"tool_input": map[string]any{"command": command}, "cwd": repo})
	cmd := exec.Command("python3", master)
	cmd.Dir = repo
	cmd.Stdin = bytes.NewReader(payload)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("master: %v", err)
	}
	var reply struct {
		HookSpecificOutput struct {
			PermissionDecision       string `json:"permissionDecision"`
			PermissionDecisionReason string `json:"permissionDecisionReason"`
		} `json:"hookSpecificOutput"`
	}
	if len(bytes.TrimSpace(out)) == 0 {
		return ""
	}
	if err := json.Unmarshal(out, &reply); err != nil {
		t.Fatalf("master output %q: %v", out, err)
	}
	if reply.HookSpecificOutput.PermissionDecision != "deny" {
		return ""
	}
	return reply.HookSpecificOutput.PermissionDecisionReason
}

// The setting decides the global guard: on installs it and registers the master in
// the harnesses Steer is set up in, a second pass changes nothing, and off removes it
// without an archive, since this binary carries the copy.
func TestReconcileFollowsTheSetting(t *testing.T) {
	_, repo := withHomeAndRepo(t)

	if change, err := Reconcile(true, nil, Options{Unverified: true}); err != nil || change != Unchanged || storeState(t) != StoreAbsent {
		t.Fatalf("with no harness set up: change %q, err %v, store %s", change, err, storeState(t))
	}
	if change, err := Reconcile(true, claudeOnly, Options{Unverified: true}); err != nil || change != Installed {
		t.Fatalf("install: change %q, err %v", change, err)
	}
	if got := storeState(t); got != StoreCurrent {
		t.Fatalf("store after install = %s", got)
	}
	reason := fireMaster(t, repo, "steer config set protection.self off")
	if !strings.Contains(reason, "config set "+SettingKey+" off") {
		t.Fatalf("the master did not deny turning protection off, or its footer names no way to: %q", reason)
	}
	if fireMaster(t, repo, "steer config set reviewer.effort high") != "" {
		t.Fatal("the master denied an unrelated setting")
	}
	if change, err := Reconcile(true, claudeOnly, Options{Unverified: true}); err != nil || change != Unchanged {
		t.Fatalf("second pass: change %q, err %v", change, err)
	}

	if change, err := Reconcile(false, claudeOnly, Options{Unverified: true}); err != nil || change != Removed {
		t.Fatalf("off: change %q, err %v", change, err)
	}
	if got := storeState(t); got != StoreAbsent {
		t.Fatalf("store after off = %s", got)
	}
	if n := archived(t, claude.GlobalScope); n != 0 {
		t.Fatalf("turning protection off wrote %d archive entries", n)
	}
	if !Enabled(config.Config{}) || Enabled(config.Config{Protection: config.Protection{Self: SettingOff}}) {
		t.Fatal("unset must read as on and off as off")
	}
}

// An earlier Steer's catalog copy in the global store becomes the one guard Steer
// owns now, under the same id, with no second entry left beside it.
func TestReconcileReplacesTheCatalogCopyInPlace(t *testing.T) {
	withHomeAndRepo(t)
	installLegacy(t, claude.GlobalScope)
	if got := storeState(t); got != StoreOutdated {
		t.Fatalf("store holding the catalog copy = %s, want outdated", got)
	}
	if change, err := Reconcile(true, claudeOnly, Options{Unverified: true}); err != nil || change != Updated {
		t.Fatalf("change %q, err %v", change, err)
	}
	manifest, err := claude.LoadManifest(claude.GlobalScope)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Hooks) != 1 || manifest.Hooks[HookName].Source != claude.SourceSteer {
		t.Fatalf("hooks after migration: %+v", manifest.Hooks)
	}
	if legacy, _ := LegacyIn(claude.GlobalScope); legacy {
		t.Fatal("the catalog copy is still recorded")
	}
}

// A guard the user installed under the id is never replaced.
func TestReconcileRefusesAGuardSteerDidNotInstall(t *testing.T) {
	withHomeAndRepo(t)
	if _, err := claude.InstallManagedHook(claude.GlobalScope, claude.InstallManagedHookInput{
		HookName: HookName, Rule: "mine | mine", Content: []byte("def main():\n    return\n"),
		Metadata: claude.HookInstallMetadata{Source: claude.SourceManual},
	}, claudeOnly...); err != nil {
		t.Fatal(err)
	}
	if _, err := Reconcile(true, claudeOnly, Options{Unverified: true}); err == nil {
		t.Fatal("a manual guard under the id was replaced")
	}
	if got := storeState(t); got != StoreForeign {
		t.Fatalf("store = %s, want foreign", got)
	}
}

// A repository's catalog copy is archived and removed; the global guard covers it.
func TestRetireLegacyArchivesTheProjectCopy(t *testing.T) {
	withHomeAndRepo(t)
	installLegacy(t, claude.ProjectScope)
	removed, err := RetireLegacy(claude.ProjectScope, claudeOnly...)
	if err != nil || !removed {
		t.Fatalf("removed %v, err %v", removed, err)
	}
	if legacy, _ := LegacyIn(claude.ProjectScope); legacy {
		t.Fatal("the project still holds the catalog copy")
	}
	if n := archived(t, claude.ProjectScope); n != 1 {
		t.Fatalf("archive entries = %d, want 1", n)
	}
	if removed, err := RetireLegacy(claude.ProjectScope, claudeOnly...); err != nil || removed {
		t.Fatalf("a second retirement removed %v, err %v", removed, err)
	}
}
