package mine

import (
	"fmt"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/gate"
	"github.com/steer-sh/steer-cli/internal/testhome"
)

// harnessChain is one adjudicated chain on a harness, carrying one accepted finding.
func harnessChain(ts int, harness, text string) []gate.Row {
	open := row(ts, "deny")
	open["harness"] = harness
	return []gate.Row{open,
		concerns(row(ts+1, "retry"), "round", c(text)),
		firing(row(ts+2, "clean"), 2)}
}

// registeredBaseline registers the opts and returns the frozen "hits/units" pair.
func registeredBaseline(t *testing.T, stateDir string, o RegisterOpts) string {
	t.Helper()
	o.Prose, o.Class, o.Tier, o.HookID, o.Target = testProse, "widget parser", "guard", "test-guard", "steer-hooks.py"
	if err := Register(discard{}, stateDir, o); err != nil {
		t.Fatal(err)
	}
	recorded, _ := Load(stateDir)
	b := recorded[len(recorded)-1]["baseline"].(map[string]any)
	return fmt.Sprintf("%d/%d", numInt(b["hits"]), numInt(b["units"]))
}

func TestScopedMatchCountsTheChainsRegisterFreezes(t *testing.T) {
	testhome.Set(t, t.TempDir())
	t.Chdir(t.TempDir())
	const cutoff = 2000
	var rows []gate.Row
	for i, h := range []string{"codex", "codex", "codex", "claude-code", "claude-code"} {
		rows = append(rows, harnessChain(1000+i*100, h, "widget parser drops a flag")...)
	}
	rows = append(rows, harnessChain(1500, "codex", "unrelated naming nit")...)
	rows = append(rows, harnessChain(cutoff+100, "codex", "widget parser drops a flag")...)
	rows = append(rows, harnessChain(cutoff+200, "codex", "widget parser drops a flag")...)
	stateDir, ledger := writeLedger(t, rows)

	frozen := registeredBaseline(t, stateDir, RegisterOpts{Ledger: ledger, Match: "widget parser",
		Kind: "defect", ScopeHarness: []string{"codex"}, InstalledTS: cutoff})
	check(t, "register freezes the codex chains before the cutoff", frozen, "3/4")

	var buf strings.Builder
	if err := Match(&buf, stateDir, MatchOpts{Ledger: ledger, Regex: "widget parser", Kind: "accepted",
		ScopeHarness: []string{"codex"}, InstalledTS: cutoff}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "matches 7/8 chains") {
		t.Errorf("the whole corpus stays listed; got:\n%s", out)
	}
	if !strings.Contains(out, "# registration baseline: "+frozen+" chains") {
		t.Errorf("match counts the window register froze (%s); got:\n%s", frozen, out)
	}
	check(t, "chains marked counted", strings.Count(out, "counted in baseline"), 3)
	check(t, "chains marked outside", strings.Count(out, "outside baseline"), 4)
}

func TestScopedMatchCountsTheSessionsRegisterFreezes(t *testing.T) {
	testhome.Set(t, t.TempDir())
	t.Chdir(t.TempDir())
	const cutoff = 2000
	units := []SessionUnit{
		friction(1000, "codex", "proj", "bare python here"),
		friction(1100, "codex", "proj", "an unrelated correction"),
		friction(1200, "claude-code", "proj", "bare python here"),
		spans(friction(1900, "codex", "proj", "bare python here"), cutoff+100),
		friction(cutoff+500, "codex", "proj", "bare python here"),
	}
	frozen := registeredBaseline(t, t.TempDir(), RegisterOpts{Match: "bare python",
		Corpus: CorpusTranscript, Sessions: units, ScopeHarness: []string{"codex"}, InstalledTS: cutoff})
	check(t, "register freezes the codex sessions wholly before the cutoff", frozen, "1/2")

	var buf strings.Builder
	if err := Match(&buf, t.TempDir(), MatchOpts{Regex: "bare python", Corpus: CorpusTranscript,
		Sessions: units, ScopeHarness: []string{"codex"}, InstalledTS: cutoff}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "# registration baseline: "+frozen+" sessions") {
		t.Errorf("match counts the window register froze (%s); got:\n%s", frozen, out)
	}
	check(t, "sessions marked counted", strings.Count(out, "counted in baseline"), 1)
	check(t, "sessions marked outside", strings.Count(out, "outside baseline"), 3)
}

func TestUnscopedMatchSaysItIsNoBaseline(t *testing.T) {
	dir := findingsDir(t, nil, []map[string]any{c("the parser drops a flag")})
	var buf strings.Builder
	if err := Match(&buf, dir, MatchOpts{Regex: "parser", Kind: "accepted"}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "not a registration baseline") {
		t.Errorf("an unscoped count is labelled as no baseline; got:\n%s", out)
	}
	if strings.Contains(out, "counted in baseline") || strings.Contains(out, "outside baseline") {
		t.Errorf("with no window, no unit is marked against one; got:\n%s", out)
	}
}

func TestMatchRefusesAPreviewRegisterCouldNotFreeze(t *testing.T) {
	dir := findingsDir(t, nil, []map[string]any{c("the parser drops a flag")})
	for name, o := range map[string]MatchOpts{
		"a cutoff with no scope": {Kind: "accepted", InstalledTS: 2000},
		"strict joins":           {Kind: "accepted", ScopeAll: true, StrictJoins: true},
		"both verdicts":          {Kind: "both", ScopeAll: true},
	} {
		o.Regex = "parser"
		if err := Match(&strings.Builder{}, dir, o); err == nil {
			t.Errorf("%s: register counts no such window, so match must refuse it", name)
		}
	}
}
