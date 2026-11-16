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

	"github.com/steer-sh/steer-cli/internal/checks"
	"github.com/steer-sh/steer-cli/internal/claude"
	codexhooks "github.com/steer-sh/steer-cli/internal/codex"
)

const personalGateCommand = "python3 /Users/someone/.claude/hooks/check-gate.py"

// approvedRepo makes a Git repository the personal gate would have run, with
// the given check.sh body, or none when body is empty.
func approvedRepo(t *testing.T, body string) string {
	t.Helper()
	root := t.TempDir()
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	if body != "" {
		if err := os.WriteFile(filepath.Join(root, checks.OracleName), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func writeApprovals(t *testing.T, home string, paths ...string) {
	t.Helper()
	dir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "# approved repositories\n\n" + strings.Join(paths, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, checks.PersonalApprovalsName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writePersonalGates registers the personal Stop hook in both harnesses, each
// beside an unrelated Stop hook of the operator's.
func writePersonalGates(t *testing.T, home string) (claudeSettings, codexHooks string) {
	t.Helper()
	if err := claude.EnsureDirectories(claude.GlobalScope); err != nil {
		t.Fatal(err)
	}
	settings, err := claude.LoadSettings(claude.GlobalScope)
	if err != nil {
		t.Fatal(err)
	}
	settings.Hooks.Stop = append(settings.Hooks.Stop, claude.StopHookEntry{Hooks: []claude.CommandHook{
		{Type: "command", Command: "python3 ~/.claude/hooks/read-provenance.py", Timeout: 20},
		{Type: "command", Command: personalGateCommand, Timeout: 320},
	}})
	if err := claude.SaveSettings(claude.GlobalScope, settings); err != nil {
		t.Fatal(err)
	}
	claudeSettings, err = claude.GetSettingsFile(claude.GlobalScope)
	if err != nil {
		t.Fatal(err)
	}

	codexHooks = filepath.Join(home, ".codex", "hooks.json")
	if err := os.MkdirAll(filepath.Dir(codexHooks), 0o755); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"hooks":{"Stop":[{"hooks":[
	  {"type":"command","command":"python3 ~/.claude/hooks/codex-plan-review.py","timeout":300},
	  {"type":"command","command":%q,"timeout":330}]}]}}`, personalGateCommand)
	if err := os.WriteFile(codexHooks, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return claudeSettings, codexHooks
}

func runMigrate(t *testing.T, args []string, apply bool) (string, error) {
	t.Helper()
	if err := checkMigrateCmd.Flags().Set("apply", fmt.Sprint(apply)); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	checkMigrateCmd.SetOut(&out)
	t.Cleanup(func() {
		checkMigrateCmd.SetOut(nil)
		_ = checkMigrateCmd.Flags().Set("apply", "false")
	})
	err := checkMigrateCmd.RunE(checkMigrateCmd, args)
	return out.String(), err
}

func read(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// The preview reports what it would import and replace, and changes nothing.
func TestCheckMigratePreviewChangesNothing(t *testing.T) {
	home := fakeHomes(t)
	repo := approvedRepo(t, "#!/usr/bin/env bash\nexit 0\n")
	writeApprovals(t, home, repo)
	settingsPath, codexPath := writePersonalGates(t, home)
	settingsBefore, codexBefore := read(t, settingsPath), read(t, codexPath)

	out, err := runMigrate(t, nil, false)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	for _, want := range []string{approvalImport, repo, checks.PersonalGateScript, "Preview only"} {
		if !strings.Contains(out, want) {
			t.Fatalf("preview does not name %q:\n%s", want, out)
		}
	}
	if read(t, settingsPath) != settingsBefore || read(t, codexPath) != codexBefore {
		t.Fatal("the preview edited a harness registration")
	}
	if adoptions, err := checks.Adoptions(); err != nil || len(adoptions) != 0 {
		t.Fatalf("the preview recorded %d adoptions (err %v)", len(adoptions), err)
	}
}

// Applying adopts the approvals, swaps the personal hook for the Steer runner
// in both harnesses while keeping unrelated hooks, and repeats as a no-op. An
// approval with no check.sh is reported and does not hold the swap back.
func TestCheckMigrateApplyImportsApprovalsAndReplacesTheGate(t *testing.T) {
	home := fakeHomes(t)
	repo := approvedRepo(t, "#!/usr/bin/env bash\nexit 0\n")
	bare := approvedRepo(t, "")
	writeApprovals(t, home, repo, bare, filepath.Join(home, "gone"))
	settingsPath, codexPath := writePersonalGates(t, home)

	out, err := runMigrate(t, nil, true)
	if err != nil {
		t.Fatalf("migrate --apply: %v", err)
	}
	if !strings.Contains(out, approvalNoOracle) || !strings.Contains(out, approvalNoRepo) {
		t.Fatalf("apply does not report the approvals it could not import:\n%s", out)
	}
	identity, err := checks.RepoIdentity(repo)
	if err != nil {
		t.Fatal(err)
	}
	if _, adopted, err := checks.AdoptionOf(identity); err != nil || !adopted {
		t.Fatalf("%s is not adopted after the import (err %v)", identity, err)
	}

	settings, codex := read(t, settingsPath), read(t, codexPath)
	if strings.Contains(settings, checks.PersonalGateScript) || strings.Contains(codex, checks.PersonalGateScript) {
		t.Fatalf("the personal hook is still registered:\n%s\n%s", settings, codex)
	}
	if !strings.Contains(settings, "read-provenance.py") || !strings.Contains(codex, "codex-plan-review.py") {
		t.Fatalf("an unrelated Stop hook was removed:\n%s\n%s", settings, codex)
	}
	claudeArming, err := claude.CheckHookArming(claude.GlobalScope)
	if err != nil || !claudeArming.Registered || claudeArming.Timeout != checks.RegisteredHookTimeout() {
		t.Fatalf("Claude Code runner = %+v (err %v); want the registered runner", claudeArming, err)
	}
	if a := codexhooks.CheckHookArming("global", ""); !a.Registered {
		t.Fatal("the Codex runner was not registered in place of the personal hook")
	}

	again, err := runMigrate(t, nil, true)
	if err != nil {
		t.Fatalf("second migrate --apply: %v", err)
	}
	if !strings.Contains(again, "Nothing to migrate") {
		t.Fatalf("a second apply was not a no-op:\n%s", again)
	}
	if read(t, settingsPath) != settings || read(t, codexPath) != codex {
		t.Fatal("a second apply rewrote the registrations")
	}
}

// Codex keys a hook's approval by its entry position, so the runner takes the
// personal hook's own entry and the operator's later entry keeps its index.
func TestCheckMigrateKeepsCodexEntryPositions(t *testing.T) {
	home := fakeHomes(t)
	writeApprovals(t, home, approvedRepo(t, "#!/usr/bin/env bash\nexit 0\n"))
	codexPath := filepath.Join(home, ".codex", "hooks.json")
	if err := os.MkdirAll(filepath.Dir(codexPath), 0o755); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"hooks":{"Stop":[
	  {"hooks":[{"type":"command","command":%q,"timeout":330}]},
	  {"hooks":[{"type":"command","command":"echo operator-stop"}]}]}}`, personalGateCommand)
	if err := os.WriteFile(codexPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := runMigrate(t, nil, true); err != nil {
		t.Fatalf("migrate --apply: %v", err)
	}
	var doc struct {
		Hooks struct {
			Stop []struct {
				Hooks []struct {
					Command string `json:"command"`
				} `json:"hooks"`
			} `json:"Stop"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(read(t, codexPath)), &doc); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, entry := range doc.Hooks.Stop {
		for _, h := range entry.Hooks {
			got = append(got, h.Command)
		}
	}
	if len(got) != 2 || !strings.Contains(got[0], "check hook") || got[1] != "echo operator-stop" {
		t.Fatalf("Codex Stop entries = %q; want the runner in the personal hook's entry and the operator's after it", got)
	}
}

// An approval that does not adopt keeps the personal hook registered, so its
// repository never loses the gate it had.
func TestCheckMigrateKeepsTheGateWhenAnApprovalDoesNotAdopt(t *testing.T) {
	home := fakeHomes(t)
	repo := approvedRepo(t, "#!/usr/bin/env bash\nexit 1\n")
	writeApprovals(t, home, repo)
	settingsPath, _ := writePersonalGates(t, home)

	out, err := runMigrate(t, nil, true)
	if err != nil {
		t.Fatalf("migrate --apply: %v", err)
	}
	if !strings.Contains(out, "Kept the personal hook") {
		t.Fatalf("a refused import did not keep the personal hook:\n%s", out)
	}
	identity, err := checks.RepoIdentity(repo)
	if err != nil {
		t.Fatal(err)
	}
	if _, adopted, _ := checks.AdoptionOf(identity); adopted {
		t.Fatalf("%s was adopted although its %s failed", identity, checks.OracleName)
	}
	if settings := read(t, settingsPath); !strings.Contains(settings, checks.PersonalGateScript) {
		t.Fatalf("the personal hook was removed while an approval was uncovered:\n%s", settings)
	}
	if a, _ := claude.CheckHookArming(claude.GlobalScope); a.Registered {
		t.Fatal("the runner was registered beside a personal hook that still runs check.sh")
	}
}
