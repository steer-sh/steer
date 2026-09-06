package status

import (
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/cadence"
)

// The four causes of a withheld evidence-incomplete result read as four
// sentences, and one command repairs one of them.
func TestEvidenceGapNamesItsCause(t *testing.T) {
	cases := []struct {
		name   string
		unit   string
		before cadence.Coverage
		after  cadence.Coverage
		attr   cadence.Attribution
		says   string
		fix    string
	}{
		{
			name:   "a ledger the resolver closed as gone",
			unit:   "chain",
			before: cadence.Coverage{Total: 4, Unknown: 4, UnknownEvidence: 4},
			after:  cadence.Coverage{Total: 3, Eligible: 3},
			says:   "no recorded decision",
			fix:    BackfillCommand,
		},
		{
			name:   "chains carrying no scope metadata",
			unit:   "chain",
			before: cadence.Coverage{Total: 4, Eligible: 4},
			after:  cadence.Coverage{Total: 3, Unknown: 3, UnknownScope: 3},
			says:   "no repository or changed files",
		},
		{
			name:   "sessions carrying no project",
			unit:   "session",
			before: cadence.Coverage{Total: 5, Unknown: 5},
			after:  cadence.Coverage{Total: 2, Eligible: 2},
			says:   "record no project",
		},
		{
			name:   "no guard audit stream on this machine",
			unit:   "session",
			before: cadence.Coverage{Total: 5, Eligible: 5},
			after:  cadence.Coverage{Total: 2, Eligible: 2},
			attr:   cadence.Attribution{Applicable: true, LogPresent: false},
			says:   "no guard audit log",
		},
	}
	for _, c := range cases {
		g := EvidenceGap("evidence-incomplete", c.unit, c.before, c.after, c.attr)
		if g == nil {
			t.Errorf("%s: named no cause", c.name)
			continue
		}
		if !strings.Contains(g.Sentence, c.says) {
			t.Errorf("%s: said %q, want it to name %q", c.name, g.Sentence, c.says)
		}
		if g.Command != c.fix {
			t.Errorf("%s: offered %q, want %q", c.name, g.Command, c.fix)
		}
	}
}

// A pending ledger row is not a missing one, and the chain fields are never read
// over a session window.
func TestEvidenceGapReadsOnlyItsOwnCorpus(t *testing.T) {
	pending := cadence.Coverage{Total: 3, Unknown: 3, UnknownEvidence: 3, Pending: 3}
	if g := EvidenceGap("evidence-incomplete", "chain", pending, pending, cadence.Attribution{}); g != nil {
		t.Errorf("a fully pending window named a cause: %q", g.Sentence)
	}
	chainShaped := cadence.Coverage{Total: 3, Unknown: 3, UnknownEvidence: 3}
	g := EvidenceGap("evidence-incomplete", "session", chainShaped, chainShaped, cadence.Attribution{})
	if g == nil || g.Command != "" {
		t.Errorf("a session window was handed the resolver: %+v", g)
	}
}

// Every other verdict keeps the table's own sentence.
func TestEvidenceGapIsEvidenceIncompleteOnly(t *testing.T) {
	empty := cadence.Coverage{Total: 3, Unknown: 3, UnknownScope: 3}
	for _, v := range tokenVocabulary {
		if v == "evidence-incomplete" {
			continue
		}
		if g := EvidenceGap(v, "chain", empty, empty, cadence.Attribution{}); g != nil {
			t.Errorf("verdict %q named an evidence cause: %q", v, g.Sentence)
		}
		if got := GapSentence(nil, v, false); got != ActionSentence(v, false) {
			t.Errorf("verdict %q lost its own sentence", v)
		}
	}
}
