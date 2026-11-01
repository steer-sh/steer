package mine

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/checks"
	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/testhome"
)

// writeRuleStore places a manifest carrying one instruction rule in one scope's
// neutral store.
func writeRuleStore(t *testing.T, dir, ruleID, text string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"hooks":{},"rules":{"` + ruleID + `":{"text":"` + text + `","rule_hash":"stale"}}}`
	if err := os.WriteFile(filepath.Join(dir, "steer-manifest.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSteerRuleFactsReadsTheStoreAndRecomputesTheDigest(t *testing.T) {
	home := t.TempDir()
	testhome.Set(t, home)
	t.Chdir(t.TempDir())
	text := "resolve a config path from the repo root"
	writeRuleStore(t, filepath.Join(home, ".steer", "hooks"), "config-path", text)

	facts, err := SteerRuleFacts("config-path", "")
	if err != nil {
		t.Fatal(err)
	}
	if facts["rule"] != text || facts["rule_scope"] != "global" {
		t.Fatalf("facts = %#v", facts)
	}
	sum := sha256.Sum256([]byte(text))
	// The entry's own rule_hash reads "stale". A record must name the text the store
	// holds, so the digest is recomputed rather than copied.
	if facts["rule_hash"] != hex.EncodeToString(sum[:]) {
		t.Fatalf("rule_hash = %v, recomputed over the stored text = %s",
			facts["rule_hash"], hex.EncodeToString(sum[:]))
	}
}

// A rule the store does not name is absence, not a failure. Register turns that
// into its own error naming the repair.
func TestSteerRuleFactsAnswersNothingForAnUnknownRule(t *testing.T) {
	home := t.TempDir()
	testhome.Set(t, home)
	t.Chdir(t.TempDir())
	writeRuleStore(t, filepath.Join(home, ".steer", "hooks"), "config-path", "a rule")

	facts, err := SteerRuleFacts("no-such-rule", "")
	if err != nil || facts != nil {
		t.Fatalf("facts = %#v, err = %v", facts, err)
	}
}

// One id in both stores cannot be resolved by picking one: the two entries carry
// different text, so the digest would freeze prose the record was not made about.
func TestSteerRuleFactsRefusesAnIDInBothStores(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	testhome.Set(t, home)
	t.Chdir(project)
	writeRuleStore(t, filepath.Join(home, ".steer", "hooks"), "config-path", "the global wording")
	writeRuleStore(t, filepath.Join(project, ".steer", "hooks"), "config-path", "the project wording")

	if _, err := SteerRuleFacts("config-path", ""); err == nil ||
		!strings.Contains(err.Error(), "--rule-scope") {
		t.Fatalf("err = %v", err)
	}
	facts, err := SteerRuleFacts("config-path", "project")
	if err != nil || facts["rule"] != "the project wording" {
		t.Fatalf("facts = %#v, err = %v", facts, err)
	}
}

// A project entry records the repository whose store it was read from, so a later
// reader never matches it against another checkout's entry of the same id. A global
// entry has one store per machine and records none.
func TestProjectFactsRecordTheRepository(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	testhome.Set(t, home)
	t.Chdir(project)
	if out, err := exec.Command("git", "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	writeRuleStore(t, filepath.Join(home, ".steer", "hooks"), "global-rule", "a global rule")
	writeRuleStore(t, filepath.Join(project, ".steer", "hooks"), "config-path", "a project rule")
	want, err := checks.RepoIdentity(project)
	if err != nil {
		t.Fatal(err)
	}

	facts, err := SteerRuleFacts("config-path", "project")
	if err != nil || facts["rule_repo"] != want {
		t.Fatalf("project facts = %#v, err = %v; want rule_repo %s", facts, err, want)
	}
	facts, err = SteerRuleFacts("global-rule", "global")
	if _, stamped := facts["rule_repo"]; err != nil || stamped {
		t.Fatalf("global facts = %#v, err = %v; want no rule_repo", facts, err)
	}
}

// normalizeRuleText is claude.NormalizeRule's pre-image, repeated because mine may
// import no harness adapter. The two must agree or a record's digest names text the
// installer never hashed. This test file imports the adapter to pin them, which the
// production rule does not cover and a duplicated formula needs.
func TestRuleDigestMatchesTheInstaller(t *testing.T) {
	for _, text := range []string{
		"  leading and trailing  ",
		"collapsed\tinner\n\nwhitespace",
		"already normal",
	} {
		sum := sha256.Sum256([]byte(normalizeRuleText(text)))
		if got, want := hex.EncodeToString(sum[:]), claude.HashRule(text); got != want {
			t.Errorf("digest of %q = %s, claude.HashRule = %s", text, got, want)
		}
	}
}
