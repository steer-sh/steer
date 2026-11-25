package mine

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/testhome"
)

// A reviewed record binds a rule the reviewer reads: a rule with no review section is
// refused, and one with a section freezes its paths beside its text.
func TestAReviewedRecordNeedsAndFreezesTheRulesReviewSection(t *testing.T) {
	stateDir, ledger := registerRuleFixture(t)
	home := t.TempDir()
	testhome.Set(t, home)
	t.Chdir(t.TempDir())
	store := filepath.Join(home, ".steer", "hooks")
	opts := RegisterOpts{
		Prose: testProse, ScopeFlags: ScopeFlags{ScopeAll: true}, Ledger: ledger,
		Class: "widget parser flag loss", Match: "widget parser", Kind: "defect",
		Tier: "reviewed", Target: "CLAUDE.md", RuleID: "config-path",
		NotMechanizable: testNotMechanizable,
	}

	writeRuleStore(t, store, "config-path", "resolve a config path from the repo root")
	if err := Register(io.Discard, stateDir, opts); err == nil || !strings.Contains(err.Error(), "--review-path") {
		t.Fatalf("a rule with no review section: err = %v", err)
	}

	manifest := `{"hooks":{},"rules":{"config-path":{"text":"resolve a config path from the repo root",` +
		`"rule_hash":"x","review":{"paths":["cli/"]}}}}`
	if err := os.WriteFile(filepath.Join(store, "steer-manifest.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Register(io.Discard, stateDir, opts); err != nil {
		t.Fatal(err)
	}
	recorded, _ := Load(stateDir)
	check(t, "one intervention recorded", len(recorded), 1)
	paths, _ := recorded[0]["rule_review_paths"].([]any)
	if len(paths) != 1 || paths[0] != "cli/" {
		t.Fatalf("rule_review_paths = %#v", recorded[0]["rule_review_paths"])
	}
}

// A project reviewed rule is registered only once HEAD holds it, because the gate
// reads project rules from HEAD.
func TestAProjectReviewedRecordWaitsForTheRuleAtHead(t *testing.T) {
	stateDir, ledger := registerRuleFixture(t)
	testhome.Set(t, t.TempDir())
	repo := t.TempDir()
	t.Chdir(repo)
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q", ".")
	store := filepath.Join(repo, ".steer", "hooks")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"hooks":{},"rules":{"config-path":{"text":"resolve a config path from the repo root",` +
		`"rule_hash":"x","review":{"paths":["cli/"]}}}}`
	if err := os.WriteFile(filepath.Join(store, "steer-manifest.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	opts := RegisterOpts{
		Prose: testProse, ScopeFlags: ScopeFlags{ScopeAll: true}, Ledger: ledger,
		Class: "widget parser flag loss", Match: "widget parser", Kind: "defect",
		Tier: "reviewed", Target: "CLAUDE.md", RuleID: "config-path", RuleScope: "project",
		NotMechanizable: testNotMechanizable,
	}
	if err := Register(io.Discard, stateDir, opts); err == nil || !strings.Contains(err.Error(), "not at HEAD") {
		t.Fatalf("a rule only in the working tree: err = %v", err)
	}
	git("add", "-A")
	git("co"+"mmit", "-qm", "rule")
	if err := Register(io.Discard, stateDir, opts); err != nil {
		t.Fatalf("a rule at HEAD: %v", err)
	}
}
