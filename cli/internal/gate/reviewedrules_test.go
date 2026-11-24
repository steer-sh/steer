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
// its reviewer, and a project rule with no paths reaches every commit.
func TestAProjectReviewedRuleIsReadFromHead(t *testing.T) {
	manifest := ".steer/hooks/steer-manifest.json"
	committed := map[string]ruleSpec{
		"a":     {text: "committed", paths: []string{"*.py"}, review: true},
		"every": {text: "every commit", review: true},
	}
	repo := buildRepo(t, map[string]string{manifest: rulesManifest(t, committed)})
	gitCmd(t, repo, verb, "-qm", "init")
	committed["b"] = ruleSpec{text: "staged", paths: []string{"*.py"}, review: true}
	writeFileAt(t, repo, manifest, rulesManifest(t, committed))
	gitCmd(t, repo, "add", "-A")
	a := LoadAddendum(repo)
	if got := ruleIDs(a.Rules); !reflect.DeepEqual(got, []string{"project:a", "project:every"}) {
		t.Fatalf("rules = %v, want the HEAD rules alone", got)
	}
	if got := ruleIDs(a.RulesFor([]string{"docs/guide.txt"})); !reflect.DeepEqual(got, []string{"project:every"}) {
		t.Fatalf("RulesFor = %v, want the pathless project rule alone", got)
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

// The RULE line is optional: a finding that carries it names its rule, however the
// reviewer wraps the id, and the block after it still parses. A key the round applied
// keeps a wrapper character it ends in.
func TestParseFindingsReadsTheOptionalRuleLine(t *testing.T) {
	for _, c := range []struct {
		line  string
		known []string
		want  string
	}{
		{"`tui-reads-status`", nil, "tui-reads-status"},
		{"[global:py]", nil, "global:py"},
		{"`[global:py]`", nil, "global:py"},
		{"<global:py>", nil, "global:py"},
		{"global:py.", nil, "global:py"},
		{"**global:py**", nil, "global:py"},
		{"[global:py] tui reads status", nil, "global:py"},
		{"", nil, ""},
		{"[global:py_]", []string{"global:py", "global:py_"}, "global:py_"},
		{"global:py_.", []string{"global:py_"}, "global:py_"},
		{"`global:py.`", []string{"global:py."}, "global:py."},
		{"[global:py]", []string{"global:py", "global:py_"}, "global:py"},
		{"[global:other_]", []string{"global:py"}, "global:other_"},
		{"[global:py_]", []string{"global:py"}, "global:py_"},
		{"[global:py_]", nil, "global:py_"},
		{"_global:py_", []string{"global:py", "global:py_"}, "global:py"},
		{"(global:py)", nil, "global:py"},
	} {
		text := "[1] SEVERITY: SUBSTANTIVE\n    FILE: a.go\n    CLAIM: c.\n    FAILS WHEN: f.\n    RULE: " + c.line + "\n" +
			"[2] SEVERITY: NITPICK\n    FILE: b.go\n    CLAIM: c.\n    FAILS WHEN: f.\n"
		findings, err := ParseFindings(text)
		if err != nil {
			t.Fatalf("%q: ParseFindings: %v", c.line, err)
		}
		if len(findings) != 2 || findings[1].Rule != "" || (c.known == nil && findings[0].Rule != c.want) {
			t.Fatalf("%q: findings = %+v", c.line, findings)
		}
		if CountFindings(text) != 2 {
			t.Fatalf("%q: CountFindings = %d, want 2", c.line, CountFindings(text))
		}
		want := map[string]map[string]int{}
		if c.want != "" {
			want[c.want] = map[string]int{"SUBSTANTIVE": 1}
		}
		if got := ruleFindings([]string{text}, c.known); !reflect.DeepEqual(got, want) {
			t.Fatalf("%q: ruleFindings = %v", c.line, got)
		}
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
