package status

import (
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/cadence"
	"github.com/steer-sh/steer-cli/internal/checks"
)

func TestScopePhraseCombinesPredicatesWithAnd(t *testing.T) {
	cases := []struct {
		in   *cadence.Scope
		want string
	}{
		{nil, "none (legacy)"},
		{&cadence.Scope{Version: 1, All: true}, "all"},
		{&cadence.Scope{Version: 1, Repo: "steer"}, "repo=/steer/"},
		{&cadence.Scope{Version: 1, Path: `\.go$`}, `path=/\.go$/`},
		{&cadence.Scope{Version: 1, Repo: "steer", Path: `\.go$`}, `repo=/steer/ AND path=/\.go$/`},
	}
	for _, c := range cases {
		if got := ScopePhrase(c.in); got != c.want {
			t.Errorf("scope %#v: got %q, want %q", c.in, got, c.want)
		}
	}
}

func TestCoveragePhraseAccountsForEveryChain(t *testing.T) {
	got := CoveragePhrase(cadence.Coverage{
		Total: 20, Eligible: 12, Ineligible: 3, Unknown: 6,
		UnknownScope: 2, UnknownEvidence: 4, Pending: 1, LedgerOverdue: 1, LedgerMissed: 1,
	})
	want := "12 of 20 counted (3 out of scope, 2 with no repository recorded, 1 with no outcome read, 1 with no decision found, 1 with no decision recorded, 1 pending)"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got := CoveragePhrase(cadence.Coverage{Total: 8, Eligible: 8}); got != "8 of 8 counted" {
		t.Errorf("a clean window carries no parenthetical, got %q", got)
	}
}

// A tier is a type. The empty state says so in words, because "none → " is the
// reading the page gave a rejection and it names nothing the reader can act on.
func TestTierPhraseNamesTheTypeAndTheEmptyState(t *testing.T) {
	cases := []struct{ tier, target, want string }{
		{"check", "check.sh", "repository check → check.sh"},
		{"steer", "/h/guard.py", "pre-tool guard → /h/guard.py"},
		{"guard", "", "pre-tool guard"},
		{"prose", "CLAUDE.md", "instruction rule → CLAUDE.md"},
		{"none", "", "no fix installed"},
		{"", "", "no fix installed"},
		{"future-tier", "x", "future-tier → x"},
	}
	for _, tc := range cases {
		if got := TierPhrase(tc.tier, tc.target); got != tc.want {
			t.Errorf("TierPhrase(%q, %q) = %q, want %q", tc.tier, tc.target, got, tc.want)
		}
	}
}

// A channel's observed time names what it recorded, so a reader never meets a bare
// "row".
func TestObservedPhraseNamesTheRecordKind(t *testing.T) {
	for corpus, want := range map[string]string{
		ledgerCorpus: "last review ",
		checkCorpus:  "last check run ",
	} {
		got := ObservedPhrase(ScopeArming{ObservedCorpus: corpus, Observed: 1700000000})
		if !strings.HasPrefix(got, want) || strings.Contains(got, "row") {
			t.Errorf("%s: ObservedPhrase = %q; want it to start %q", corpus, got, want)
		}
	}
}

// A check.sh run names what started it and what it did to the turn in words, never
// as the stored source and disposition tokens.
func TestCheckRunPhraseNamesSourceAndDispositionInWords(t *testing.T) {
	cases := []struct {
		run  CheckRun
		want string
	}{
		{CheckRun{Source: checks.SourceStop, Outcome: checks.OutcomePassed, Disposition: checks.DispositionAllow},
			"passed at the end of a turn"},
		{CheckRun{Source: checks.SourceStop, Outcome: checks.OutcomeFailed, Disposition: checks.DispositionBlock},
			"failed at the end of a turn; it blocked the turn"},
		{CheckRun{Source: checks.SourceStop, Outcome: checks.OutcomeFailed, Disposition: checks.DispositionYield},
			"failed at the end of a turn; it failed again on an unchanged tree, so the turn ended without another block"},
		{CheckRun{Source: checks.SourceRun, Outcome: checks.OutcomePassed}, "passed from steer check run"},
		{CheckRun{Source: checks.SourceStop, Outcome: checks.OutcomePassed, IntegrationFailure: true, Disposition: checks.DispositionBlock},
			"passed, verification incomplete at the end of a turn; it blocked the turn"},
		{CheckRun{Source: checks.SourceRun, Outcome: checks.OutcomePassed, IntegrationFailure: true, Missing: []string{"declared"}},
			"passed, verification incomplete, without running declared from steer check run"},
	}
	for _, c := range cases {
		if got := CheckRunPhrase(&c.run); got != c.want {
			t.Errorf("got %q, want %q", got, c.want)
		}
	}
}
