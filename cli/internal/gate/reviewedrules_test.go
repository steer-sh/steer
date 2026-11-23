package gate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/testhome"
)

type ruleSpec struct {
	text   string
	paths  []string
	review bool
}

// rulesManifest renders a manifest holding rules; a spec with review set carries a
// review section with its paths.
func rulesManifest(t *testing.T, rules map[string]ruleSpec) string {
	t.Helper()
	out := map[string]any{}
	for id, r := range rules {
		entry := map[string]any{"text": r.text, "rule_hash": "x", "created": "2026-01-01"}
		if r.review {
			entry["review"] = map[string]any{"paths": r.paths}
		}
		out[id] = entry
	}
	raw, err := json.Marshal(map[string]any{"hooks": map[string]any{}, "rules": out})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func writeGlobalRules(t *testing.T, rules map[string]ruleSpec) {
	t.Helper()
	home := t.TempDir()
	testhome.Set(t, home)
	writeFileAt(t, home, ".steer/hooks/steer-manifest.json", rulesManifest(t, rules))
}

func ruleIDs(rules []ReviewedRule) []string {
	var out []string
	for _, r := range rules {
		out = append(out, r.Scope+":"+r.ID)
	}
	return out
}

// A rule reaches the reviewer only for a commit that changes a path it names; a rule
// with no paths reaches every commit, and a rule with no review section none.
func TestAReviewedRuleReachesOnlyTheCommitsItsPathsMatch(t *testing.T) {
	writeGlobalRules(t, map[string]ruleSpec{
		"tui":   {text: "tui reads status", paths: []string{"cli/internal/tui/"}, review: true},
		"every": {text: "every commit", review: true},
		"plain": {text: "author only"},
	})
	a := LoadAddendum("")
	cases := []struct {
		paths []string
		want  []string
	}{
		{[]string{"cli/internal/tui/model.go"}, []string{"global:every", "global:tui"}},
		{[]string{"cli/cmd/root.go"}, []string{"global:every"}},
	}
	for _, c := range cases {
		if got := ruleIDs(a.RulesFor(c.paths)); !reflect.DeepEqual(got, c.want) {
			t.Errorf("RulesFor(%v) = %v, want %v", c.paths, got, c.want)
		}
	}
	block := a.RulesBlock([]string{"cli/cmd/root.go"})
	if !strings.Contains(block, "- [global:every] every commit") || strings.Contains(block, "tui reads status") ||
		strings.Contains(block, "author only") {
		t.Fatalf("block for an unmatched path:\n%s", block)
	}
}

// The rules block sits in the context slot, ahead of the stock rules it amends.
func TestTheReviewedRulesBlockPrecedesTheStockRules(t *testing.T) {
	a := Addendum{Rules: []ReviewedRule{{Scope: "project", ID: "r", Text: "a rule", Paths: []string{"*.py"}}}}
	prompt := BuildReviewPrompt("msg", "diff", "ctx", a, []string{"a.py"})
	block := strings.Index(prompt, "<reviewed-rules>")
	rules := strings.Index(prompt, "Rules — these are strict:")
	if block < 0 || rules < 0 || block > rules || !strings.Contains(prompt, "RULE: <id>") {
		t.Fatalf("block at %d, rules at %d:\n%s", block, rules, prompt)
	}
	if strings.Contains(BuildReviewPrompt("msg", "diff", "ctx", a, []string{"a.md"}), "<reviewed-rules>") {
		t.Fatal("a commit no rule matched got a rules block")
	}
}

// The commit under review can stage a new reviewed rule; only what HEAD holds reaches
// its reviewer.
func TestAProjectReviewedRuleIsReadFromHead(t *testing.T) {
	manifest := ".steer/hooks/steer-manifest.json"
	repo := buildRepo(t, map[string]string{
		manifest: rulesManifest(t, map[string]ruleSpec{"a": {text: "committed", paths: []string{"*.py"}, review: true}}),
	})
	gitCmd(t, repo, verb, "-qm", "init")
	writeFileAt(t, repo, manifest, rulesManifest(t, map[string]ruleSpec{
		"a": {text: "committed", paths: []string{"*.py"}, review: true},
		"b": {text: "staged", paths: []string{"*.py"}, review: true},
	}))
	gitCmd(t, repo, "add", "-A")
	if got := ruleIDs(LoadAddendum(repo).Rules); !reflect.DeepEqual(got, []string{"project:a"}) {
		t.Fatalf("rules = %v, want the HEAD rule alone", got)
	}
}

// The digest is the configured set's, so commits that match different rules share it;
// a change to the set changes it.
func TestTheDigestCoversTheConfiguredRulesNotTheMatchedOnes(t *testing.T) {
	a := Addendum{Rules: []ReviewedRule{
		{Scope: "global", ID: "py", Text: "py rule", Paths: []string{"*.py"}},
		{Scope: "global", ID: "go", Text: "go rule", Paths: []string{"*.go"}},
	}}
	if a.Digest() == "" {
		t.Fatal("configured rules gave no digest")
	}
	b := a
	b.Rules = []ReviewedRule{a.Rules[0], {Scope: "global", ID: "go", Text: "go rule", Paths: []string{"cli/"}}}
	if a.Digest() == b.Digest() {
		t.Fatal("a changed path left the digest unchanged")
	}
	if (Addendum{}).Digest() != "" {
		t.Fatal("no entries and no rules gave a digest")
	}
}

// A reviewed round's row names the rules its paths matched and counts the findings
// that name each one.
func TestTheEngineStampsAppliedRulesAndRuleFindings(t *testing.T) {
	writeGlobalRules(t, map[string]ruleSpec{
		"py":   {text: "py rule", paths: []string{"*.py"}, review: true},
		"docs": {text: "docs rule", paths: []string{"docs/"}, review: true},
	})
	reply := oneFinding + "    RULE: [global:py]\n\n[2] SEVERITY: NITPICK\n    FILE: a.py\n    CLAIM: c.\n    FAILS WHEN: d.\n"
	h := newHarness(t, findingReply(reply), findingReply("NO FINDINGS"))
	h.handle()
	row := h.lastRow()
	if got, _ := json.Marshal(row["reviewed_rules_applied"]); string(got) != `["global:py"]` {
		t.Fatalf("reviewed_rules_applied = %s", got)
	}
	if got, _ := json.Marshal(row["rule_findings"]); string(got) != `{"global:py":{"BLOCKING":1}}` {
		t.Fatalf("rule_findings = %s", got)
	}
}

// The RULE line is optional: a finding that carries it names its rule, and the block
// after it still parses.
func TestParseFindingsReadsTheOptionalRuleLine(t *testing.T) {
	text := "[1] SEVERITY: SUBSTANTIVE\n    FILE: a.go\n    CLAIM: c.\n    FAILS WHEN: f.\n    RULE: `tui-reads-status`\n" +
		"[2] SEVERITY: NITPICK\n    FILE: b.go\n    CLAIM: c.\n    FAILS WHEN: f.\n"
	findings, err := ParseFindings(text)
	if err != nil {
		t.Fatalf("ParseFindings: %v", err)
	}
	if len(findings) != 2 || findings[0].Rule != "tui-reads-status" || findings[1].Rule != "" {
		t.Fatalf("findings = %+v", findings)
	}
	if CountFindings(text) != 2 {
		t.Fatalf("CountFindings = %d, want 2", CountFindings(text))
	}
	if got := ruleFindings([]string{text}); !reflect.DeepEqual(got, map[string]map[string]int{"tui-reads-status": {"SUBSTANTIVE": 1}}) {
		t.Fatalf("ruleFindings = %v", got)
	}
}

// A prose-only commit is reviewed when a reviewed rule reaches it, by its paths or
// by having none, and skipped as before when no rule reaches it.
func TestAReviewedRuleNamingAProsePathBringsItsCommitToReview(t *testing.T) {
	for _, c := range []struct {
		rule ruleSpec
		want string
	}{
		{ruleSpec{text: "docs rule", paths: []string{"docs/"}, review: true}, "clean"},
		{ruleSpec{text: "every commit", review: true}, "clean"},
		{ruleSpec{text: "code rule", paths: []string{"*.go"}, review: true}, "skip-prose"},
	} {
		writeGlobalRules(t, map[string]ruleSpec{"r": c.rule})
		h := newHarness(t, findingReply("NO FINDINGS"), findingReply("NO FINDINGS"))
		h.repo = buildRepo(t, map[string]string{"docs/guide.md": "# Guide\n"})
		h.handle()
		if got := h.lastRow()["phase"]; got != c.want {
			t.Errorf("rule paths %v: phase = %v, want %s", c.rule.paths, got, c.want)
		}
	}
}

// A file moved out of a rule's directory changes that directory, so the rule reaches
// the commit even though git reports a detected rename by its new path.
func TestAMoveOutOfARulesDirectoryReachesTheRule(t *testing.T) {
	writeGlobalRules(t, map[string]ruleSpec{"tui": {text: "tui rule", paths: []string{"tui/"}, review: true}})
	h := newHarness(t, findingReply("NO FINDINGS"), findingReply("NO FINDINGS"))
	h.repo = buildRepo(t, map[string]string{"tui/model.go": lines(40)})
	gitCmd(t, h.repo, verb, "-qm", "init")
	if err := os.MkdirAll(filepath.Join(h.repo, "core"), 0o755); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, h.repo, "mv", "tui/model.go", "core/model.go")
	h.handle()
	if got, _ := json.Marshal(h.lastRow()["reviewed_rules_applied"]); string(got) != `["global:tui"]` {
		t.Fatalf("reviewed_rules_applied = %s", got)
	}
}
