package mine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/testhome"
)

// writeStore places a manifest and its guard file in one scope's neutral store.
func writeStore(t *testing.T, dir, hookID, rule, version, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"hooks":{"` + hookID + `":{"file":"` + hookID + `.py","rule":"` + rule +
		`","hook_version":"` + version + `"}}}`
	if err := os.WriteFile(filepath.Join(dir, "steer-manifest.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, hookID+".py"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSteerHookFactsReadsTheNeutralStoreAndTheImplementationIdentity(t *testing.T) {
	home := t.TempDir()
	testhome.Set(t, home)
	t.Chdir(t.TempDir())
	writeStore(t, filepath.Join(home, ".steer", "hooks"), "no-bare-python", "use python3",
		"factory-hook-v2", "print('v2')\n")

	facts, err := SteerHookFacts("no-bare-python", "")
	if err != nil {
		t.Fatal(err)
	}
	if facts["rule"] != "use python3" || facts["hook_scope"] != "global" ||
		facts["hook_version"] != "factory-hook-v2" || facts["hook_file"] != "no-bare-python.py" {
		t.Fatalf("facts = %#v", facts)
	}
	digest, _ := facts["hook_digest"].(string)
	if len(digest) != 16 {
		t.Fatalf("hook_digest = %q", digest)
	}

	// The digest names the code, so a repair with an unchanged rule moves it alone.
	writeStore(t, filepath.Join(home, ".steer", "hooks"), "no-bare-python", "use python3",
		"factory-hook-v2", "print('repaired')\n")
	repaired, err := SteerHookFacts("no-bare-python", "")
	if err != nil {
		t.Fatal(err)
	}
	if repaired["rule_hash"] != facts["rule_hash"] {
		t.Fatalf("rule hash moved: %v -> %v", facts["rule_hash"], repaired["rule_hash"])
	}
	if repaired["hook_digest"] == facts["hook_digest"] {
		t.Fatalf("hook_digest did not move: %v", repaired["hook_digest"])
	}
}

func TestSteerHookFactsRefusesAnIDHeldByBothStores(t *testing.T) {
	home := t.TempDir()
	testhome.Set(t, home)
	project := t.TempDir()
	t.Chdir(project)
	writeStore(t, filepath.Join(home, ".steer", "hooks"), "shared-id", "global rule", "v1", "g")
	writeStore(t, filepath.Join(project, ".steer", "hooks"), "shared-id", "project rule", "v1", "p")

	if _, err := SteerHookFacts("shared-id", ""); err == nil ||
		!strings.Contains(err.Error(), "--hook-scope") {
		t.Fatalf("err = %v", err)
	}
	facts, err := SteerHookFacts("shared-id", "project")
	if err != nil || facts["rule"] != "project rule" {
		t.Fatalf("facts = %#v, err = %v", facts, err)
	}
}

func TestSteerHookFactsReportsAnAbsentIDAsNoFacts(t *testing.T) {
	testhome.Set(t, t.TempDir())
	t.Chdir(t.TempDir())
	facts, err := SteerHookFacts("absent", "")
	if facts != nil || err != nil {
		t.Fatalf("facts = %#v, err = %v", facts, err)
	}
	if _, err := SteerHookFacts("absent", "everywhere"); err == nil {
		t.Fatal("an invalid scope was accepted")
	}
}

func TestSteerHookFactsReportsACorruptManifestRatherThanAnAbsentHook(t *testing.T) {
	home := t.TempDir()
	testhome.Set(t, home)
	t.Chdir(t.TempDir())
	dir := filepath.Join(home, ".steer", "hooks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "steer-manifest.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := SteerHookFacts("any-hook", ""); err == nil ||
		!strings.Contains(err.Error(), "guard manifest") {
		t.Fatalf("err = %v", err)
	}
}

func TestRegisterRefusesAGuardThatAlreadyBlockedBeforeTheCutoff(t *testing.T) {
	home := t.TempDir()
	testhome.Set(t, home)
	t.Chdir(t.TempDir())
	writeStore(t, filepath.Join(home, ".steer", "hooks"), "no-bare-python", "use python3", "v1", "g")
	opts := func(firings ...Firing) RegisterOpts {
		return RegisterOpts{
			Prose: testProse, Class: "bare", Match: "bare python", Corpus: CorpusTranscript,
			Sessions: []SessionUnit{friction(1000, "claude-code", "proj", "bare python here")},
			Firings:  firings, Tier: "guard", HookID: "no-bare-python", Target: "no-bare-python.py",
			ScopeAll: true, InstalledTS: 2000,
		}
	}
	block := func(scope string, ts float64) Firing {
		return Firing{HookID: "no-bare-python", Scope: scope, Harness: "claude-code", SessionID: "s", TS: ts}
	}

	dir := t.TempDir()
	err := Register(discard{}, dir, opts(block("global", 1700), block("global", 1500)))
	if err == nil || !strings.Contains(err.Error(), "--installed-ts 1500") ||
		!strings.Contains(err.Error(), "2 recorded block(s)") {
		t.Fatalf("err = %v, want a refusal naming the first block", err)
	}
	if records, _ := Load(dir); len(records) != 0 {
		t.Fatalf("a refused register wrote %d record(s)", len(records))
	}

	// A namesake's block, or one at the cutoff, is not this guard's before window.
	if err := Register(discard{}, dir, opts(block("project", 1500), block("global", 2000))); err != nil {
		t.Fatalf("blocks outside the before window refused the record: %v", err)
	}
	// A replacement for a record bound to the same guard opens a fresh window.
	replace := opts(block("global", 1500))
	replace.Supersedes = "bare"
	if err := Register(discard{}, dir, replace); err != nil {
		t.Fatalf("a same-guard replacement was refused: %v", err)
	}
	forced := opts(block("global", 1500))
	forced.Force = true
	if err := Register(discard{}, dir, forced); err != nil {
		t.Fatalf("--force did not record anyway: %v", err)
	}

	// A block the record's scope excludes never reached its baseline.
	elsewhere := block("global", 1500)
	elsewhere.Repository = "/work/other-repo"
	repo := opts(elsewhere)
	repo.ScopeAll, repo.ScopeRepo = false, "^proj$"
	if err := Register(discard{}, dir, repo); err != nil {
		t.Fatalf("a block in another repository refused the record: %v", err)
	}
	other := opts(block("global", 1500))
	other.ScopeAll, other.ScopeHarness = false, []string{"codex"}
	other.Sessions = []SessionUnit{friction(1000, "codex", "proj", "bare python here")}
	if err := Register(discard{}, dir, other); err != nil {
		t.Fatalf("a Claude Code block refused a Codex-scoped record: %v", err)
	}

	// A guard the manifest no longer holds is matched by id in either store.
	gone := opts(Firing{HookID: "removed-guard", Scope: "project", TS: 1500})
	gone.HookID = "removed-guard"
	if err := Register(discard{}, dir, gone); err == nil || !strings.Contains(err.Error(), "--installed-ts 1500") {
		t.Fatalf("err = %v, want an unmanifested guard's earlier block refused", err)
	}
}
