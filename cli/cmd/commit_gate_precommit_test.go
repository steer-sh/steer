package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestThePreCommitStubLetsTheCommitThrough: the hook an earlier Steer installed runs
// `steer commit-gate precommit`, with reviewer flags when an older Steer wrote it, and
// git refuses the commit on any non-zero exit. The stub succeeds on both forms and
// prints the one line naming the removal command.
func TestThePreCommitStubLetsTheCommitThrough(t *testing.T) {
	for _, args := range [][]string{
		{"commit-gate", "precommit"},
		{"commit-gate", "precommit", "--provider", "codex", "--model", "m"},
	} {
		var stdout, stderr bytes.Buffer
		rootCmd.SetOut(&stdout)
		rootCmd.SetErr(&stderr)
		rootCmd.SetArgs(args)
		err := rootCmd.Execute()
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
		rootCmd.SetArgs(nil)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if stderr.String() != preCommitRemovedNotice+"\n" || stdout.Len() != 0 {
			t.Errorf("%v: stdout %q, stderr %q; want only the notice on stderr", args, stdout.String(), stderr.String())
		}
	}
	if !strings.Contains(preCommitRemovedNotice, "steer commit-gate remove --precommit") {
		t.Errorf("the notice names no removal command: %q", preCommitRemovedNotice)
	}
	if !commitGatePreCommitCmd.Hidden {
		t.Error("the stub is listed in help")
	}
}

// TestRemovePreCommitTakesOnlySteersHook: the removal deletes the hook the pre-commit
// review wrote, including an older one carrying reviewer flags, and leaves a hook that
// is not Steer's or that has lines Steer did not write.
func TestRemovePreCommitTakesOnlySteersHook(t *testing.T) {
	flagged := strings.Replace(buildCommitGatePreCommitScript(),
		"steer commit-gate precommit", "steer commit-gate precommit --provider codex --model m", 1)
	cases := []struct {
		name, text string
		removed    bool
		fails      bool
	}{
		{"the hook Steer wrote", buildCommitGatePreCommitScript(), true, false},
		{"an older hook with reviewer flags", flagged, true, false},
		{"a hook with an added line", buildCommitGatePreCommitScript() + "make lint\n", false, true},
		{"a command appended to the flagged line", strings.Replace(flagged, " --model m", " --model m; make lint", 1), false, true},
		{"the verify hook", buildSteerPreCommitScript(false), false, false},
		{"another tool's hook", "#!/bin/sh\nmake lint\n", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := gitRepo(t)
			hook := filepath.Join(repo, ".git", "hooks", "pre-commit")
			if err := os.MkdirAll(filepath.Dir(hook), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(hook, []byte(tc.text), 0o755); err != nil {
				t.Fatal(err)
			}
			_, err := removeCommitGatePreCommit()
			if (err != nil) != tc.fails {
				t.Errorf("err = %v, want failure %v", err, tc.fails)
			}
			_, statErr := os.Stat(hook)
			if gone := os.IsNotExist(statErr); gone != tc.removed {
				t.Errorf("removed = %v, want %v", gone, tc.removed)
			}
		})
	}

	gitRepo(t)
	if message, err := removeCommitGatePreCommit(); err != nil || !strings.Contains(message, "nothing changed") {
		t.Errorf("with no hook: %q, %v", message, err)
	}

	// A hooks directory outside the git directory may serve other repositories.
	repo := gitRepo(t)
	shared := t.TempDir()
	gitIn(t, repo, "config", "core.hooksPath", shared)
	hook := filepath.Join(shared, "pre-commit")
	if err := os.WriteFile(hook, []byte(buildCommitGatePreCommitScript()), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := removeCommitGatePreCommit(); err == nil {
		t.Error("a shared hooks directory's hook was removed without a refusal")
	}
	if _, err := os.Stat(hook); err != nil {
		t.Errorf("the shared hook is gone: %v", err)
	}
}

// TestUninstallRemovesAnOlderFlaggedPreCommit: uninstall deletes the pre-commit
// review's hook as an older Steer wrote it, with reviewer flags.
func TestUninstallRemovesAnOlderFlaggedPreCommit(t *testing.T) {
	repo := gitRepo(t)
	hook := filepath.Join(repo, ".git", "hooks", "pre-commit")
	flagged := strings.Replace(buildCommitGatePreCommitScript(),
		"steer commit-gate precommit", "steer commit-gate precommit --provider codex", 1)
	if err := os.WriteFile(hook, []byte(flagged), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := runUninstallWith(t, true, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(hook); !os.IsNotExist(err) {
		t.Errorf("the flagged hook is still there: %v", err)
	}
}
