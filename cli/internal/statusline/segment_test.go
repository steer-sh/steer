package statusline

import (
	"strings"
	"testing"
)

func TestRenderNamesTheMostUrgentItem(t *testing.T) {
	cases := []struct {
		name string
		in   Inputs
		want string
	}{
		{
			name: "an unarmed gate outranks everything else",
			in:   Inputs{Armed: false, Model: "gemini-3.7-flash-high", Runs: 3, Decisions: 4, Observed: true},
			want: "steer ✕ gate off → steer commit-gate install",
		},
		{
			name: "decisions outrank the status word",
			in:   Inputs{Armed: true, Model: "gemini-3.7-flash-high", Runs: 3, Decisions: 2, Observed: true},
			want: "steer ⏻ 3×flash · 2 decisions → /steer-report",
		},
		{
			name: "one decision carries the singular noun",
			in:   Inputs{Armed: true, Model: "claude-opus-5", Runs: 1, Decisions: 1, Observed: true},
			want: "steer ⏻ 1×opus · 1 decision → /steer-report",
		},
		{
			name: "an armed gate with no ledger row has never fired",
			in:   Inputs{Armed: true, Model: "gemini-3.7-flash-high", Runs: 3, Observed: false},
			want: "steer ⏻ 3×flash · never fired",
		},
		{
			name: "healthy needs an observed row",
			in:   Inputs{Armed: true, Model: "gemini-3.7-flash-high", Runs: 3, Observed: true},
			want: "steer ⏻ 3×flash · healthy",
		},
		{
			name: "an unknown reviewer leaves the label off rather than guessing",
			in:   Inputs{Armed: true, Observed: true},
			want: "steer ⏻ healthy",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Render(tc.in); got != tc.want {
				t.Errorf("Render() = %q, want %q", got, tc.want)
			}
		})
	}
}

// A row that claims health over a gate that never fired makes exactly the claim the
// arming posture exists to refuse.
func TestHealthyIsWithheldUntilTheLedgerHoldsARow(t *testing.T) {
	row := Render(Inputs{Armed: true, Model: "claude-opus-5", Runs: 3, Observed: false})
	if strings.Contains(row, "healthy") {
		t.Errorf("Render() claimed health with no observed row: %q", row)
	}
}

func TestShortModelKeepsTheDistinguishingSegment(t *testing.T) {
	cases := map[string]string{
		"gemini-3.7-flash-high":     "flash",
		"claude-opus-5":             "opus",
		"claude-haiku-4-5-20251001": "haiku",
		"gpt-5.2-codex":             "codex",
		"agy":                       "agy",
		"":                          "",
		// Every segment drops, so the id is returned whole rather than empty.
		"claude-5": "claude-5",
	}
	for model, want := range cases {
		if got := ShortModel(model); got != want {
			t.Errorf("ShortModel(%q) = %q, want %q", model, got, want)
		}
	}
}

// A number with no noun names nothing the reader can act on.
func TestEveryCountCarriesItsNoun(t *testing.T) {
	for _, n := range []int{1, 2, 17} {
		if got := decisionsPhrase(n); !strings.Contains(got, "decision") {
			t.Errorf("decisionsPhrase(%d) = %q, want a noun", n, got)
		}
	}
}
