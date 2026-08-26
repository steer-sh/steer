package mine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"testing"

	"github.com/steer-sh/steer-cli/internal/gate"
)

// The unit that matters here is the CHAIN, not the ledger row: counting rows inflates
// the denominator, and not deduplicating within a chain inflates the numerator. These
// cases are the port of test_mine.py, which is where the denominator failures live.

func check(t *testing.T, label string, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s: got %#v, want %#v", label, got, want)
	}
}

func row(ts int, phase string) gate.Row {
	return gate.Row{"ts": ts, "phase": phase, "firing": 1, "files": 1}
}
func firing(r gate.Row, f int) gate.Row        { r["firing"] = f; return r }
func host(r gate.Row, h string) gate.Row       { r["host"] = h; return r }
func ledgerTS(r gate.Row, t int) gate.Row      { r["ledger_ts"] = t; return r }
func instrument(r gate.Row, i string) gate.Row { r["instrument"] = i; return r }

func concerns(r gate.Row, join string, cs ...map[string]any) gate.Row {
	list := make([]any, len(cs))
	for i := range cs {
		list[i] = cs[i]
	}
	r["concerns"] = list
	r["ledger_join"] = join
	r["session_id"] = "s1"
	return r
}
func c(text string) map[string]any { return map[string]any{"concern": text, "verdict": "ACCEPTED"} }
func cv(text, v string) map[string]any {
	return map[string]any{"concern": text, "verdict": v}
}

func mustCompile(t *testing.T, expr string) *regexp.Regexp {
	t.Helper()
	p, err := compilePattern(expr)
	if err != nil {
		t.Fatalf("compile %q: %v", expr, err)
	}
	return p
}

func TestChainSegmentation(t *testing.T) {
	rows := []gate.Row{
		row(100, "deny"),
		concerns(row(110, "retry"), "round", c("alpha")),
		firing(row(120, "deny"), 2),
		firing(row(130, "retry"), 2),
		firing(row(140, "clean"), 3),
		row(150, "skip-prose"),
		row(160, "deny"),
		concerns(row(170, "retry"), "round", c("beta")),
		firing(row(180, "cap"), 5),
	}
	chains := BuildChains(rows, false)
	check(t, "two chains from two firing-1 denies", len(chains), 2)
	check(t, "first chain absorbs its retries and clean", len(chains[0].Rows), 5)
	check(t, "first chain terminal", *chains[0].Terminal, "clean")
	check(t, "second chain terminal", *chains[1].Terminal, "cap")
	total := 0
	for _, x := range chains {
		total += len(x.Rows)
	}
	check(t, "skip rows never enter a chain", total, 8)
	check(t, "chain rounds is max firing", chains[0].Rounds, 3)
}

func TestHostPartitioning(t *testing.T) {
	mixed := []gate.Row{
		host(row(100, "deny"), "alpha"),
		host(row(110, "deny"), "beta"),
		host(concerns(row(130, "retry"), "round", c("alpha finding")), "alpha"),
		host(concerns(row(140, "retry"), "round", c("beta finding")), "beta"),
		host(firing(row(160, "deny"), 2), "alpha"),
		host(firing(row(170, "clean"), 2), "beta"),
		host(firing(row(190, "retry"), 2), "alpha"),
		host(firing(row(220, "clean"), 3), "alpha"),
	}
	chains := BuildChains(mixed, false)
	check(t, "one chain per host, not one per interleaving", len(chains), 2)
	check(t, "chains sort by start", []string{chains[0].Host, chains[1].Host}, []string{"alpha", "beta"})
	check(t, "local chain keeps all its rows", len(chains[0].Rows), 5)
	check(t, "remote chain keeps all its rows", len(chains[1].Rows), 3)
	texts := []string{}
	for _, f := range chains[0].Findings {
		texts = append(texts, f.Text)
	}
	check(t, "findings stay with their host", texts, []string{"alpha finding"})
	check(t, "a remote clean is not the local terminal", *chains[0].Terminal, "clean")

	unhosted := BuildChains([]gate.Row{row(10, "deny"), concerns(row(20, "retry"), "round", c("x"))}, false)
	check(t, "unhosted rows share one partition", len(unhosted), 1)
}

func TestCrossHostCollision(t *testing.T) {
	collide := []gate.Row{
		host(row(100, "deny"), "alpha"),
		host(ledgerTS(concerns(row(110, "retry"), "round", c("same text")), 500), "alpha"),
		host(row(120, "deny"), "beta"),
		host(ledgerTS(concerns(row(130, "retry"), "round", c("same text")), 500), "beta"),
	}
	chains := BuildChains(collide, false)
	total := 0
	for _, x := range chains {
		total += len(x.Findings)
	}
	check(t, "a ledger_ts collision across hosts counts twice", total, 2)
}

func TestShardLoading(t *testing.T) {
	dir := t.TempDir()
	writeLine := func(name string, r gate.Row) {
		if err := os.WriteFile(filepath.Join(dir, name), append(mustJSON(t, r), '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeLine("adjudication.alpha.jsonl", row(200, "deny"))
	writeLine("adjudication.beta.jsonl", host(row(100, "deny"), "explicit"))
	writeLine("adjudication.jsonl", row(300, "deny"))
	if err := os.WriteFile(filepath.Join(dir, "adjudication.alpha.jsonl.bak.1"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadRows(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	tss := []int{}
	for _, r := range loaded {
		tss = append(tss, numInt(r["ts"]))
	}
	check(t, "every shard is read, backups are not", tss, []int{100, 200, 300})
	check(t, "an unstamped row takes its shard's name", str(loaded[1]["host"]), "alpha")
	check(t, "a stamped row keeps its own host", str(loaded[0]["host"]), "explicit")
	check(t, "an unsharded legacy ledger reads as unknown", str(loaded[2]["host"]), "unknown")
}

func TestNonReviewPhases(t *testing.T) {
	cleanOnly := BuildChains([]gate.Row{row(200, "clean")}, false)
	check(t, "a first-round clean is its own chain", len(cleanOnly), 1)
	check(t, "a first-round clean carries no findings", len(cleanOnly[0].Findings), 0)

	reentry := BuildChains([]gate.Row{
		row(600, "deny"),
		concerns(row(610, "retry"), "round", c("gamma")),
		firing(row(620, "clean"), 2),
		row(630, "skip-cleared"),
	}, false)
	check(t, "a cleared re-entry opens no chain", len(reentry), 1)
	check(t, "a cleared re-entry joins no chain either", len(reentry[0].Rows), 3)

	ungated := BuildChains([]gate.Row{
		row(700, "deny"),
		concerns(row(710, "retry"), "round", c("delta")),
		firing(row(720, "clean"), 2),
		row(730, "ungated"),
	}, false)
	check(t, "an ungated commit opens no chain", len(ungated), 1)
	check(t, "an ungated commit joins no chain either", len(ungated[0].Rows), 3)

	// The phase set is imported from the engine, so ungated-partial is a non-review
	// phase here without a second copy to keep in step.
	partial := BuildChains([]gate.Row{
		row(800, "deny"),
		concerns(row(810, "retry"), "round", c("eps")),
		firing(row(820, "clean"), 2),
		row(830, "ungated-partial"),
	}, false)
	check(t, "ungated-partial opens no chain", len(partial), 1)
	check(t, "ungated-partial joins no chain either", len(partial[0].Rows), 3)
}

func TestDeduplication(t *testing.T) {
	dupe := BuildChains([]gate.Row{
		row(300, "deny"),
		concerns(row(310, "retry"), "window", c("same defect"), c("other")),
		concerns(row(320, "retry"), "window", c("Same  Defect")),
	}, false)
	check(t, "one chain from the duplicated join", len(dupe), 1)
	check(t, "duplicate finding text collapses", len(dupe[0].Findings), 2)

	across := BuildChains([]gate.Row{
		row(400, "deny"),
		concerns(row(410, "retry"), "round", c("recurring defect")),
		firing(row(420, "clean"), 2),
		row(500, "deny"),
		concerns(row(510, "retry"), "round", c("recurring defect")),
	}, false)
	check(t, "the same text in two chains stays two chains", len(across), 2)
	hits, _, _ := Incidence(across, mustCompile(t, "recurring defect"), "accepted")
	check(t, "recurrence across chains counts twice", hits, 2)

	straddle := BuildChains([]gate.Row{
		row(900, "deny"),
		ledgerTS(concerns(row(910, "retry"), "window", c("one adjudication")), 5000),
		row(920, "deny"),
		ledgerTS(concerns(row(930, "retry"), "window", c("one adjudication")), 5000),
	}, false)
	check(t, "the straddling rows still form two chains", len(straddle), 2)
	hits, _, _ = Incidence(straddle, mustCompile(t, "one adjudication"), "accepted")
	check(t, "one ledger write counts once across chains", hits, 1)

	repeated := BuildChains([]gate.Row{
		row(10, "deny"),
		ledgerTS(concerns(row(11, "retry"), "window", c("repeated across rounds")), 1000),
		firing(row(12, "deny"), 2),
		ledgerTS(firing(concerns(row(13, "retry"), "window", c("repeated across rounds")), 2), 2000),
		row(20, "deny"),
		ledgerTS(concerns(row(21, "retry"), "window", c("repeated across rounds")), 2000),
	}, false)
	check(t, "the repeated-round rows form two chains", len(repeated), 2)
	hits, _, _ = Incidence(repeated, mustCompile(t, "repeated across rounds"), "accepted")
	check(t, "a ledger dropped as an intra-chain duplicate still blocks the next chain", hits, 1)

	distinct := BuildChains([]gate.Row{
		row(900, "deny"),
		ledgerTS(concerns(row(910, "retry"), "round", c("real recurrence")), 5000),
		row(920, "deny"),
		ledgerTS(concerns(row(930, "retry"), "round", c("real recurrence")), 6000),
	}, false)
	hits, _, _ = Incidence(distinct, mustCompile(t, "real recurrence"), "accepted")
	check(t, "distinct ledger writes stay two occurrences", hits, 2)
}

func TestStrictJoins(t *testing.T) {
	mk := func() []gate.Row {
		return []gate.Row{
			row(600, "deny"),
			concerns(row(610, "retry"), "window", c("window only")),
		}
	}
	check(t, "window joins included by default", len(BuildChains(mk(), false)[0].Findings), 1)
	check(t, "strict-joins drops non-round rows", len(BuildChains(mk(), true)[0].Findings), 0)
}

func TestIncidenceIsChainLevel(t *testing.T) {
	dense := BuildChains([]gate.Row{
		row(700, "deny"),
		concerns(row(710, "retry"), "round", c("leak one"), c("leak two"), c("leak three")),
	}, false)
	h, tot, rate := Incidence(dense, mustCompile(t, "leak"), "accepted")
	check(t, "three matches in one chain is one occurrence", []any{h, tot, rate}, []any{1, 1, 1.0})
}

func TestVerdictFiltering(t *testing.T) {
	verdicts := BuildChains([]gate.Row{
		row(800, "deny"),
		concerns(row(810, "retry"), "round", cv("real bug", "ACCEPTED"), cv("false alarm", "REJECTED")),
	}, false)
	h, _, _ := Incidence(verdicts, mustCompile(t, "real bug"), "accepted")
	check(t, "accepted filter", h, 1)
	h, _, _ = Incidence(verdicts, mustCompile(t, "false alarm"), "accepted")
	check(t, "rejected excluded from accepted", h, 0)
	h, _, _ = Incidence(verdicts, mustCompile(t, "false alarm"), "rejected")
	check(t, "rejected filter finds it", h, 1)
	check(t, "both filter", len(ChainHits(verdicts[0], mustCompile(t, "."), "both")), 2)
}

func TestFisher(t *testing.T) {
	check(t, "empty margin is never significant", FisherOneSided(0, 0, 0, 0), 1.0)
	check(t, "no before-hits is never significant", FisherOneSided(0, 20, 0, 20), 1.0)
	if !(FisherOneSided(10, 10, 0, 20) < 0.01) {
		t.Error("clean elimination is significant")
	}
	if !(FisherOneSided(5, 15, 5, 15) > 0.5) {
		t.Error("unchanged rate is not significant")
	}
	if !(FisherOneSided(1, 1, 1, 1) <= 1.0) {
		t.Error("p never exceeds 1")
	}
}

func TestVerdicts(t *testing.T) {
	v := func(bh, bt int, br float64, ah, at int, ar float64) string {
		label, _ := VerdictFor(bh, bt, br, ah, at, ar, 12)
		return label
	}
	check(t, "sparse after-window withholds a verdict", v(5, 20, 0.25, 0, 3, 0.0), "insufficient-data")
	check(t, "a rate that rose is ineffective", v(2, 20, 0.10, 4, 20, 0.20), "ineffective")
	check(t, "an equal rate is ineffective", v(2, 20, 0.10, 2, 20, 0.10), "ineffective")
	check(t, "elimination with volume is effective", v(10, 20, 0.5, 0, 20, 0.0), "effective")
	check(t, "a thin elimination is only promising", v(2, 13, 0.154, 0, 13, 0.0), "promising")
	check(t, "a partial drop is partial", v(10, 20, 0.5, 8, 20, 0.4), "partial")
	check(t, "no baseline is reported, not scored", v(0, 20, 0.0, 0, 20, 0.0), "no-baseline")
}

func TestTrends(t *testing.T) {
	tr := func(bh, bt int, br float64, ah, at int, ar float64) string {
		label, _ := TrendFor(bh, bt, br, ah, at, ar, 12)
		return label
	}
	check(t, "a class that grew is class-grew", tr(2, 20, 0.10, 5, 20, 0.25), "class-grew")
	check(t, "a class that halved is class-faded", tr(10, 20, 0.50, 4, 20, 0.20), "class-faded")
	check(t, "a class holding its rate is class-steady", tr(10, 20, 0.50, 8, 20, 0.40), "class-steady")
	check(t, "a thin after-window withholds a trend", tr(10, 20, 0.50, 0, 3, 0.0), "insufficient-data")
}

func mustJSON(t *testing.T, r gate.Row) []byte {
	t.Helper()
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
