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
	original := `{"hooks":{"PreToolUse":[{"matcher":"exec_command","hooks":[{"type":"command","command":"steer commit-gate review-codex"}]},{"matcher":"view_image","hooks":[{"type":"command","command":"other"}]}]}}`
	path := filepath.Join(home, "hooks.json")
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	oldKey := path + ":pre_tool_use:0:0"
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(
		"[hooks.state.\""+oldKey+"\"]\ntrusted_hash = \"sha256:stale\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Register removes the old gate and appends the current definition, moving it
	// behind the unrelated entry. The old index record must not attach to it.
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
