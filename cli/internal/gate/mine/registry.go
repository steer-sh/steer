package mine

import "fmt"

// The candidate-record contract's corpus and unit tags. A corpus names the evidence
// a class is counted over; a unit names what one denominator entry is.
const (
	CorpusReviewFindings = "review-findings"
	CorpusTranscript     = "transcript-friction"

	UnitChain   = "chain"
	UnitSession = "session"
)

// The scoring engines. Each reads one evidence set and counts one unit; a record names
// its corpus and the registry names the engine that corpus is scored by.
const (
	EngineChain   = "chain"
	EngineSession = "session"
)

// CorpusSpec is one corpus's entry in the registry: the unit its records count by, the
// engine that scores it, and the scope predicates that engine can decide.
type CorpusSpec struct {
	Corpus   string
	Unit     string
	Engine   string
	Scorable bool
	// PathScope reports that a unit of this corpus carries a changed-path list, so
	// --scope-path can decide it. A session carries none.
	PathScope bool
}

var corpora = map[string]CorpusSpec{
	CorpusReviewFindings: {
		Corpus: CorpusReviewFindings, Unit: UnitChain,
		Engine: EngineChain, Scorable: true, PathScope: true,
	},
	CorpusTranscript: {
		Corpus: CorpusTranscript, Unit: UnitSession,
		Engine: EngineSession, Scorable: true,
	},
}

// Corpora lists the registered corpora by name, for a caller validating a flag.
func Corpora() []string {
	return []string{CorpusReviewFindings, CorpusTranscript}
}

// LookupCorpus reads a corpus's spec.
func LookupCorpus(name string) (CorpusSpec, bool) {
	spec, ok := corpora[name]
	return spec, ok
}

// CorpusOf is a record's corpus. A record written before the tag existed is
// review-findings, the only corpus that existed then.
func CorpusOf(record Record) string {
	if c := str(record["corpus"]); c != "" {
		return c
	}
	return CorpusReviewFindings
}

// UnitOf is a record's unit. The stored tag wins, so a record round-trips unchanged;
// the registry supplies it when the tag is absent.
func UnitOf(record Record) string {
	if u := str(record["unit"]); u != "" {
		return u
	}
	if spec, ok := LookupCorpus(CorpusOf(record)); ok {
		return spec.Unit
	}
	return UnitChain
}

// Scorable reports whether a registered engine can score the record. A caller that
// scores an inventory reads this instead of testing a corpus name.
func Scorable(record Record) bool {
	spec, ok := LookupCorpus(CorpusOf(record))
	if !ok || !spec.Scorable {
		return false
	}
	// A record tagged with a unit its corpus does not count by cannot be scored: the
	// denominator its engine builds is the corpus's own unit.
	return UnitOf(record) == spec.Unit
}

// EngineOf names the engine a record is scored by, empty when none can score it.
func EngineOf(record Record) string {
	spec, ok := LookupCorpus(CorpusOf(record))
	if !ok || !Scorable(record) {
		return ""
	}
	return spec.Engine
}

// requireScorable is the guard every scoring entry point runs first.
func requireScorable(record Record) error {
	if Scorable(record) {
		return nil
	}
	return fmt.Errorf("intervention %s is corpus %s by %s, which no scoring engine reads",
		pyRepr(str(record["id"])), pyRepr(CorpusOf(record)), pyRepr(UnitOf(record)))
}

// requireChainCorpus is the guard on the chain engine's own entry point, for a caller
// holding chains and nothing else.
func requireChainCorpus(record Record) error {
	if err := requireScorable(record); err != nil {
		return err
	}
	if EngineOf(record) != EngineChain {
		return fmt.Errorf("intervention %s is scored by the %s engine, not %s",
			pyRepr(str(record["id"])), pyRepr(EngineOf(record)), pyRepr(EngineChain))
	}
	return nil
}
