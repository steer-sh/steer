package mine

import "testing"

func TestTheAddendumRidesInsideTheDigestSegment(t *testing.T) {
	with := ParseInstrumentID("gemini/3/abc+d1/go/high")
	if with.PromptDigest != "abc" || with.AddendumDigest != "d1" || !with.AddendumKnown || with.Effort != "high" {
		t.Fatalf("parsed = %#v", with)
	}
	without := ParseInstrumentID("gemini/3/abc/go/high")
	if without.AddendumDigest != "" || !without.AddendumKnown || without.Effort != "high" {
		t.Fatalf("a string with no addendum must read as known-empty: %#v", without)
	}
	if bad := ParseInstrumentID("gemini/3/abc+d1/go/high/extra"); bad.AddendumKnown {
		t.Fatalf("an unreadable string knows no addendum: %#v", bad)
	}
	fromFields, _ := InstrumentFieldsFrom(map[string]any{"prompt_digest": "abc"})
	if fromFields.AddendumDigest != "" || !fromFields.AddendumKnown {
		t.Fatalf("a row with no addendum_digest must read as known-empty: %#v", fromFields)
	}
}

func TestAnAddendumChangeIsANamedMajorDrift(t *testing.T) {
	base := ParseInstrumentID("gemini/3/abc/go")
	next := ParseInstrumentID("gemini/3/abc+d1/go")
	check(t, "the moved dimension is named", DriftDims(base, next), []string{"review addendum"})
	check(t, "an addendum change is major", DriftGrade(base, next), DriftMajor)
}

// A reviewer settings edit has no repository: each record keeps its own addendum.
func TestASettingsEditInheritsEachRecordsAddendum(t *testing.T) {
	authored := "gemini/3/abc+d1/go"
	record := withKV(scopedRecord(4000), "instrument", authored)
	chains := []Chain{chain(t, 1000, instrument(row(1000, "deny"), authored))}

	if got := ImpactOf([]Record{record}, chains, use("gemini", "")); len(got) != 0 {
		t.Fatalf("an unchanged reviewer over an addendum record drifts nothing, got %#v", got)
	}
	effort := ImpactOf([]Record{record}, chains, use("gemini", "high"))
	if len(effort) != 1 {
		t.Fatalf("got %#v", effort)
	}
	check(t, "effort alone moved", effort[0].Dims, []string{"effort"})
	check(t, "and it is minor", effort[0].Grade, DriftMinor)

	known := use("gemini", "")
	known.Fields.AddendumKnown = true
	if got := ImpactOf([]Record{record}, chains, known); len(got) != 1 || got[0].Grade != DriftMajor {
		t.Fatalf("a known different addendum is major drift, got %#v", got)
	}
}

func TestTheBaselineFoldsTheAddendum(t *testing.T) {
	agreed := foldInstruments(ParseInstrumentID("m/3/abc/go"), []InstrumentFields{
		{AddendumDigest: "d1", AddendumKnown: true}, {AddendumDigest: "d1", AddendumKnown: true},
	})
	if agreed.AddendumDigest != "d1" || !agreed.AddendumKnown {
		t.Fatalf("agreed = %#v", agreed)
	}
	split := foldInstruments(ParseInstrumentID("m/3/abc/go"), []InstrumentFields{
		{AddendumDigest: "d1", AddendumKnown: true}, {AddendumDigest: "", AddendumKnown: true},
		{AddendumDigest: "d1", AddendumKnown: true},
	})
	if split.AddendumKnown {
		t.Fatalf("a baseline over two addenda is not one identity: %#v", split)
	}
}
