package codex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestProjectHookMutationsRefuseSymlinks(t *testing.T) {
	operations := []struct {
		name string
		run  func(string) error
	}{
		{"register gate and resolver", func(dir string) error {
			_, err := RegisterCommitGateAndResolver("project", dir, "steer commit-gate review-codex", 210, "steer commit-gate backfill", 30)
			return err
		}},
		{"remove gate and resolver", func(dir string) error {
			_, _, err := RemoveCommitGateAndResolver("project", dir)
			return err
		}},
		{"register master", func(dir string) error {
			_, err := RegisterMaster("project", dir)
			return err
		}},
		{"remove master", func(dir string) error {
			_, err := RemoveMaster("project", dir)
			return err
		}},
		{"register check", func(dir string) error {
			_, err := RegisterCheckHook("project", dir, "steer check hook --harness codex", 330)
			return err
		}},
		{"remove check", func(dir string) error {
			_, _, err := RemoveCheckHook("project", dir)
			return err
		}},
	}
	for _, link := range []string{"directory", "file"} {
		for _, op := range operations {
			t.Run(link+"/"+op.name, func(t *testing.T) {
				project, external := t.TempDir(), t.TempDir()
				if _, err := RegisterCommitGateAndResolver("project", external, "old-steer commit-gate review-codex", 90, "old-steer commit-gate backfill", 15); err != nil {
					t.Fatal(err)
				}
				if _, err := RegisterMaster("project", external); err != nil {
					t.Fatal(err)
				}
				if _, err := RegisterCheckHook("project", external, "old-steer check hook --harness codex", 90); err != nil {
					t.Fatal(err)
				}
				dir := filepath.Join(external, ".codex")
				path := filepath.Join(dir, "hooks.json")
				before, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				target, at := dir, filepath.Join(project, ".codex")
				if link == "file" {
					if err := os.Mkdir(at, 0o755); err != nil {
						t.Fatal(err)
					}
					target, at = path, filepath.Join(at, "hooks.json")
				}
				if err := os.Symlink(target, at); err != nil {
					t.Fatal(err)
				}
				if err := op.run(project); err == nil || !strings.Contains(err.Error(), "symlink") {
					t.Fatalf("mutation error = %v, want symlink refusal", err)
				}
				after, err := os.ReadFile(path)
				if err != nil || string(after) != string(before) {
					t.Fatalf("external hooks changed: %v\n%s", err, after)
				}
				if info, err := os.Lstat(at); err != nil || info.Mode()&os.ModeSymlink == 0 {
					t.Fatalf("project symlink replaced: %v", err)
				}
				entries, err := os.ReadDir(dir)
				if err != nil || len(entries) != 1 {
					t.Fatalf("external directory changed: %v, entries %v", err, entries)
				}
			})
		}
	}
}

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
	key := realPath(t, filepath.Join(home, "hooks.json")) + ":pre_tool_use:1:0"
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

// Status and guardscope read a project runner with cwd ".", but Codex keys its
// approval by the absolute hooks.json path.
func TestProjectRunnerTrustIsReadAtTheAbsolutePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	repo := t.TempDir()
	t.Chdir(repo)
	if _, err := RegisterMaster("project", "."); err != nil {
		t.Fatal(err)
	}
	if a := MasterArming("project", "."); !a.Registered || a.TrustRecordPresent {
		t.Fatalf("before approval: %#v", a)
	}
	abs, err := filepath.Abs(filepath.Join(".codex", "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	config := "[hooks.state." + strconv.Quote(realPath(t, abs)+":pre_tool_use:0:0") + "]\nenabled = true\n"
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	if a := MasterArming("project", "."); !a.TrustStateKnown || !a.TrustRecordPresent {
		t.Fatalf("an approved project runner read as unapproved: %#v", a)
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
	for _, event := range resolverEvents {
		entries := hooks[event].([]any)
		handler := entries[len(entries)-1].(map[string]any)["hooks"].([]any)[0].(map[string]any)
		if handler["statusMessage"] != ResolverStatusMessage {
			t.Fatalf("%s resolver handler has no name for /hooks: %s", event, raw)
		}
	}

	if _, _, err := RemoveCommitGateAndResolver("global", ""); err != nil {
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
	path := realPath(t, filepath.Join(home, "hooks.json"))
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

// TestTrustKeysNameBothSpellingsOfALinkedRepository: Codex keys a project by the
// working directory it was given, the link or its target, so either record counts.
func TestTrustKeysNameBothSpellingsOfALinkedRepository(t *testing.T) {
	dir := realPath(t, t.TempDir())
	repo := filepath.Join(dir, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "repo-link")
	if err := os.Symlink(repo, link); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(link, ".codex", "hooks.json")
	got := trustKeys(linked, "PreToolUse", 0, 0)
	want := []string{linked + ":pre_tool_use:0:0", filepath.Join(repo, ".codex", "hooks.json") + ":pre_tool_use:0:0"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("trustKeys = %q; want %q", got, want)
	}
}

// TestTrustKeyKeepsTheHooksFileLink: Codex keys a hooks.json by the folder it loaded
// it from, so a link at the file itself stays in the key.
func TestTrustKeyKeepsTheHooksFileLink(t *testing.T) {
	home := realPath(t, t.TempDir())
	target := filepath.Join(home, "shared.json")
	if err := os.WriteFile(target, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	hooks := filepath.Join(home, "hooks.json")
	if err := os.Symlink(target, hooks); err != nil {
		t.Fatal(err)
	}
	if got, want := trustKeys(hooks, "Stop", 0, 0), hooks+":stop:0:0"; len(got) != 1 || got[0] != want {
		t.Errorf("trustKeys = %q; want only %q", got, want)
	}
}

// realPath is path with symlinks resolved.
func realPath(t *testing.T, path string) string {
	t.Helper()
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return real
}

// TestTrustRecordsNeedOneSpellingForEveryHandler: Codex loads one spelling of a
// hooks.json, so approvals split across two spellings approve neither.
func TestTrustRecordsNeedOneSpellingForEveryHandler(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	handlers := [][]string{{"/link/h.json:stop:0:0", "/real/h.json:stop:0:0"},
		{"/link/h.json:subagent_stop:0:0", "/real/h.json:subagent_stop:0:0"}}
	write := func(keys ...string) {
		t.Helper()
		config := ""
		for _, key := range keys {
			config += trustSection(key) + "\nenabled = true\n"
		}
		if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(handlers[0][0], handlers[1][1])
	if _, present := trustRecords(handlers); present {
		t.Error("approvals split across spellings read as present")
	}
	write(handlers[0][1], handlers[1][1])
	if _, present := trustRecords(handlers); !present {
		t.Error("both handlers approved under the resolved spelling read as absent")
	}
}

// TestTrustKeysKeepALinkedCodexFolder: Codex joins hooks.json to the .codex folder as
// spelled, so a link at that folder adds no resolved spelling.
func TestTrustKeysKeepALinkedCodexFolder(t *testing.T) {
	dir := realPath(t, t.TempDir())
	shared := filepath.Join(dir, "shared")
	repo := filepath.Join(dir, "repo")
	for _, d := range []string{shared, repo} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(shared, filepath.Join(repo, ".codex")); err != nil {
		t.Fatal(err)
	}
	hooks := filepath.Join(repo, ".codex", "hooks.json")
	if got := trustKeys(hooks, "PreToolUse", 0, 0); len(got) != 1 || got[0] != hooks+":pre_tool_use:0:0" {
		t.Errorf("trustKeys = %q; want only the spelling as written", got)
	}
}

// TestRemoveCommitGateReportsWhetherAHandlerWasThere: only a real removal reports true.
func TestRemoveCommitGateReportsWhetherAHandlerWasThere(t *testing.T) {
	dir := t.TempDir()
	if _, removed, err := RemoveCommitGateAndResolver("project", dir); err != nil || removed {
		t.Fatalf("empty: removed, err = %v, %v", removed, err)
	}
	if _, err := RegisterCommitGateAndResolver("project", dir, "steer commit-gate review-codex", 460, "steer commit-gate backfill --if-due", 30); err != nil {
		t.Fatal(err)
	}
	if _, removed, err := RemoveCommitGateAndResolver("project", dir); err != nil || !removed {
		t.Fatalf("armed: removed, err = %v, %v", removed, err)
	}
	if _, removed, err := RemoveCommitGateAndResolver("project", dir); err != nil || removed {
		t.Fatalf("again: removed, err = %v, %v", removed, err)
	}
}

// TestRemoveCommitGateLeavesAnUnrelatedEmptyEvent: an empty event list is not a removal.
func TestRemoveCommitGateLeavesAnUnrelatedEmptyEvent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".codex", "hooks.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	const text = `{"hooks":{"PreToolUse":[]}}`
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, removed, err := RemoveCommitGateAndResolver("project", dir); err != nil || removed {
		t.Fatalf("removed, err = %v, %v", removed, err)
	}
	if got, _ := os.ReadFile(path); string(got) != text {
		t.Errorf("file rewritten to %s", got)
	}
}

// TestRemoveCommitGateFindsAHandlerInASharedEntry: a Steer handler beside another
// handler is still a removal, and the other handler stays.
func TestRemoveCommitGateFindsAHandlerInASharedEntry(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".codex", "hooks.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	const text = `{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"steer commit-gate review-codex"},{"type":"command","command":"echo mine"}]}]}}`
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, removed, err := RemoveCommitGateAndResolver("project", dir); err != nil || !removed {
		t.Fatalf("removed, err = %v, %v", removed, err)
	}
	got, _ := os.ReadFile(path)
	if strings.Contains(string(got), "review-codex") || !strings.Contains(string(got), "echo mine") {
		t.Errorf("file = %s", got)
	}
}

// A registered handler's arming carries its approval, on every channel.
func TestArmingCarriesApproval(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	if _, err := RegisterCommitGateAndResolver("global", "", "steer commit-gate review-codex", 460, "steer commit-gate backfill --if-due", 30); err != nil {
		t.Fatal(err)
	}
	if got := CommitGateArming("global", "").Approval; got != ApprovalMissing {
		t.Errorf("gate approval = %q", got)
	}
	if got := ResolverArmingRow("global", "").Approval; got != ApprovalMissing {
		t.Errorf("resolver approval = %q", got)
	}
}
