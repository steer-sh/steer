package mine

import (
	"testing"

	"github.com/steer-sh/steer-cli/internal/gate"
)

func fields(r gate.Row, f map[string]any) gate.Row { r["instrument_fields"] = f; return r }

func reviewerFields(provider, model, effort string, runs int, digest string) map[string]any {
	return map[string]any{
		"provider": provider, "model": model, "effort": effort,
		"runs": runs, "prompt_digest": digest, "engine": "go",
	}
}

// windowDrift grades an after window against a record's registered instrument, the way
// ImpactOf does over one prospective identity: the baseline folds the before window,
// and every after identity is graded against it. It returns the worst grade, the union
// of the named dimensions and the drifting labels.
func windowDrift(authored string, before, after []Chain) (grade string, dims, labels []string) {
	base := BaselineInstrument(authored, InstrumentUses(before))
	for _, u := range InstrumentUses(after) {
		g := gradeDrift(authored, base, u)
		if g == "" {
			continue
		}
		if g == DriftMajor {
			grade = DriftMajor
		} else if grade == "" {
			grade = DriftMinor
		}
		labels = appendUnique(labels, u.Label())
		for _, d := range DriftDims(base, u.Fields) {
			dims = appendUnique(dims, d)
		}
	}
	return grade, dims, labels
}

func TestParseInstrumentIDRecoversTheDimensions(t *testing.T) {
	four := ParseInstrumentID("gemini-3.7-flash/3/aaaa1111/go")
	check(t, "model", four.Model, "gemini-3.7-flash")
	check(t, "runs", four.Runs, 3)
	check(t, "prompt digest", four.PromptDigest, "aaaa1111")
	check(t, "engine", four.Engine, "go")
	check(t, "an unset effort is a known value", four.EffortKnown, true)
	check(t, "the string never held the provider", four.Provider, "")

	five := ParseInstrumentID("gemini-3.7-flash/3/aaaa1111/go/high")
	check(t, "the fifth segment is the effort", five.Effort, "high")

	for _, id := range []string{"modelA/3/aaaa1111", "a/b/c/d", "a/1/b/c/d/e", ""} {
		check(t, "an unreadable id is unknown everywhere: "+id, ParseInstrumentID(id), InstrumentFields{})
	}
}

func TestDriftGradesByDimension(t *testing.T) {
	base := ParseInstrumentID("gemini-3.7-flash/3/aaaa1111/go")
	effort := ParseInstrumentID("gemini-3.7-flash/3/aaaa1111/go/high")
	runs := ParseInstrumentID("gemini-3.7-flash/5/aaaa1111/go")
	model := ParseInstrumentID("claude-sonnet-5/3/aaaa1111/go")
	prompt := ParseInstrumentID("gemini-3.7-flash/3/bbbb2222/go")

	check(t, "an unchanged reviewer is no drift", DriftGrade(base, base), "")
	check(t, "effort alone is minor", DriftGrade(base, effort), DriftMinor)
	check(t, "the dimension is named", DriftDims(base, effort), []string{"effort"})
	check(t, "runs alone is minor", DriftGrade(base, runs), DriftMinor)
	check(t, "a model swap is major", DriftGrade(base, model), DriftMajor)
	check(t, "a prompt swap is major", DriftGrade(base, prompt), DriftMajor)

	// A provider swap at one model name, prompt and engine is invisible to the string.
	a, _ := InstrumentFieldsFrom(reviewerFields("agy", "m", "", 3, "aaaa1111"))
	b, _ := InstrumentFieldsFrom(reviewerFields("codex", "m", "", 3, "aaaa1111"))
	check(t, "a provider swap is major", DriftGrade(a, b), DriftMajor)

	// The legacy string carries no provider, so the dimension is unknown on one side.
	sameButForProvider, _ := InstrumentFieldsFrom(
		reviewerFields("codex", "gemini-3.7-flash", "", 3, "aaaa1111"))
	check(t, "an unknown provider is not graded", DriftGrade(base, sameButForProvider), "")
	check(t, "and it names no dimension", DriftDims(base, sameButForProvider), []string(nil))
}

// A legacy row and a current one differing only in effort must grade as that one
// change. A whole-string compare reads the pair as a full mismatch and withholds.
func TestLegacyStringGradesAgainstStructuredFields(t *testing.T) {
	rows := []gate.Row{
		instrument(row(1000, "deny"), "gemini-3.7-flash/3/aaaa1111/go"),
		instrument(concerns(row(1001, "retry"), "round", c("widget parser drops a flag")),
			"gemini-3.7-flash/3/aaaa1111/go"),
		fields(instrument(row(1100, "deny"), "gemini-3.7-flash/3/aaaa1111/go/high"),
			reviewerFields("agy", "gemini-3.7-flash", "high", 3, "aaaa1111")),
		fields(instrument(concerns(row(1101, "retry"), "round", c("unrelated nit")),
			"gemini-3.7-flash/3/aaaa1111/go/high"),
			reviewerFields("agy", "gemini-3.7-flash", "high", 3, "aaaa1111")),
	}
	chains := BuildChains(rows, false)
	before, after := SplitChains(chains, 1050)

	grade, dims, labels := windowDrift("gemini-3.7-flash/3/aaaa1111/go", before, after)
	check(t, "effort alone grades minor", grade, DriftMinor)
	check(t, "the dimension is named", dims, []string{"effort"})
	check(t, "the after side carries the provider the string lacks", labels,
		[]string{"gemini-3.7-flash/3/aaaa1111/go/high (provider agy)"})

	steady, _, _ := windowDrift("gemini-3.7-flash/3/aaaa1111/go", before, before)
	check(t, "an unchanged reviewer grades nothing", steady, "")
}

// The provider is the one dimension the instrument string cannot hold, so two windows
// naming different providers at one string must still read as drift.
func TestProviderSwapAtOneInstrumentStringIsDrift(t *testing.T) {
	id := "sonnet/3/aaaa1111/go"
	rows := []gate.Row{
		fields(instrument(row(1000, "deny"), id), reviewerFields("claude-code", "sonnet", "", 3, "aaaa1111")),
		fields(instrument(concerns(row(1001, "retry"), "round", c("widget parser drops a flag")), id),
			reviewerFields("claude-code", "sonnet", "", 3, "aaaa1111")),
		fields(instrument(row(1100, "deny"), id), reviewerFields("codex", "sonnet", "", 3, "aaaa1111")),
		fields(instrument(concerns(row(1101, "retry"), "round", c("unrelated nit")), id),
			reviewerFields("codex", "sonnet", "", 3, "aaaa1111")),
	}
	chains := BuildChains(rows, false)
	before, after := SplitChains(chains, 1050)

	check(t, "the strings alone say nothing", InstrumentsIn(after), []string{id})
	grade, dims, _ := windowDrift(id, before, after)
	check(t, "a provider swap grades major", grade, DriftMajor)
	check(t, "the dimension names the provider", dims, []string{"provider"})
}

// The baseline provider is read from the before window alone. Pooling both windows
// lets an after-window row of the same instrument string supply the baseline its own
// provider, and the swap is then graded against itself.
func TestTheBaselineProviderComesFromTheBeforeWindow(t *testing.T) {
	id := "sonnet/3/aaaa1111/go"
	// The after provider sorts first, so a pooled lookup would return it as the base.
	rows := []gate.Row{
		fields(instrument(row(1000, "deny"), id), reviewerFields("codex", "sonnet", "", 3, "aaaa1111")),
		fields(instrument(concerns(row(1001, "retry"), "round", c("widget parser drops a flag")), id),
			reviewerFields("codex", "sonnet", "", 3, "aaaa1111")),
		fields(instrument(row(1100, "deny"), id), reviewerFields("agy", "sonnet", "", 3, "aaaa1111")),
		fields(instrument(concerns(row(1101, "retry"), "round", c("unrelated nit")), id),
			reviewerFields("agy", "sonnet", "", 3, "aaaa1111")),
	}
	chains := BuildChains(rows, false)
	before, after := SplitChains(chains, 1050)
	grade, _, _ := windowDrift(id, before, after)
	check(t, "the swap grades major", grade, DriftMajor)
	base := BaselineInstrument(id, InstrumentUses(before))
	check(t, "the baseline keeps the before window's provider",
		InstrumentUse{ID: id, Fields: base}.Label(), id+" (provider codex)")
}

// An instrument string neither side can parse is uncharacterized, not small.
func TestAnUnreadableInstrumentPairGradesMajor(t *testing.T) {
	rows := []gate.Row{
		instrument(row(1000, "deny"), "modelA/3/aaaa1111"),
		instrument(concerns(row(1001, "retry"), "round", c("widget parser drops a flag")), "modelA/3/aaaa1111"),
		instrument(row(1100, "deny"), "modelB/3/bbbb2222"),
		instrument(concerns(row(1101, "retry"), "round", c("unrelated nit")), "modelB/3/bbbb2222"),
	}
	chains := BuildChains(rows, false)
	before, after := SplitChains(chains, 1050)
	grade, dims, _ := windowDrift("modelA/3/aaaa1111", before, after)
	check(t, "an unreadable change grades major", grade, DriftMajor)
	if len(dims) != 0 {
		t.Errorf("an unreadable change names no dimension, got %#v", dims)
	}
}

// One instrument string can be stamped by a legacy row and a newer one. The provider
// the newer row knows must survive the legacy row that knows none.
func TestTheBaselineFoldsWhatTheBeforeWindowKnows(t *testing.T) {
	id := "sonnet/3/aaaa1111/go"
	rows := []gate.Row{
		instrument(row(1000, "deny"), id),
		instrument(concerns(row(1001, "retry"), "round", c("widget parser drops a flag")), id),
		fields(instrument(row(1010, "deny"), id), reviewerFields("codex", "sonnet", "", 3, "aaaa1111")),
		fields(instrument(concerns(row(1011, "retry"), "round", c("widget parser drops a flag")), id),
			reviewerFields("codex", "sonnet", "", 3, "aaaa1111")),
		fields(instrument(row(1100, "deny"), id), reviewerFields("agy", "sonnet", "", 3, "aaaa1111")),
		fields(instrument(concerns(row(1101, "retry"), "round", c("unrelated nit")), id),
			reviewerFields("agy", "sonnet", "", 3, "aaaa1111")),
	}
	chains := BuildChains(rows, false)
	before, after := SplitChains(chains, 1050)

	base := BaselineInstrument(id, InstrumentUses(before))
	check(t, "an unstamped row does not erase a known provider", base.Provider, "codex")

	grade, _, _ := windowDrift(id, before, after)
	check(t, "the swap grades major", grade, DriftMajor)

	// A baseline that several providers reviewed is not one identity, so the dimension
	// is unknown rather than one of them, and an after window grades against none. A
	// third value must not refill a dimension the first two already contradicted.
	parsed := ParseInstrumentID(id)
	split := foldInstruments(parsed, []InstrumentFields{
		{Provider: "codex", Model: "sonnet", EffortKnown: true},
		{Provider: "agy", Model: "sonnet", EffortKnown: true},
		{Provider: "claude-code", Model: "sonnet", EffortKnown: true},
	})
	check(t, "a disagreeing dimension stays unknown past a third value", split.Provider, "")
	check(t, "an agreeing one survives", split.Model, "sonnet")

	// The record's own string is the fallback, never a voter: it is derived from the
	// fields, so a row that stamped it cannot legitimately contradict it.
	stamped := foldInstruments(parsed, []InstrumentFields{
		{Provider: "agy", Model: "sonnet", Effort: "high", EffortKnown: true, Runs: 3},
	})
	check(t, "a row's effort is not a conflict with the string's silence", stamped.Effort, "high")
	check(t, "and it stays known", stamped.EffortKnown, true)
	check(t, "an unstamped dimension falls back to the string", stamped.PromptDigest, "aaaa1111")
}

// An after window holding a dimensioned change and an unreadable one reports both:
// the grade comes from the unreadable identity, and the named dimension from the
// other. Neither may stand in for the other.
func TestAnUnreadableDriftIsReportedBesideADimensionedOne(t *testing.T) {
	base := "gemini-3.7-flash/3/aaaa1111/go"
	rows := []gate.Row{
		instrument(row(1000, "deny"), base),
		instrument(concerns(row(1001, "retry"), "round", c("widget parser drops a flag")), base),
		instrument(row(1100, "deny"), base+"/high"),
		instrument(concerns(row(1101, "retry"), "round", c("unrelated nit")), base+"/high"),
		instrument(row(1200, "deny"), "house-reviewer-v2"),
		instrument(concerns(row(1201, "retry"), "round", c("another nit")), "house-reviewer-v2"),
	}
	chains := BuildChains(rows, false)
	before, after := SplitChains(chains, 1050)

	grade, dims, labels := windowDrift(base, before, after)
	check(t, "an unreadable identity carries the grade", grade, DriftMajor)
	check(t, "the dimensioned half is still named", dims, []string{"effort"})
	check(t, "and both identities are reported", labels,
		[]string{base + "/high", "house-reviewer-v2"})
}
