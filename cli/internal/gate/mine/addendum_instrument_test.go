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
