//go:build e2e

package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// rewriteFile replaces every old with new in a file under the sandbox root.
func (s *Sandbox) rewriteFile(path, old, new string) {
	s.t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		s.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.ReplaceAll(string(data), old, new)), 0o644); err != nil {
		s.t.Fatal(err)
	}
}

// codexOrder lists each event's handler commands in the global Codex hooks file,
// with any directory part dropped.
func (s *Sandbox) codexOrder() map[string][]string {
	s.t.Helper()
	var doc struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	data, err := os.ReadFile(filepath.Join(s.Home, ".codex", "hooks.json"))
	if err != nil {
		s.t.Fatal(err)
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		s.t.Fatal(err)
	}
	out := map[string][]string{}
	for event, entries := range doc.Hooks {
		for _, entry := range entries {
			for _, h := range entry.Hooks {
				out[event] = append(out[event], filepath.Base(h.Command))
			}
		}
	}
	return out
}

// TestLegacyStoreKeepsItsFixturesThroughMigration builds a store at the legacy
// .claude/hooks path with stored cases, then requires the replay that migrates it
// to find the same cases on every later run.
//
// pins existing behaviour
func TestLegacyStoreKeepsItsFixturesThroughMigration(t *testing.T) {
	t.Parallel()
	s := NewSandbox(t)
	src := s.path(noVarHook + ".py")
	if err := os.WriteFile(src, []byte(noVarSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	s.MustSteer("register", src, "--rule", noVarRule, "--harness", "claude-code", "--fixtures", s.writeNoVarFixtures(false))
	if err := os.Rename(filepath.Join(s.Repo, ".steer", "hooks"), filepath.Join(s.Repo, ".claude", "hooks")); err != nil {
		t.Fatal(err)
	}
	s.rewriteFile(filepath.Join(s.Repo, ".claude", "settings.json"), ".steer/hooks/", ".claude/hooks/")

	for run := 1; run <= 2; run++ {
		r := s.Steer("fixture", "run", noVarHook)
		if r.Code != 0 || !strings.Contains(r.Output(), "(2 cases)") {
			t.Fatalf("fixture run %d exited %d:\n%s", run, r.Code, r.Output())
		}
	}
	if !fileExists(filepath.Join(s.Repo, ".steer", "hooks", "fixtures", noVarHook+".json")) {
		t.Error("the migrated store holds no fixtures")
	}
	if !fileExists(filepath.Join(s.Repo, ".claude", "hooks", "fixtures", noVarHook+".json")) {
		t.Error("migration removed the legacy fixtures")
	}
}

// TestLegacyStateIsReadInPlaceAndMovedByAWrite seeds the legacy evidence root, runs
// the read commands, and requires none of them to create the neutral root; the first
// write then moves every component and marks it.
//
// pins existing behaviour
func TestLegacyStateIsReadInPlaceAndMovedByAWrite(t *testing.T) {
	t.Parallel()
	s := NewSandbox(t, WithoutStubs())
	legacy := filepath.Join(s.Home, ".claude", "state", "commit-review")
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatal(err)
	}
	record := `{"id":"legacy-rule","tier":"instruction","rule_id":"r","rule_scope":"global","installed_ts":1,"status":"installed"}` + "\n"
	if err := os.WriteFile(filepath.Join(legacy, "interventions.jsonl"), []byte(record), 0o644); err != nil {
		t.Fatal(err)
	}
	neutral := filepath.Join(s.Home, ".steer", "state")
	for _, args := range [][]string{{"status"}, {"status", "--json"}, {"fix", "list", "--json"}, {"fix", "score", "--json"},
		{"commit-gate", "status"}, {"audit", "queue"}, {"check", "blocks"}, {"archive", "list"}} {
		s.Steer(args...)
		if fileExists(neutral) {
			t.Fatalf("steer %q created %s", args, neutral)
		}
	}
	if r := s.MustSteer("fix", "list", "--json"); !strings.Contains(r.Stdout, "legacy-rule") {
		t.Fatalf("fix list did not read the legacy record:\n%s", r.Stdout)
	}

	s.MustSteer("fix", "retire", "legacy-rule", "--reason", "e2e")
	for _, component := range []string{"commit-review", "firing-audits", "mods", "steer-report", "transcript-sessions"} {
		if !fileExists(filepath.Join(neutral, component, ".steer-state")) {
			t.Errorf("%s is not marked migrated after a write", component)
		}
	}
	if data, _ := os.ReadFile(filepath.Join(legacy, "interventions.jsonl")); string(data) != record {
		t.Errorf("the write changed the legacy record:\n%s", data)
	}
}

// TestBinInstallRepointsSteersRegistrationsAndLeavesAWrapper registers the gate as
// a bare command with old reviewer flags and the resolver behind a wrapper, then
// requires bin install to repoint and clean the first and leave the second whole.
//
// pins existing behaviour
func TestBinInstallRepointsSteersRegistrationsAndLeavesAWrapper(t *testing.T) {
	t.Parallel()
	s := NewSandbox(t)
	s.MustSteer("commit-gate", "install", "--harness", "claude-code", "--scope", "project")
	settings := filepath.Join(s.Repo, ".claude", "settings.json")
	gate := s.RegisteredCommand(settings, "commit-gate review")
	resolver := s.RegisteredCommand(settings, "commit-gate backfill")
	wrapper := s.path("wrap.sh") + " commit-gate backfill --if-due --apply --quiet --model old"
	s.rewriteFile(settings, gate, "steer commit-gate review --model old-model --provider agy")
	s.rewriteFile(settings, resolver, wrapper)

	s.MustSteer("bin", "install")
	stable := filepath.Join(s.Home, ".steer", "bin", "steer")
	if got := s.RegisteredCommand(settings, "commit-gate review"); got != stable+" commit-gate review" {
		t.Errorf("gate = %q, want the stable path with no reviewer flags", got)
	}
	if got := s.RegisteredCommand(settings, "commit-gate backfill"); got != wrapper {
		t.Errorf("wrapper = %q, want it untouched", got)
	}
}

// TestAReviewerTimeoutPastTheRegistrationIsAStep raises the reviewer timeout without
// re-registering, and requires the setting to say so and status to name the repair
// until the reinstall moves the harness timeout.
//
// pins existing behaviour
func TestAReviewerTimeoutPastTheRegistrationIsAStep(t *testing.T) {
	t.Parallel()
	s := NewSandbox(t)
	s.MustSteer("config", "set", "reviewer.provider", "claude-code")
	s.MustSteer("commit-gate", "install", "--harness", "claude-code", "--scope", "project")
	if r := s.MustSteer("config", "set", "reviewer.timeout", "600"); !strings.Contains(r.Output(), "Re-run `steer commit-gate install`") {
		t.Errorf("config set did not say the harness timeout stays:\n%s", r.Output())
	}
	const step = "the registered wall clock is 360s, and a review"
	if r := s.MustSteer("status"); !strings.Contains(r.Output(), step) ||
		!strings.Contains(r.Output(), "steer commit-gate install --harness claude-code --scope project") {
		t.Errorf("status does not name the short wall clock:\n%s", r.Output())
	}
	s.MustSteer("commit-gate", "install", "--harness", "claude-code", "--scope", "project")
	if r := s.MustSteer("status"); strings.Contains(r.Output(), "registered wall clock") {
		t.Errorf("the step survived the reinstall:\n%s", r.Output())
	}
}

// TestAStalePluginCopyIsNamedOncePerChange puts a plugin copy with another build in
// Claude Code's cache, and requires the SessionStart notice once, silence while it
// stands, and the notice again only after the copy agreed and then changed.
//
// pins existing behaviour
func TestAStalePluginCopyIsNamedOncePerChange(t *testing.T) {
	t.Parallel()
	s := NewSandbox(t, WithoutStubs())
	s.MustSteer("bin", "install")
	bin := filepath.Join(s.Home, ".claude", "plugins", "cache", "mkt", "steer", "0.1.0", "bin", runtime.GOOS+"-"+runtime.GOARCH)
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	copyPath := filepath.Join(bin, "steer")
	writeCopy := func(content []byte) {
		t.Helper()
		if err := os.WriteFile(copyPath, content, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cadence := func() string {
		t.Helper()
		r := s.MustSteer("report", "cadence")
		s.WaitForRefresh()
		return r.Stdout
	}
	const notice = "could not read the Claude Code plugin's build"

	writeCopy([]byte("old build"))
	if out := cadence(); !strings.Contains(out, notice) {
		t.Fatalf("the first session after the change was not told:\n%s", out)
	}
	if out := cadence(); strings.Contains(out, notice) {
		t.Fatalf("the notice repeated for an unchanged copy:\n%s", out)
	}
	stable, err := os.ReadFile(filepath.Join(s.Home, ".steer", "bin", "steer"))
	if err != nil {
		t.Fatal(err)
	}
	writeCopy(stable)
	if out := cadence(); strings.Contains(out, notice) {
		t.Fatalf("an agreeing copy was named:\n%s", out)
	}
	writeCopy([]byte("other build"))
	if out := cadence(); !strings.Contains(out, notice) {
		t.Fatalf("a copy that differs again was not named:\n%s", out)
	}
}

// TestCodexReRegistrationKeepsEveryHandlersPosition registers the guard runner, the
// gate and the check runner beside operator hooks, runs every install again, and
// requires each handler to keep its position, which Codex keys hook approval by.
//
// pins existing behaviour
func TestCodexReRegistrationKeepsEveryHandlersPosition(t *testing.T) {
	t.Parallel()
	s := NewSandbox(t)
	hooks := filepath.Join(s.Home, ".codex", "hooks.json")
	operator := `{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"echo op-pre"}]}],` +
		`"Stop":[{"hooks":[{"type":"command","command":"echo op-stop"}]}]}}`
	if err := os.WriteFile(hooks, []byte(operator), 0o644); err != nil {
		t.Fatal(err)
	}
	src := s.path(noVarHook + ".py")
	if err := os.WriteFile(src, []byte(noVarSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	install := func(rule string) {
		t.Helper()
		s.MustSteer("register", src, "--rule", rule, "--harness", "codex", "--scope", "global")
		s.MustSteer("commit-gate", "install", "--harness", "codex", "--scope", "global")
		s.MustSteer("check", "install", "--harness", "codex", "--scope", "global")
	}
	install(noVarRule)
	before := s.codexOrder()
	if len(before["PreToolUse"]) != 3 || len(before["Stop"]) != 3 {
		t.Fatalf("setup order = %v", before)
	}
	install(noVarRule + ", in any file")
	s.MustSteer("bin", "install")
	if after := s.codexOrder(); !reflect.DeepEqual(before, after) {
		t.Errorf("re-registration moved a handler:\nbefore %v\nafter  %v", before, after)
	}
}
