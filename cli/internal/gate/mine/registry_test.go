package mine

import "testing"

func TestCorpusOfAndUnitOfDefaultToReviewFindings(t *testing.T) {
	// A record written before the tags existed predates every other corpus.
	legacy := Record{"id": "old"}
	check(t, "legacy corpus", CorpusOf(legacy), CorpusReviewFindings)
	check(t, "legacy unit", UnitOf(legacy), UnitChain)
	if !Scorable(legacy) {
		t.Error("a legacy review-findings record must stay scorable")
	}
}

func TestUnitOfPrefersTheStoredTag(t *testing.T) {
	// The stored tag wins so a record round-trips unchanged even when the registry's
	// default for its corpus later differs.
	r := Record{"corpus": CorpusReviewFindings, "unit": "something-else"}
	check(t, "stored unit", UnitOf(r), "something-else")
	if Scorable(r) {
		t.Error("a unit its corpus does not count by must not be scorable here")
	}
}

func TestAnotherCorpusIsNotScoredAgainstChains(t *testing.T) {
	r := Record{"id": "friction", "corpus": CorpusTranscript, "unit": UnitSession, "match": "x"}
	if Scorable(r) {
		t.Error("a transcript-friction record must not be scorable by the chain engine")
	}
	if _, err := ScoreRecord(r, nil, 0); err == nil {
		t.Error("ScoreRecord must refuse a corpus it cannot count")
	}
	unknown := Record{"id": "u", "corpus": "made-up"}
	if Scorable(unknown) {
		t.Error("an unregistered corpus must not be scorable")
	}
}

func TestRegisterRefusesACorpusItCannotBaseline(t *testing.T) {
	dir := t.TempDir()
	err := Register(discard{}, dir, RegisterOpts{
		Class: "c", Match: "x", Corpus: CorpusTranscript,
		Tier: "check", Target: "check.sh", ScopeAll: true, Force: true,
	})
	if err == nil {
		t.Fatal("registering a non-chain corpus must be refused at write time")
	}
	if err := Register(discard{}, dir, RegisterOpts{
		Class: "c", Match: "x", Corpus: "made-up",
		Tier: "check", Target: "check.sh", ScopeAll: true, Force: true,
	}); err == nil {
		t.Fatal("an unknown corpus must be refused")
	}
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }
