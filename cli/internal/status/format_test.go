package status

import (
	"testing"

	"github.com/steer-sh/steer-cli/internal/cadence"
)

func fptr(f float64) *float64 { return &f }
func iptr(n int) *int         { return &n }

func TestEffectPhraseNamesTheOpenBounds(t *testing.T) {
	cases := []struct {
		label string
		in    cadence.Effect
		want  string
	}{
		{"a closed range prints both bounds",
			cadence.Effect{Estimate: fptr(0.25), Lower: 0.05, Upper: fptr(0.4), Confidence: 0.9, Method: "m"},
			"RR 0.25 [0.05, 0.40] @90%"},
		{"an unbounded upper prints ∞, never a number",
			cadence.Effect{Estimate: fptr(0.25), Lower: 0.05, Confidence: 0.9, Method: "m"},
			"RR 0.25 [0.05, ∞] @90%"},
		{"a missing estimate prints —, never zero",
			cadence.Effect{Lower: 0.05, Upper: fptr(0.4), Confidence: 0.9, Method: "m"},
			"RR — [0.05, 0.40] @90%"},
		{"an unmeasured record prints nothing at all",
			cadence.Effect{}, ""},
	}
	for _, c := range cases {
		if got := EffectPhrase(c.in); got != c.want {
			t.Errorf("%s: got %q, want %q", c.label, got, c.want)
		}
	}
}

func TestRequiredPhraseSeparatesUnreachableFromSatisfied(t *testing.T) {
	if got := RequiredPhrase(iptr(9), "chain", "collecting"); got != "needs 9 more clean, eligible review sequences" {
		t.Errorf("a reachable target names its count, got %q", got)
	}
	if got := RequiredPhrase(iptr(0), "chain", "effective"); got != "" {
		t.Errorf("a met target says nothing, got %q", got)
	}
	// A null required_after on an abstaining record means no window could decide it.
	if got := RequiredPhrase(nil, "chain", "inconclusive"); got == "" {
		t.Error("an unreachable target must be named on an abstaining record")
	}
	if got := RequiredPhrase(nil, "chain", "effective"); got != "" {
		t.Errorf("a decided record says nothing, got %q", got)
	}
}

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
		Total: 20, Eligible: 12, Ineligible: 3, Unknown: 5,
		UnknownScope: 2, UnknownEvidence: 3, Pending: 1,
	})
	want := "12 eligible / 20 (3 out of scope, 2 unknown scope, 3 with no decision recorded, 1 pending)"
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
