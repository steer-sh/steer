package status

import (
	"testing"

	"github.com/steer-sh/steer-cli/internal/cadence"
)

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
	if got := RequiredPhrase(iptr(9), "collecting"); got != "needs 9 clean eligible chain(s)" {
		t.Errorf("a reachable target names its count, got %q", got)
	}
	if got := RequiredPhrase(iptr(0), "effective"); got != "" {
		t.Errorf("a met target says nothing, got %q", got)
	}
	// A null required_after on an abstaining record means no window could decide it.
	if got := RequiredPhrase(nil, "inconclusive"); got == "" {
		t.Error("an unreachable target must be named on an abstaining record")
	}
	if got := RequiredPhrase(nil, "effective"); got != "" {
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
	want := "12 eligible / 20 (3 out of scope, 2 unknown scope, 3 without a ledger, 1 pending)"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got := CoveragePhrase(cadence.Coverage{Total: 8, Eligible: 8}); got != "8 eligible / 8" {
		t.Errorf("a clean window carries no parenthetical, got %q", got)
	}
}
