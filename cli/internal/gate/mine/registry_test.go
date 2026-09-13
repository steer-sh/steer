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

func TestTheSessionCorpusIsScoredByItsOwnEngine(t *testing.T) {
	r := Record{"id": "friction", "corpus": CorpusTranscript, "unit": UnitSession, "match": "x"}
	if !Scorable(r) {
		t.Error("a transcript-friction record must be scorable by the session engine")
	}
	check(t, "engine", EngineOf(r), EngineSession)
	// The chain engine's own entry point still refuses it: it counts a different unit.
	if _, err := ScoreRecord(r, EvidenceSet{}); err == nil {
		t.Error("ScoreRecord must refuse a record the chain engine cannot count")
	}
	if _, err := ScoreWith(r, EvidenceSet{}); err != nil {
		t.Errorf("ScoreWith must route a session record to the session engine: %v", err)
	}
	unknown := Record{"id": "u", "corpus": "made-up"}
	if Scorable(unknown) {
		t.Error("an unregistered corpus must not be scorable")
	}
	if _, err := ScoreWith(unknown, EvidenceSet{}); err == nil {
		t.Error("ScoreWith must refuse an unregistered corpus")
	}
}

func TestRegisterRefusesOnlyACorpusItCannotBaseline(t *testing.T) {
	dir := t.TempDir()
	err := Register(discard{}, dir, RegisterOpts{
		Prose: testProse,
		Class: "c", Match: "bare python", Corpus: CorpusTranscript,
		Sessions: []SessionUnit{friction(1000, "claude-code", "proj", "bare python here")},
		Tier:     "review",
		Target:   "review-prompt.md", ScopeAll: true, InstalledTS: 2000,
	})
	if err != nil {
		t.Fatalf("the session corpus can freeze a baseline: %v", err)
	}
	if err := Register(discard{}, dir, RegisterOpts{
		Prose: testProse,
		Class: "c", Match: "x", Corpus: "made-up",
		Tier: "review", Target: "review-prompt.md", ScopeAll: true, Force: true,
	}); err == nil {
		t.Fatal("an unknown corpus must be refused")
	}
	// A session carries no changed-path list, so a path predicate cannot be decided.
	if err := Register(discard{}, dir, RegisterOpts{
		Prose: testProse,
		Class: "c", Match: "bare python", Corpus: CorpusTranscript,
		Sessions: []SessionUnit{friction(1000, "claude-code", "proj", "bare python here")},
		Tier:     "review", Target: "review-prompt.md", ScopePath: "cli/", InstalledTS: 2000,
	}); err == nil {
		t.Fatal("--scope-path over the session corpus must be refused")
	}
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }
