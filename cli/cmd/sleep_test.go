package cmd

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/steer-sh/steer-cli/internal/protection"
	"github.com/steer-sh/steer-cli/internal/sleep"
	"github.com/steer-sh/steer-cli/internal/testhome"
)

// steer sleep sets a sleep that the readers then find, steer wake ends it, and
// steer sleep list shows both; the self-protection guard, and a sleep naming no
// session, are refused.
func TestSleepAndWakeSetAndEndASleepButNeverOfSelfProtection(t *testing.T) {
	testhome.Set(t, t.TempDir())
	run := func(args ...string) (string, error) {
		var stdout bytes.Buffer
		rootCmd.SetOut(&stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs(args)
		err := rootCmd.Execute()
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
		rootCmd.SetArgs(nil)
		sleepSession, sleepMinutes, sleepJSON = "", sleep.DefaultMinutes, false
		return stdout.String(), err
	}
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
