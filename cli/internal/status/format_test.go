package status

import (
	"testing"

	"github.com/steer-sh/steer-cli/internal/cadence"
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
	want := "12 eligible / 20 (3 out of scope, 2 unknown scope, 1 with no outcome read, 1 with no decision found, 1 with no decision recorded, 1 pending)"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got := CoveragePhrase(cadence.Coverage{Total: 8, Eligible: 8}); got != "8 eligible / 8" {
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
