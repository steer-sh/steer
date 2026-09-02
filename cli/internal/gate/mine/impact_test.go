package mine

import "testing"

// use builds the prospective identity an operator's pending edit would stamp.
func use(model, effort string) InstrumentUse {
	id := model + "/3/abc/go"
	if effort != "" {
		id += "/" + effort
	}
	return InstrumentUse{ID: id, Fields: InstrumentFields{
		Provider: "agy", Model: model, Effort: effort, EffortKnown: true,
		Runs: 3, PromptDigest: "abc", Engine: "go",
	}}
}

func TestImpactOfGradesAPendingReviewerChangeByDimension(t *testing.T) {
	authored := "gemini/3/abc/go"
	record := withKV(scopedRecord(4000), "instrument", authored)
	chains := []Chain{chain(t, 1000, instrument(row(1000, "deny"), authored))}

	effortOnly := ImpactOf([]Record{record}, chains, use("gemini", "high"))
	if len(effortOnly) != 1 {
		t.Fatalf("an effort change lands in the record, got %#v", effortOnly)
	}
	check(t, "effort alone is minor", effortOnly[0].Grade, DriftMinor)
	check(t, "the moved dimension is named", effortOnly[0].Dims, []string{"effort"})

	swap := ImpactOf([]Record{record}, chains, use("other-model", ""))
	if len(swap) != 1 || swap[0].Grade != DriftMajor {
		t.Fatalf("a model swap is major, got %#v", swap)
	}

	same := ImpactOf([]Record{record}, chains, use("gemini", ""))
	if len(same) != 0 {
		t.Fatalf("an unchanged identity lands in nothing, got %#v", same)
	}
}

// The session corpus stamps the transcript parser as its instrument, which no reviewer
// setting moves. Naming one of its records would report an edit that cannot reach it.
func TestImpactOfReadsTheChainCorpusOnly(t *testing.T) {
	authored := "gemini/3/abc/go"
	session := withKV(withKV(withKV(scopedRecord(4000), "instrument", authored),
		"corpus", CorpusTranscript), "unit", UnitSession)
	chains := []Chain{chain(t, 1000, instrument(row(1000, "deny"), authored))}

	if got := ImpactOf([]Record{session}, chains, use("other-model", "")); len(got) != 0 {
		t.Fatalf("a session-corpus record is not graded here, got %#v", got)
	}
}

func TestImpactOfSkipsRetiredRecords(t *testing.T) {
	authored := "gemini/3/abc/go"
	retired := withKV(withKV(scopedRecord(4000), "instrument", authored), "retired_ts", 5000)
	chains := []Chain{chain(t, 1000, instrument(row(1000, "deny"), authored))}

	if got := ImpactOf([]Record{retired}, chains, use("other-model", "")); len(got) != 0 {
		t.Fatalf("a retired record collects nothing, got %#v", got)
	}
}

// A provider swap at one model name stamps the same instrument string, so a comparison
// of strings cannot see it. The before window's own rows carry the provider the string
// cannot hold, and that is the dimension the grade reads.
func TestImpactOfSeesAProviderSwapAtOneModelName(t *testing.T) {
	authored := "gemini/3/abc/go"
	record := withKV(scopedRecord(4000), "instrument", authored)
	deny := instrument(row(1000, "deny"), authored)
	deny["instrument_fields"] = map[string]any{
		"provider": "agy", "model": "gemini", "effort": "",
		"runs": 3, "prompt_digest": "abc", "engine": "go",
	}
	chains := []Chain{chain(t, 1000, deny)}

	next := use("gemini", "")
	next.Fields.Provider = "codex"
	got := ImpactOf([]Record{record}, chains, next)
	if len(got) != 1 || got[0].Grade != DriftMajor {
		t.Fatalf("a provider swap is major, got %#v", got)
	}
	check(t, "the moved dimension is named", got[0].Dims, []string{"provider"})
}
