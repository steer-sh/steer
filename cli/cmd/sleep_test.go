package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/hookstore"
	"github.com/steer-sh/steer-cli/internal/protection"
	"github.com/steer-sh/steer-cli/internal/sleep"
	"github.com/steer-sh/steer-cli/internal/testhome"
)

// steer sleep sets a sleep that the readers then find, steer wake ends it, and
// steer sleep list shows both; the self-protection guard, and a sleep naming no
// session, are refused.
func TestSleepAndWakeSetAndEndASleepButNeverOfSelfProtection(t *testing.T) {
	testhome.Set(t, t.TempDir())
	withCmdProjectTempDir(t)
	run := runSleep
	if _, err := run("sleep", "guard", protection.HookName, "--session", "s"); err == nil {
		t.Error("the self-protection guard was put to sleep")
	}
	if _, err := run("sleep", "guard", "g"); err == nil {
		t.Error("a sleep naming no session was set")
	}
	if _, err := run("sleep", "guard", "g", "--session", "s", "--minutes", "3"); err != nil {
		t.Fatal(err)
	}
	rec, live := sleep.Find("s", sleep.KindGuard, "g", time.Now())
	if !live || rec.UntilMS-rec.StartedMS != (3*time.Minute).Milliseconds() {
		t.Fatalf("after steer sleep: %+v live %v; want a 3-minute sleep of g", rec, live)
	}
	if out, err := run("wake", "guard", "g", "--session", "s"); err != nil || !strings.Contains(out, "awake") {
		t.Fatalf("steer wake: %q, %v", out, err)
	}
	if _, live := sleep.Find("s", sleep.KindGuard, "g", time.Now()); live {
		t.Error("g still sleeps after steer wake")
	}
	out, err := run("sleep", "list", "--session", "s", "--json")
	if err != nil || strings.Count(out, `"target": "g"`) != 2 || !strings.Contains(out, `"action": "wake"`) {
		t.Errorf("steer sleep list: %q, %v; want the sleep and the wake", out, err)
	}
}

// runSleep runs one steer command and returns its stdout, resetting the sleep flags.
func runSleep(args ...string) (string, error) {
	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs(args)
	err := rootCmd.Execute()
	rootCmd.SetOut(nil)
	rootCmd.SetErr(nil)
	rootCmd.SetArgs(nil)
	sleepSession, sleepMinutes, sleepJSON, sleepGuardRepos = "", sleep.DefaultMinutes, false, nil
	return stdout.String(), err
}

// steer sleep guard refuses, writing no record and naming the repository, while a
// registered master that runs the guard in a repository --repo names lacks the
// runtime's sleep version; a global master without it that runs other guards, and
// a repository whose master has it, do not refuse. A relative --repo resolves from
// the working directory the command started in.
func TestSleepGuardRefusesWhileItsMasterCannotSleep(t *testing.T) {
	testhome.Set(t, t.TempDir())
	stale := withCmdProjectTempDir(t)
	current := t.TempDir()
	addOrphanFactoryHook(t, claude.GlobalScope, "other", "other", "never use var")
	if _, err := claude.RebuildMaster(claude.GlobalScope); err != nil {
		t.Fatal(err)
	}
	dropSleep(t, claude.GlobalScope)
	for _, dir := range []string{current, stale} {
		if err := os.Chdir(dir); err != nil {
			t.Fatal(err)
		}
		addOrphanFactoryHook(t, claude.ProjectScope, "g", "g", "never commit on main")
		if _, err := claude.RebuildMaster(claude.ProjectScope); err != nil {
			t.Fatal(err)
		}
	}
	dropSleep(t, claude.ProjectScope)
	if err := os.Chdir(filepath.Dir(current)); err != nil {
		t.Fatal(err)
	}

	if _, err := runSleep("sleep", "guard", "g", "--session", "s", "--repo", current, "--repo", filepath.Base(current)); err != nil {
		t.Fatalf("a current project master, beside a global one that does not run g, refused its sleep: %v", err)
	}
	if _, err := runSleep("wake", "guard", "g", "--session", "s"); err != nil {
		t.Fatal(err)
	}
	_, err := runSleep("sleep", "guard", "g", "--session", "s", "--repo", current, "--repo", stale)
	if err == nil || !strings.Contains(err.Error(), "steer hooks rebuild --scope project") ||
		!strings.Contains(err.Error(), filepath.Base(stale)) {
		t.Fatalf("steer sleep under a master that cannot sleep g: %v; want the rebuild in %s named", err, stale)
	}
	if _, live := sleep.Find("s", sleep.KindGuard, "g", time.Now()); live {
		t.Error("a refused sleep wrote a live record")
	}
}

// dropSleep removes the sleep version from the working directory's master in scope.
func dropSleep(t *testing.T, scope string) {
	t.Helper()
	master, err := hookstore.MasterPath(scope)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(master)
	if err != nil {
		t.Fatal(err)
	}
	marker := "\nSTEER_SLEEP_VERSION = " + strconv.Itoa(sleep.Version) + "\n"
	if !strings.Contains(string(data), marker) {
		t.Fatalf("the %s master declares no%s", scope, marker)
	}
	if err := os.WriteFile(master, []byte(strings.Replace(string(data), marker, "\n", 1)), 0o755); err != nil {
		t.Fatal(err)
	}
}
