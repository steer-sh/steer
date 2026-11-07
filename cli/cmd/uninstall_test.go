package cmd

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/checks"
	"github.com/steer-sh/steer-cli/internal/claude"
	codexhooks "github.com/steer-sh/steer-cli/internal/codex"
	"github.com/steer-sh/steer-cli/internal/harness"
	"github.com/steer-sh/steer-cli/internal/statestore"
	"github.com/steer-sh/steer-cli/internal/testhome"
)

// uninstallHome arms every global registration uninstall removes, beside a hook and
// instruction text that are not Steer's, in a home outside any repository.
func uninstallHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	testhome.Set(t, home)
	t.Setenv("CODEX_HOME", "")
	t.Chdir(home)
	g := claude.GlobalScope
	if err := claude.RegisterCommitGateAndResolver(g, "steer commit-gate review", 460,
		"steer commit-gate backfill --if-due --apply --quiet", 30); err != nil {
		t.Fatal(err)
	}
	if err := claude.RegisterCheckHook(g, "steer check hook --harness claude-code", 600); err != nil {
		t.Fatal(err)
	}
	if err := claude.RegisterMasterHook(g); err != nil {
		t.Fatal(err)
	}
	if err := claude.RegisterStatusLine(g, "steer statusline "+claude.EncodeWrapped("my-line")); err != nil {
		t.Fatal(err)
	}
	if _, err := codexhooks.RegisterCommitGateAndResolver(g, home, "steer commit-gate review-codex", 460,
		"steer commit-gate backfill --if-due --apply --quiet", 30); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", "CLAUDE.md"), []byte("# Mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := claude.SetRules([]string{"never do x | do y"}, g); err != nil {
		t.Fatal(err)
	}
	settings, err := claude.LoadSettings(g)
	if err != nil {
		t.Fatal(err)
	}
	settings.Hooks.Stop = append(settings.Hooks.Stop, claude.StopHookEntry{
		Hooks: []claude.CommandHook{{Type: "command", Command: "my-stop-hook"}},
	})
	if err := claude.SaveSettings(g, settings); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".steer"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".steer", "config.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return home
}

func runUninstallWith(t *testing.T, apply, purge bool) (string, error) {
	t.Helper()
	uninstallApply, uninstallPurge = apply, purge
	t.Cleanup(func() { uninstallApply, uninstallPurge = false, false })
	var out bytes.Buffer
	uninstallCmd.SetOut(&out)
	t.Cleanup(func() { uninstallCmd.SetOut(nil) })
	err := runUninstall(uninstallCmd, nil)
	return out.String(), err
}

func readFiles(t *testing.T, paths ...string) map[string]string {
	t.Helper()
	got := map[string]string{}
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		got[p] = string(raw)
	}
	return got
}

// TestUninstallWithoutApplyOnlyPrintsThePlan: the plan names each registration and
// writes nothing, even with --purge.
func TestUninstallWithoutApplyOnlyPrintsThePlan(t *testing.T) {
	home := uninstallHome(t)
	files := []string{
		filepath.Join(home, ".claude", "settings.json"),
		filepath.Join(home, ".claude", "CLAUDE.md"),
		filepath.Join(home, ".codex", "hooks.json"),
	}
	before := readFiles(t, files...)
	out, err := runUninstallWith(t, false, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Claude Code, global scope: commit gate", "Codex, global scope: commit gate",
		"Claude Code, global scope: check runner", "Claude Code, global scope: guard runner",
		"Claude Code, global scope: Steer Rules section", "status line (gives back: my-line)",
		"steer uninstall --apply --purge",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the plan does not name %q:\n%s", want, out)
		}
	}
	for path, text := range readFiles(t, files...) {
		if text != before[path] {
			t.Errorf("a plan without --apply rewrote %s", path)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".steer")); err != nil {
		t.Errorf("a plan without --apply deleted ~/.steer: %v", err)
	}
}

// TestUninstallApplyRemovesOnlySteerRegistrations: --apply leaves no global Steer
// registration, gives back the wrapped status line, keeps what is not Steer's, and
// keeps ~/.steer without --purge.
func TestUninstallApplyRemovesOnlySteerRegistrations(t *testing.T) {
	home := uninstallHome(t)
	if _, err := runUninstallWith(t, true, false); err != nil {
		t.Fatal(err)
	}
	left, err := uninstallItems(claude.GlobalScope)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range left {
		t.Errorf("still registered: %s", item.What)
	}
	line, present, mine := claude.StatusLineArmed(claude.GlobalScope)
	if !present || mine || line.Command != "my-line" {
		t.Errorf("status line = %q (present %v, steer's %v), want my-line given back", line.Command, present, mine)
	}
	settings, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(settings), "my-stop-hook") {
		t.Error("uninstall removed a Stop hook that is not Steer's")
	}
	doc, err := os.ReadFile(filepath.Join(home, ".claude", "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(doc), "# Mine") || strings.Contains(string(doc), "Steer Rules") {
		t.Errorf("CLAUDE.md after uninstall:\n%s", doc)
	}
	if _, err := os.Stat(filepath.Join(home, ".steer", "config.json")); err != nil {
		t.Errorf("uninstall without --purge deleted ~/.steer: %v", err)
	}
}

// TestUninstallPurgeDeletesSteerHome: --apply --purge deletes ~/.steer once the
// registrations are gone.
func TestUninstallPurgeDeletesSteerHome(t *testing.T) {
	home := uninstallHome(t)
	if _, err := runUninstallWith(t, true, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".steer")); !os.IsNotExist(err) {
		t.Errorf("~/.steer after --purge: %v", err)
	}
}

// TestUninstallLeavesProjectScopeAndRemovesTheLocalPreCommit: a committed project
// registration is named with its command and left as it is; the pre-commit hook
// under .git is removed.
func TestUninstallLeavesProjectScopeAndRemovesTheLocalPreCommit(t *testing.T) {
	testhome.Set(t, t.TempDir())
	t.Setenv("CODEX_HOME", "")
	repo := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	t.Chdir(repo)
	if err := claude.RegisterCommitGateAndResolver(claude.ProjectScope, "steer commit-gate review", 460,
		"steer commit-gate backfill --if-due --apply --quiet", 30); err != nil {
		t.Fatal(err)
	}
	projectSettings := filepath.Join(repo, ".claude", "settings.json")
	before := readFiles(t, projectSettings)
	preCommit := filepath.Join(repo, ".git", "hooks", "pre-commit")
	if err := os.WriteFile(preCommit, []byte(buildCommitGatePreCommitScript()), 0o755); err != nil {
		t.Fatal(err)
	}

	out, err := runUninstallWith(t, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "steer commit-gate remove --harness claude-code --scope project") {
		t.Errorf("the plan does not name the project gate's command:\n%s", out)
	}
	if readFiles(t, projectSettings)[projectSettings] != before[projectSettings] {
		t.Error("uninstall rewrote the committed project settings")
	}
	if _, err := os.Stat(preCommit); !os.IsNotExist(err) {
		t.Errorf("the Steer pre-commit hook under .git is still there: %v", err)
	}
}

// TestUninstallHarnessLeavesTheOtherAgent: --harness removes that agent's global
// registrations; the other agent's registrations stay.
func TestUninstallHarnessLeavesTheOtherAgent(t *testing.T) {
	uninstallHome(t)
	harnessFlag = "codex"
	t.Cleanup(func() { harnessFlag = "" })
	if _, err := runUninstallWith(t, true, false); err != nil {
		t.Fatal(err)
	}
	left, err := uninstallItems(claude.GlobalScope)
	if err != nil {
		t.Fatal(err)
	}
	var claudeLeft int
	for _, item := range left {
		if item.Harness == harness.Codex {
			t.Errorf("still registered: %s", item.What)
		} else if item.Harness == harness.ClaudeCode {
			claudeLeft++
		}
	}
	if claudeLeft == 0 {
		t.Error("uninstall --harness codex removed Claude Code's registrations")
	}
}

// TestUninstallPurgeRefusesWhileAnotherAgentStillCallsSteer: --purge with --harness
// changes nothing while another agent's registrations still call ~/.steer.
func TestUninstallPurgeRefusesWhileAnotherAgentStillCallsSteer(t *testing.T) {
	home := uninstallHome(t)
	settings := filepath.Join(home, ".claude", "settings.json")
	before := readFiles(t, settings)
	harnessFlag = "codex"
	t.Cleanup(func() { harnessFlag = "" })
	_, err := runUninstallWith(t, true, true)
	if err == nil || !strings.Contains(err.Error(), "--purge") {
		t.Fatalf("err = %v, want a --purge refusal", err)
	}
	if readFiles(t, settings)[settings] != before[settings] {
		t.Error("a refused purge rewrote the Claude Code settings")
	}
	if _, err := os.Stat(filepath.Join(home, ".codex", "hooks.json")); err != nil {
		t.Fatal(err)
	}
	if items, _ := uninstallItems(claude.GlobalScope); len(items) == 0 {
		t.Error("a refused purge removed registrations")
	}
	if _, err := os.Stat(filepath.Join(home, ".steer")); err != nil {
		t.Errorf("a refused purge deleted ~/.steer: %v", err)
	}
}

// gitRepo makes a repository outside the test home and moves into it.
func gitRepo(t *testing.T) string {
	t.Helper()
	testhome.Set(t, t.TempDir())
	t.Setenv("CODEX_HOME", "")
	repo := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	t.Chdir(repo)
	return repo
}

// TestUninstallKeepsAPreCommitItCannotCallItsOwn: a marked hook with an added line,
// or one in a hooks directory outside the repository, is named and kept.
func TestUninstallKeepsAPreCommitItCannotCallItsOwn(t *testing.T) {
	repo := gitRepo(t)
	edited := filepath.Join(repo, ".git", "hooks", "pre-commit")
	if err := os.WriteFile(edited, []byte(buildCommitGatePreCommitScript()+"make lint\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := runUninstallWith(t, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(edited); err != nil || !strings.Contains(out, "differs from the hook Steer writes") {
		t.Errorf("an edited hook was not kept and named (stat %v):\n%s", err, out)
	}

	repo = gitRepo(t)
	shared := t.TempDir()
	if out, err := exec.Command("git", "-C", repo, "config", "core.hooksPath", shared).CombinedOutput(); err != nil {
		t.Fatalf("git config: %v\n%s", err, out)
	}
	hook := filepath.Join(shared, "pre-commit")
	if err := os.WriteFile(hook, []byte(buildCommitGatePreCommitScript()), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err = runUninstallWith(t, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(hook); err != nil || !strings.Contains(out, "other repositories can share") {
		t.Errorf("a hook in a shared hooks directory was not kept and named (stat %v):\n%s", err, out)
	}
}

// TestUninstallPurgeWithHarnessKeepsTheGitHook: a one-agent purge is refused while
// a Steer Git hook still needs the executable.
func TestUninstallPurgeWithHarnessKeepsTheGitHook(t *testing.T) {
	repo := gitRepo(t)
	hook := filepath.Join(repo, ".git", "hooks", "pre-commit")
	if err := os.WriteFile(hook, []byte(buildSteerPreCommitScript(false)), 0o755); err != nil {
		t.Fatal(err)
	}
	harnessFlag = "codex"
	t.Cleanup(func() { harnessFlag = "" })
	if _, err := runUninstallWith(t, true, true); err == nil || !strings.Contains(err.Error(), "Git pre-commit hook") {
		t.Fatalf("err = %v, want a refusal naming the Git hook", err)
	}
	if _, err := os.Stat(hook); err != nil {
		t.Errorf("a refused purge removed the Git hook: %v", err)
	}
}

// TestUninstallFailsOnAHooksFileItCannotRead: a hooks file that does not parse is
// not read as empty; the run fails and ~/.steer stays.
func TestUninstallFailsOnAHooksFileItCannotRead(t *testing.T) {
	home := uninstallHome(t)
	if err := os.WriteFile(filepath.Join(home, ".codex", "hooks.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := runUninstallWith(t, true, true)
	if err == nil || !strings.Contains(err.Error(), "hooks file that cannot be read") {
		t.Fatalf("err = %v, want a failure naming the unreadable hooks file", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".steer")); err != nil {
		t.Errorf("~/.steer was deleted after a failed run: %v", err)
	}
}

// TestUninstallPlanRepeatsTheHarness: the command a one-agent plan prints runs that
// plan, not the plan for every agent.
func TestUninstallPlanRepeatsTheHarness(t *testing.T) {
	uninstallHome(t)
	harnessFlag = "codex"
	t.Cleanup(func() { harnessFlag = "" })
	out, err := runUninstallWith(t, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "steer uninstall --apply --harness codex") {
		t.Errorf("the plan's next command drops --harness:\n%s", out)
	}
}

// TestUnreadableClaudeSettingsLeaveACodexUninstallRunning: a broken file of one agent
// is that agent's item and does not stop the other agent's removal.
func TestUnreadableClaudeSettingsLeaveACodexUninstallRunning(t *testing.T) {
	home := uninstallHome(t)
	if err := os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	harnessFlag = "codex"
	t.Cleanup(func() { harnessFlag = "" })
	if _, err := runUninstallWith(t, true, false); err != nil {
		t.Fatal(err)
	}
	if codexhooks.CommitGateArming(claude.GlobalScope, home).Registered {
		t.Error("the Codex gate is still registered")
	}
}

// TestUninstallPurgeKeepsSteerWhileAKeptHookRunsIt: --purge is refused while a Steer
// Git hook that uninstall keeps still runs steer.
func TestUninstallPurgeKeepsSteerWhileAKeptHookRunsIt(t *testing.T) {
	repo := gitRepo(t)
	hook := filepath.Join(repo, ".git", "hooks", "pre-commit")
	if err := os.WriteFile(hook, []byte(buildSteerPreCommitScript(false)+"make lint\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := runUninstallWith(t, true, true); err == nil || !strings.Contains(err.Error(), "remove the hook first") {
		t.Fatalf("err = %v, want a refusal naming the kept hook", err)
	}
}

// TestUninstallHarnessKeepsASharedRulesDocument: when one agent's instruction
// document links to another's, a one-agent removal keeps the section both read.
func TestUninstallHarnessKeepsASharedRulesDocument(t *testing.T) {
	home := uninstallHome(t)
	agents := filepath.Join(home, ".codex", "AGENTS.md")
	claudeMD := filepath.Join(home, ".claude", "CLAUDE.md")
	if err := os.Rename(claudeMD, agents); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(agents, claudeMD); err != nil {
		t.Fatal(err)
	}
	harnessFlag = "claude-code"
	t.Cleanup(func() { harnessFlag = "" })
	if _, err := runUninstallWith(t, true, false); err != nil {
		t.Fatal(err)
	}
	doc, err := os.ReadFile(agents)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(doc), "Steer Rules") {
		t.Error("a one-agent removal deleted the rules section another agent reads")
	}
}

// TestUninstallKeepsAPreCommitItCannotRead: a hook that exists but cannot be read is
// named, kept, and refuses --purge.
func TestUninstallKeepsAPreCommitItCannotRead(t *testing.T) {
	repo := gitRepo(t)
	hook := filepath.Join(repo, ".git", "hooks", "pre-commit")
	if err := os.Symlink(filepath.Join(repo, "missing-target"), hook); err != nil {
		t.Fatal(err)
	}
	if _, err := runUninstallWith(t, true, true); err == nil || !strings.Contains(err.Error(), "remove the hook first") {
		t.Fatalf("err = %v, want a refusal naming the unreadable hook", err)
	}
	if _, err := os.Lstat(hook); err != nil {
		t.Errorf("the unreadable hook was removed: %v", err)
	}
}

// TestUninstallRemovesThisRepositoryCheckAdoption: the machine-local adoption that
// lets a project runner run check.sh goes with a full uninstall.
func TestUninstallRemovesThisRepositoryCheckAdoption(t *testing.T) {
	repo := gitRepo(t)
	identity, err := checks.RepoIdentity(repo)
	if err != nil {
		t.Fatal(err)
	}
	store := statestore.Resolve(checks.StateComponent)
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"version":1,"action":"adopt","repo":` + strconv.Quote(identity) + `,"at":"2026-11-01T00:00:00Z","steer_version":"t"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "adoptions.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, adopted, err := checks.AdoptionOf(identity); err != nil || !adopted {
		t.Fatalf("setup: adopted %v, err %v", adopted, err)
	}
	if _, err := runUninstallWith(t, true, false); err != nil {
		t.Fatal(err)
	}
	if _, adopted, _ := checks.AdoptionOf(identity); adopted {
		t.Error("the repository is still adopted after uninstall")
	}
}

// TestUninstallHarnessNamesTheSharedDocumentItKeeps: the kept shared section is in
// the plan, not silently absent.
func TestUninstallHarnessNamesTheSharedDocumentItKeeps(t *testing.T) {
	home := uninstallHome(t)
	agents := filepath.Join(home, ".codex", "AGENTS.md")
	claudeMD := filepath.Join(home, ".claude", "CLAUDE.md")
	if err := os.Rename(claudeMD, agents); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(agents, claudeMD); err != nil {
		t.Fatal(err)
	}
	harnessFlag = "claude-code"
	t.Cleanup(func() { harnessFlag = "" })
	out, err := runUninstallWith(t, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "another agent reads this document too") {
		t.Errorf("the plan does not name the kept shared document:\n%s", out)
	}
}
