package mine

import (
	"reflect"
	"testing"
)

func reviewRecord(id, match, kind, status string) Record {
	return Record{"id": id, "match": match, "kind": kind, "status": status,
		"corpus": CorpusReviewFindings, "unit": UnitChain}
}

func TestDecidingClassesRejectsAnUnrecognizedKind(t *testing.T) {
	// KindFilter reads every kind it does not recognize as "both", and
	// RecordKindFilter passes an unrecognized string straight through. A record
	// carrying one would cover every finding of every verdict, so it is refused
	// before it is ever consulted.
	deciding := DecidingClasses([]Record{reviewRecord("odd", "parser", "typo", "active")})
	if len(deciding) != 0 {
		t.Fatalf("an unrecognized kind must decide nothing; got %d class(es)", len(deciding))
	}
}

func TestDecidingClassesRejectsARetiredRecord(t *testing.T) {
	for _, status := range []string{"removed", "reworked"} {
		deciding := DecidingClasses([]Record{reviewRecord("gone", "parser", "defect", status)})
		if len(deciding) != 0 {
			t.Errorf("status %s must decide nothing; got %d class(es)", status, len(deciding))
		}
	}
}

func TestDecidingClassesRejectsAnotherCorpus(t *testing.T) {
	r := reviewRecord("elsewhere", "parser", "defect", "active")
	r["corpus"] = CorpusTranscript
	r["unit"] = UnitSession
	if deciding := DecidingClasses([]Record{r}); len(deciding) != 0 {
		t.Fatalf("a transcript class must decide nothing here; got %d class(es)", len(deciding))
	}
}

func TestDecidingClassesSkipsAnUncompilableRegex(t *testing.T) {
	records := []Record{
		reviewRecord("broken", "a(b", "defect", "active"),
		reviewRecord("sound", "parser", "defect", "active"),
	}
	deciding := DecidingClasses(records)
	if len(deciding) != 1 || deciding[0].ID != "sound" {
		t.Fatalf("a bad regex must be skipped and the rest kept; got %#v", deciding)
	}
}

func TestCoveringIDsHonoursTheClassVerdict(t *testing.T) {
	// A false-positive class was authored against rejected findings. It cannot
	// cover an accepted defect whose text it happens to match.
	deciding := DecidingClasses([]Record{
		reviewRecord("fp", "parser", "false-positive", "active"),
	})
	accepted := Finding{Verdict: "ACCEPTED", Text: "the parser drops a flag"}
	if ids := CoveringIDs(accepted, deciding); len(ids) != 0 {
		t.Errorf("a false-positive class must not cover an accepted finding; got %v", ids)
	}
	rejected := Finding{Verdict: "REJECTED", Text: "the parser drops a flag"}
	if ids := CoveringIDs(rejected, deciding); !reflect.DeepEqual(ids, []string{"fp"}) {
		t.Errorf("it must cover the rejected finding; got %v", ids)
	}
}

func TestCoveringIDsAreSorted(t *testing.T) {
	deciding := DecidingClasses([]Record{
		reviewRecord("zeta", "parser", "defect", "active"),
		reviewRecord("alpha", "drops", "defect", "active"),
	})
	f := Finding{Verdict: "ACCEPTED", Text: "the parser drops a flag"}
	if ids := CoveringIDs(f, deciding); !reflect.DeepEqual(ids, []string{"alpha", "zeta"}) {
		t.Errorf("covering ids must read the same on every run; got %v", ids)
	}
}

func TestHasUncoveredKeepsAResidualFinding(t *testing.T) {
	deciding := DecidingClasses([]Record{
		reviewRecord("known", "parser", "defect", "active"),
	})
	findings := []Finding{
		{Verdict: "ACCEPTED", Text: "the parser drops a flag"},
		{Verdict: "ACCEPTED", Text: "a defect no class names"},
	}
	if !HasUncovered(findings, deciding) {
		t.Error("one covered finding must not decide the others")
	}
	if HasUncovered(findings[:1], deciding) {
		t.Error("a fully covered set has nothing uncovered")
	}
}

func TestFindingsOfKindSelectsByVerdict(t *testing.T) {
	ch := Chain{Findings: []Finding{
		{Verdict: "ACCEPTED", Text: "a"},
		{Verdict: "REJECTED", Text: "b"},
	}}
	if got := FindingsOfKind(ch, "accepted"); len(got) != 1 || got[0].Text != "a" {
		t.Errorf("accepted selects one finding; got %#v", got)
	}
	if got := FindingsOfKind(ch, "both"); len(got) != 2 {
		t.Errorf("both selects two findings; got %#v", got)
	}
}
