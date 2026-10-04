package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/steer-sh/steer-cli/internal/archive"
	"github.com/steer-sh/steer-cli/internal/checks"
	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/gate/mine"
)

// archiveRepo makes a Git repository with fake homes and returns its root.
func archiveRepo(t *testing.T) string {
	t.Helper()
	fakeHomes(t)
	if out, err := exec.Command("git", "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	root, err := repoRootOf(".")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func runArchiveCmd(t *testing.T, c *cobra.Command, runScope string, args []string, flags map[string]string) (string, error) {
	t.Helper()
	old := scope
	scope = runScope
	t.Cleanup(func() { scope = old })
	for name, value := range flags {
		if err := c.Flags().Set(name, value); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Flags().Set(name, "") })
	}
	var out bytes.Buffer
	c.SetOut(&out)
	t.Cleanup(func() { c.SetOut(nil) })
	err := c.RunE(c, args)
	return out.String(), err
}

func writeArchived(t *testing.T, scope, dir, kind, id, reason string, manifest any, missing ...string) {
	t.Helper()
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := archive.Write(dir, archive.Entry{
		Tier: kind, ID: id, Scope: scope, Reason: reason, Manifest: raw, Missing: missing,
	}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestArchiveListReadsTheThreeStores(t *testing.T) {
	root := archiveRepo(t)
	project, err := claude.GetHooksDir(claude.ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	global, err := claude.GetHooksDir(claude.GlobalScope)
	if err != nil {
		t.Fatal(err)
	}
	writeArchived(t, claude.ProjectScope, project, "guard", "project-guard", "blocks too much", claude.ManifestEntry{}, claude.ArchiveMissingGuardFile)
	writeArchived(t, claude.GlobalScope, global, "instruction", "global-rule", "", claude.RuleEntry{Text: "a rule"})
	writeArchived(t, claude.ProjectScope, checks.Dir(root), "check", "project-check", "flaky on CI", checks.Declaration{ID: "project-check"})

	out, err := runArchiveCmd(t, archiveListCmd, claude.ProjectScope, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"guard  project-guard  project", "reason: blocks too much", "missing: guard file",
		"instruction  global-rule  global",
		"check  project-check  project", "reason: flaky on CI",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("list output lacks %q:\n%s", want, out)
		}
	}
}

func TestArchiveRestoreSelectsAcrossTheHookAndCheckStores(t *testing.T) {
	root := archiveRepo(t)
	if _, err := claude.InstallManagedRule(claude.ProjectScope, "shared", "resolve a config path from the repo root"); err != nil {
		t.Fatal(err)
	}
	if _, err := claude.RemoveManagedRule(claude.ProjectScope, "shared", ""); err != nil {
		t.Fatal(err)
	}
	writeArchived(t, claude.ProjectScope, checks.Dir(root), "check", "shared", "", checks.Declaration{ID: "shared"})

	if _, err := runArchiveCmd(t, archiveRestoreCmd, claude.ProjectScope, []string{"shared"}, nil); err == nil || !strings.Contains(err.Error(), "--tier") {
		t.Fatalf("an id held by a rule and a check was restored without --tier: %v", err)
	}
	if _, err := runArchiveCmd(t, archiveRestoreCmd, claude.GlobalScope, []string{"shared"}, nil); err == nil || !strings.Contains(err.Error(), "no archive entry") {
		t.Fatalf("a global restore read the project stores: %v", err)
	}

	out, err := runArchiveCmd(t, archiveRestoreCmd, claude.ProjectScope, []string{"shared"}, map[string]string{"tier": "prose"})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := claude.LoadManifest(claude.ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Rules["shared"].Text != "resolve a config path from the repo root" {
		t.Fatalf("restored rule = %+v", manifest.Rules["shared"])
	}
	if strings.Contains(out, "--supersedes") {
		t.Fatalf("a restore with no record named one:\n%s", out)
	}
}

// A restore names the live record that measured the fix, and only that one.
func TestArchiveRestoreNamesTheRecordThatMeasuredTheFix(t *testing.T) {
	archiveRepo(t)
	if _, err := claude.InstallManagedRule(claude.GlobalScope, "shared", "resolve a config path from the repo root"); err != nil {
		t.Fatal(err)
	}
	stateDir := gateConfig().StateDir
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	split := time.Now().Add(-time.Hour).Unix()
	records := `{"id":"live-rule","tier":"instruction","rule_id":"shared","rule_scope":"global","installed_ts":` + fmt.Sprint(split) + "}\n" +
		`{"id":"retired-rule","tier":"instruction","rule_id":"shared","rule_scope":"global","installed_ts":` + fmt.Sprint(split) + `,"retired_ts":` + fmt.Sprint(split+1) + "}\n" +
		`{"id":"other-rule","tier":"instruction","rule_id":"other","rule_scope":"global","installed_ts":` + fmt.Sprint(split) + "}\n"
	if err := os.WriteFile(mine.InterventionsPath(stateDir), []byte(records), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := claude.RemoveManagedRule(claude.GlobalScope, "shared", ""); err != nil {
		t.Fatal(err)
	}

	out, err := runArchiveCmd(t, archiveRestoreCmd, claude.GlobalScope, []string{"shared"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "live-rule measured this fix and now shows its removal") ||
		strings.Contains(out, "retired-rule") || strings.Contains(out, "other-rule") {
		t.Fatalf("restore pointer names the wrong records:\n%s", out)
	}

	// A project rule of the same id is a different fix: the global record stays out.
	if _, err := claude.InstallManagedRule(claude.ProjectScope, "shared", "a project rule"); err != nil {
		t.Fatal(err)
	}
	if _, err := claude.RemoveManagedRule(claude.ProjectScope, "shared", ""); err != nil {
		t.Fatal(err)
	}
	out, err = runArchiveCmd(t, archiveRestoreCmd, claude.ProjectScope, []string{"shared"}, map[string]string{"tier": "instruction"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "--supersedes") {
		t.Fatalf("a project restore named the global rule's record:\n%s", out)
	}
}

// A project review entry is read at HEAD, where a committed removal still stands after
// the restore; the record that measured it is named all the same.
func TestArchiveRestoreNamesAProjectReviewRecordBeforeTheRestoreIsCommitted(t *testing.T) {
	root := archiveRepo(t)
	if _, err := claude.InstallAddendum(claude.ProjectScope, "nits", "naming preferences are not findings"); err != nil {
		t.Fatal(err)
	}
	identity, err := checks.RepoIdentity(root)
	if err != nil {
		t.Fatal(err)
	}
	stateDir := gateConfig().StateDir
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	record := `{"id":"review-nits","tier":"review","addendum_id":"nits","addendum_scope":"project","addendum_repo":"` +
		identity + `","installed_ts":` + fmt.Sprint(time.Now().Add(-time.Hour).Unix()) + "}\n"
	if err := os.WriteFile(mine.InterventionsPath(stateDir), []byte(record), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := claude.RemoveAddendum(claude.ProjectScope, "nits", ""); err != nil {
		t.Fatal(err)
	}
	commit := exec.Command("git", "-c", "user.name=t", "-c", "user.email=t@t.invalid", "-c", "core.hooksPath=/dev/null",
		"commit", "-q", "--allow-empty", "-m", "base")
	if out, err := commit.CombinedOutput(); err != nil {
		t.Fatalf("git: %v: %s", err, out)
	}

	out, err := runArchiveCmd(t, archiveRestoreCmd, claude.ProjectScope, []string{"nits"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "review-nits measured this fix") {
		t.Fatalf("the restore did not name the project review record:\n%s", out)
	}
}

// A restored check is declared, and the declaration runs only where check.sh
// reaches it. The pointer appears when the oracle neither runs every check nor
// names this one, which is what a removal that took the check's own line out
// leaves behind.
func TestACheckRestorePointsAtTheOracleThatDoesNotRunIt(t *testing.T) {
	root := archiveRepo(t)
	oracle := func(body string) string {
		if err := os.WriteFile(checks.OraclePath(root), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		printOracleWiring(&out, root, "no-todo")
		return out.String()
	}
	silent := []string{string(checks.Scaffold(root)), "\"$steer\" check exec no-todo || status=1\n"}
	for _, body := range silent {
		if got := oracle(body); got != "" {
			t.Errorf("an oracle that runs the check still got a pointer: %q", got)
		}
	}
	got := oracle("#!/usr/bin/env bash\nmake test\n")
	for _, want := range []string{checks.OracleName, "check exec no-todo", "never executes"} {
		if !strings.Contains(got, want) {
			t.Errorf("the pointer lacks %q: %q", want, got)
		}
	}
	var out bytes.Buffer
	printOracleWiring(&out, "", "no-todo")
	if out.String() != "" {
		t.Errorf("a restore outside a repository got a pointer: %q", out.String())
	}
}

// A retire ends the tracking and leaves the fix installed, so it names the command
// that removes the fix; once the fix is gone it says nothing more.
func TestFixRetireNamesTheFixItLeavesInstalled(t *testing.T) {
	archiveRepo(t)
	if _, err := claude.InstallManagedRule(claude.GlobalScope, "shared", "resolve a config path from the repo root"); err != nil {
		t.Fatal(err)
	}
	stateDir := gateConfig().StateDir
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	records := `{"id":"kept","tier":"instruction","rule_id":"shared","rule_scope":"global","installed_ts":1}` + "\n" +
		`{"id":"gone","tier":"instruction","rule_id":"absent","rule_scope":"global","installed_ts":1}` + "\n"
	if err := os.WriteFile(mine.InterventionsPath(stateDir), []byte(records), 0o644); err != nil {
		t.Fatal(err)
	}
	retire := find(t, interventionCmd, "retire")
	if err := retire.Flags().Set("reason", "no signal"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = retire.Flags().Set("reason", ""); retire.SetErr(nil) })
	for id, want := range map[string]string{
		"kept": "Retired kept: stopping the tracking ends the measurement and removes no rule; to remove it, run: steer rules remove shared --scope global.\n",
		"gone": "",
	} {
		var stderr bytes.Buffer
		retire.SetErr(&stderr)
		if err := retire.RunE(retire, []string{id}); err != nil {
			t.Fatal(err)
		}
		if stderr.String() != want {
			t.Errorf("retire %s told the reader %q, want %q", id, stderr.String(), want)
		}
	}
}

// From a linked worktree, a project record follows its fix to the main checkout. A
// restore in the worktree undoes no removal of that fix, and a retire there names
// the checkout that holds it rather than a command that would act on the worktree.
func TestWorktreeRestoreAndRetireLeaveTheMainCheckoutsRecordAlone(t *testing.T) {
	main := archiveRepo(t)
	git := func(dir string, args ...string) {
		t.Helper()
		c := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t.invalid",
			"-c", "core.hooksPath=/dev/null"}, args...)...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	git(main, "commit", "-q", "--allow-empty", "-m", "base")
	if _, err := claude.InstallManagedRule(claude.ProjectScope, "shared", "a main checkout rule"); err != nil {
		t.Fatal(err)
	}
	identity, err := checks.RepoIdentity(main)
	if err != nil {
		t.Fatal(err)
	}
	stateDir := gateConfig().StateDir
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	record := `{"id":"main-rule","tier":"instruction","rule_id":"shared","rule_scope":"project","rule_repo":"` +
		identity + `","installed_ts":` + fmt.Sprint(time.Now().Add(-time.Hour).Unix()) + "}\n"
	if err := os.WriteFile(mine.InterventionsPath(stateDir), []byte(record), 0o644); err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(t.TempDir(), "wt")
	git(main, "worktree", "add", "-q", wt)
	t.Chdir(wt)

	if _, err := claude.InstallManagedRule(claude.ProjectScope, "shared", "a worktree rule"); err != nil {
		t.Fatal(err)
	}
	if _, err := claude.RemoveManagedRule(claude.ProjectScope, "shared", ""); err != nil {
		t.Fatal(err)
	}
	out, err := runArchiveCmd(t, archiveRestoreCmd, claude.ProjectScope, []string{"shared"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "main-rule") {
		t.Fatalf("a worktree restore named the main checkout's record:\n%s", out)
	}

	retire := find(t, interventionCmd, "retire")
	var stderr bytes.Buffer
	retire.SetErr(&stderr)
	if err := retire.Flags().Set("reason", "no signal"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = retire.Flags().Set("reason", ""); retire.SetErr(nil) })
	if err := retire.RunE(retire, []string{"main-rule"}); err != nil {
		t.Fatal(err)
	}
	want := "removes no rule; it stays installed in " + identity + ", so run its remove command there.\n"
	if !strings.HasSuffix(stderr.String(), want) || strings.Contains(stderr.String(), "steer rules remove") {
		t.Fatalf("retire from a worktree told the reader %q, want it to end %q", stderr.String(), want)
	}
}

// A project review entry is read at HEAD, so after an uncommitted removal the retire
// asks for the commit rather than naming a remove command that has nothing to remove.
func TestFixRetireAsksToCommitAnUncommittedReviewRemoval(t *testing.T) {
	root := archiveRepo(t)
	if _, err := claude.InstallAddendum(claude.ProjectScope, "nits", "naming preferences are not findings"); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-q", "-m", "entry"}} {
		c := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t.invalid", "-c", "core.hooksPath=/dev/null"}, args...)...)
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	identity, err := checks.RepoIdentity(root)
	if err != nil {
		t.Fatal(err)
	}
	stateDir := gateConfig().StateDir
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	record := `{"id":"review-nits","tier":"review","addendum_id":"nits","addendum_scope":"project","addendum_repo":"` + identity + `","installed_ts":1}` + "\n"
	if err := os.WriteFile(mine.InterventionsPath(stateDir), []byte(record), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := claude.RemoveAddendum(claude.ProjectScope, "nits", ""); err != nil {
		t.Fatal(err)
	}

	retire := find(t, interventionCmd, "retire")
	var stderr bytes.Buffer
	retire.SetErr(&stderr)
	if err := retire.Flags().Set("reason", "no signal"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = retire.Flags().Set("reason", ""); retire.SetErr(nil) })
	if err := retire.RunE(retire, []string{"review-nits"}); err != nil {
		t.Fatal(err)
	}
	if got := stderr.String(); !strings.Contains(got, "its removal is not committed yet, so commit ") || strings.Contains(got, "addendum remove") {
		t.Fatalf("retire after an uncommitted review removal told the reader %q", got)
	}
}
