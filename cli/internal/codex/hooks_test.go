package codex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRegisterPreservesUnrelatedKeysAndIsIdempotent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	original := `{"description":"keep","future":{"x":1},"hooks":{"Stop":[{"hooks":[{"type":"command","command":"other"}]}],"PreToolUse":[{"matcher":"view_image","note":"keep","hooks":[{"type":"command","command":"image-hook","futureKey":true}]}]}}`
	if err := os.WriteFile(filepath.Join(home, "hooks.json"), []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := RegisterCommitGate("global", "", "steer commit-gate review-codex", 210); err != nil {
			t.Fatal(err)
		}
	}
	raw, _ := os.ReadFile(filepath.Join(home, "hooks.json"))
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatal(err)
	}
	if root["description"] != "keep" || root["future"] == nil {
		t.Fatalf("top-level keys lost: %s", raw)
	}
	entries := root["hooks"].(map[string]any)["PreToolUse"].([]any)
	if len(entries) != 2 {
		t.Fatalf("PreToolUse entries = %d, want 2: %s", len(entries), raw)
	}
	other := entries[0].(map[string]any)
	if other["note"] != "keep" || other["hooks"].([]any)[0].(map[string]any)["futureKey"] != true {
		t.Fatalf("nested keys lost: %s", raw)
	}
	arming := CommitGateArming("global", "")
	if !arming.Effective() || arming.Timeout != 210 || arming.TrustRecordPresent || !arming.TrustStateKnown {
		t.Fatalf("arming = %#v", arming)
	}
	key := filepath.Join(home, "hooks.json") + ":pre_tool_use:1:0"
	config := "[hooks.state.\"" + key + "\"]\ntrusted_hash = \"sha256:codex-validates-this\"\n"
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	if arming = CommitGateArming("global", ""); !arming.TrustRecordPresent {
		t.Fatalf("trust record not detected: %#v", arming)
	}
}

func TestTrustRecordLookupUsesCurrentEntryIndex(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	original := `{"hooks":{"PreToolUse":[{"matcher":"exec_command","hooks":[{"type":"command","command":"steer commit-gate review-codex"},{"type":"command","command":"sibling"}]},{"matcher":"view_image","hooks":[{"type":"command","command":"other"}]}]}}`
	path := filepath.Join(home, "hooks.json")
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	oldKey := path + ":pre_tool_use:0:0"
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(
		"[hooks.state.\""+oldKey+"\"]\ntrusted_hash = \"sha256:stale\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The gate shares its entry with a sibling, so Register cannot rewrite that entry
	// in place: it appends the current definition behind the unrelated entry. The old
	// index record must not attach to it.
	if _, err := RegisterCommitGate("global", "", "steer commit-gate review-codex", 210); err != nil {
		t.Fatal(err)
	}
	arming := CommitGateArming("global", "")
	if arming.TrustRecordPresent {
		t.Fatalf("stale record at the old entry index was treated as current: %#v", arming)
	}
}

func TestRemovePreservesOtherHooks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	if _, err := RegisterCommitGate("global", "", "steer commit-gate review-codex", 210); err != nil {
		t.Fatal(err)
	}
	if _, err := RemoveCommitGate("global", ""); err != nil {
		t.Fatal(err)
	}
	if CommitGateArming("global", "").Registered {
		t.Fatal("gate remains registered")
	}
}

func TestTrustSectionEscapesWindowsPath(t *testing.T) {
	key := `C:\Users\example\.codex\hooks.json:pre_tool_use:0:0`
	want := `[hooks.state."C:\\Users\\example\\.codex\\hooks.json:pre_tool_use:0:0"]`
	if got := trustSection(key); got != want {
		t.Fatalf("trust section = %q, want %q", got, want)
	}
}

func TestLegacyMatcherRegistrationIsInert(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	legacy := `{"hooks":{"PreToolUse":[{"matcher":"` + LegacyCommitGateMatcher +
		`","hooks":[{"type":"command","command":"steer commit-gate review-codex","timeout":210}]}]}}`
	if err := os.WriteFile(filepath.Join(home, "hooks.json"), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	arming := CommitGateArming("global", "")
	if !arming.Registered || arming.Effective() {
		t.Fatalf("legacy matcher reported as effective: %#v", arming)
	}
	if _, err := RegisterCommitGate("global", "", "steer commit-gate review-codex", 210); err != nil {
		t.Fatal(err)
	}
	if arming = CommitGateArming("global", ""); !arming.Effective() || arming.Matcher != CommitGateMatcher {
		t.Fatalf("re-install did not repair the matcher: %#v", arming)
	}
}

func TestRegisterArmsBothResolverEventsAndRemovesThemTogether(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	path := filepath.Join(home, "hooks.json")
	original := `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"unrelated-stop"}]}]}}`
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := RegisterCommitGateAndResolver("global", "",
			"steer commit-gate review-codex", 210, "steer commit-gate backfill --if-due --apply --quiet", 30); err != nil {
			t.Fatal(err)
		}
	}
	armed, timeout := ResolverArming("global", "")
	if !armed || timeout != 30 {
		t.Fatalf("resolver arming = %v, %d", armed, timeout)
	}
	if !CommitGateArming("global", "").Effective() {
		t.Fatal("the review hook did not arm alongside the resolver")
	}

	raw, _ := os.ReadFile(path)
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatal(err)
	}
	hooks := root["hooks"].(map[string]any)
	if len(hooks["Stop"].([]any)) != 2 || len(hooks["SubagentStop"].([]any)) != 1 {
		t.Fatalf("resolver entries not upserted once per event: %s", raw)
	}

	if _, err := RemoveCommitGateAndResolver("global", ""); err != nil {
		t.Fatal(err)
	}
	if armed, _ = ResolverArming("global", ""); armed {
		t.Fatal("the resolver survived removal")
	}
	if CommitGateArming("global", "").Registered {
		t.Fatal("the review hook survived removal")
	}
	raw, _ = os.ReadFile(path)
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatal(err)
	}
	hooks = root["hooks"].(map[string]any)
	if len(hooks["Stop"].([]any)) != 1 {
		t.Fatalf("an unrelated Stop hook was removed: %s", raw)
	}
	if _, ok := hooks["SubagentStop"]; ok {
		t.Fatalf("an empty SubagentStop list was left behind: %s", raw)
	}
}

func TestRegisterRefusesAnUnrecognisableResolver(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	if _, err := RegisterCommitGateAndResolver("global", "",
		"steer commit-gate review-codex", 210, "some-other-command", 30); err == nil {
		t.Fatal("a resolver command the remover cannot find again was accepted")
	}
}

// The review is the model's whole task, so the registration disables Codex's
// additionalContext spill. Past its 2,500-token default Codex writes the context to a
// temporary file and hands the model a preview, which drops most of the findings.
func TestCommitGateRegistrationDisablesTheContextSpill(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	if _, err := RegisterCommitGate("global", "", "steer commit-gate review-codex", 210); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(home, "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"additionalContextLimit": 0`) {
		t.Fatalf("registration does not disable the spill: %s", raw)
	}
}

// entryCommands lists the first handler's command of each entry under event, in
// file order: the entry index Codex keys a trust record by.
func entryCommands(t *testing.T, path, event string) []string {
	t.Helper()
	var out []string
	for _, raw := range readHooks(t, path)[event].([]any) {
		hook := raw.(map[string]any)["hooks"].([]any)[0].(map[string]any)
		out = append(out, hook["command"].(string))
	}
	return out
}

// Codex keys a trust record by entry index, so a re-registration that moves a Steer
// handler voids the approval of that handler and of the one that takes its place.
func TestReRegistrationKeepsEveryHandlerAtItsIndex(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	path := filepath.Join(home, "hooks.json")
	const gate, resolver, check = "steer commit-gate review-codex", "steer commit-gate backfill", "steer check hook --harness codex"
	if _, err := RegisterCommitGateAndResolver("global", "", gate, 240, resolver, 30); err != nil {
		t.Fatal(err)
	}
	if _, err := RegisterMaster("global", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := RegisterCheckHook("global", "", check, 330); err != nil {
		t.Fatal(err)
	}
	master, err := MasterCommand("global", ".")
	if err != nil {
		t.Fatal(err)
	}
	wantPre, wantStop := []string{gate, master}, []string{resolver, check}

	for _, step := range []struct {
		name string
		run  func() error
	}{
		{"commit gate", func() error {
			_, err := RegisterCommitGateAndResolver("global", "", gate, 240, resolver, 30)
			return err
		}},
		{"master", func() error { _, err := RegisterMaster("global", ""); return err }},
		{"check hook", func() error { _, err := RegisterCheckHook("global", "", check, 330); return err }},
	} {
		if err := step.run(); err != nil {
			t.Fatal(err)
		}
		if got := entryCommands(t, path, "PreToolUse"); strings.Join(got, "\n") != strings.Join(wantPre, "\n") {
			t.Errorf("after re-registering the %s, PreToolUse = %q; want %q", step.name, got, wantPre)
		}
		if got := entryCommands(t, path, "Stop"); strings.Join(got, "\n") != strings.Join(wantStop, "\n") {
			t.Errorf("after re-registering the %s, Stop = %q; want %q", step.name, got, wantStop)
		}
	}
}

// The resolver's handlers are approved one by one, apart from the gate's, so its
// trust is read at its own Stop and SubagentStop keys and needs both.
func TestResolverTrustIsReadAtItsOwnHandlers(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	if _, err := RegisterCommitGateAndResolver("global", "", "steer commit-gate review-codex", 240, "steer commit-gate backfill", 30); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "hooks.json")
	approve := func(keys ...string) {
		t.Helper()
		config := ""
		for _, key := range keys {
			config += "[hooks.state.\"" + path + ":" + key + "\"]\nenabled = true\n"
		}
		if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	approve("pre_tool_use:0:0")
	if a := ResolverArmingRow("global", ""); !a.Registered || !a.TrustStateKnown || a.TrustRecordPresent {
		t.Fatalf("the gate's approval was read as the resolver's: %#v", a)
	}
	approve("pre_tool_use:0:0", "stop:0:0")
	if a := ResolverArmingRow("global", ""); a.TrustRecordPresent {
		t.Fatalf("one of the two resolver handlers read as approved for both: %#v", a)
	}
	approve("stop:0:0", "subagent_stop:0:0")
	if a := ResolverArmingRow("global", ""); !a.TrustRecordPresent {
		t.Fatalf("both resolver handlers are approved: %#v", a)
	}
	if a := CommitGateArming("global", ""); a.TrustRecordPresent {
		t.Fatalf("the resolver's approval was read as the gate's: %#v", a)
	}
}
