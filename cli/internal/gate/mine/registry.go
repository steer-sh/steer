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

// CorpusSpec is one corpus's entry in the registry: the unit its records count by, and
// whether this engine can score them. The mine engine reads the commit-review ledger by
// chain, so it scores that corpus alone; a record from another corpus is carried
// unscored rather than counted against evidence it was never authored over.
type CorpusSpec struct {
	Corpus   string
	Unit     string
	Scorable bool
}

var corpora = map[string]CorpusSpec{
	CorpusReviewFindings: {Corpus: CorpusReviewFindings, Unit: UnitChain, Scorable: true},
	CorpusTranscript:     {Corpus: CorpusTranscript, Unit: UnitSession, Scorable: false},
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

// Scorable reports whether this engine can score the record. A caller that scores an
// inventory reads this instead of testing a corpus name, so one registry decides it.
func Scorable(record Record) bool {
	spec, ok := LookupCorpus(CorpusOf(record))
	if !ok {
		return false
	}
	if !spec.Scorable {
		return false
	}
	// A record tagged with a unit its corpus does not count by cannot be scored here
	// either: the denominator the engine builds is chains.
	return UnitOf(record) == spec.Unit
}

// requireScorable is the guard every scoring entry point runs first.
func requireScorable(record Record) error {
	if Scorable(record) {
		return nil
	}
	return fmt.Errorf("intervention %s is corpus %s by %s; this engine scores %s by %s",
		pyRepr(str(record["id"])), pyRepr(CorpusOf(record)), pyRepr(UnitOf(record)),
		pyRepr(CorpusReviewFindings), pyRepr(UnitChain))
}
