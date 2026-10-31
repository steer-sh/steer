package statusline

import (
	"os"
	"path/filepath"
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
			want: "steer ⏻ 3×gemini-3.7-flash-high · 2 decisions → /steer-report",
		},
		{
			name: "one decision carries the singular noun",
			in:   Inputs{Armed: true, Model: "claude-opus-5", Runs: 1, Decisions: 1, Observed: true},
			want: "steer ⏻ 1×claude-opus-5 · 1 decision → /steer-report",
		},
		{
			name: "an armed gate with no ledger row has never fired",
			in:   Inputs{Armed: true, Model: "gemini-3.7-flash-high", Runs: 3, Observed: false},
			want: "steer ⏻ 3×gemini-3.7-flash-high · never fired",
		},
		{
			name: "healthy needs an observed row",
			in:   Inputs{Armed: true, Model: "gemini-3.7-flash-high", Runs: 3, Observed: true},
			want: "steer ⏻ 3×gemini-3.7-flash-high · healthy",
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

// The version is what distinguishes one reviewer from the next — 3.5 flash and 3.8
// flash are different instruments — so the model is printed as configured.
func TestTheReviewerLabelKeepsTheWholeModelID(t *testing.T) {
	for _, model := range []string{
		"gemini-3.7-flash-high",
		"claude-opus-5",
		"claude-haiku-4-5-20251001",
		"my-org/fine-tuned-review-model-v2",
	} {
		row := Render(Inputs{Armed: true, Model: model, Runs: 3, Observed: true})
		if !strings.Contains(row, "3×"+model) {
			t.Errorf("Render() = %q, want it to carry 3×%s", row, model)
		}
	}
}

func TestTheReviewerLabelIsOmittedWhenItIsUnknown(t *testing.T) {
	for _, in := range []Inputs{
		{Armed: true, Runs: 3, Observed: true},
		{Armed: true, Model: "claude-opus-5", Observed: true},
		{Armed: true, Model: "   ", Runs: 3, Observed: true},
	} {
		if got := Render(in); got != "steer ⏻ healthy" {
			t.Errorf("Render(%+v) = %q, want the reviewer omitted", in, got)
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

// TestCollectNamesTheModelTheConfiguredProviderRuns reads the reviewer through the
// settings file, where each provider reports its own default model.
func TestCollectNamesTheModelTheConfiguredProviderRuns(t *testing.T) {
	cases := []struct {
		name, reviewer, want string
	}{
		{"a configured model", `{"provider":"claude-code","model":"claude-sonnet-5-5"}`, "claude-sonnet-5-5"},
		{"Steer's default for the provider", `{"provider":"codex"}`, "gpt-6-luna"},
		{"the CLI's own default", `{"provider":"agy"}`, "agy"},
		{"no reviewer for an unknown provider", `{"provider":"nonesuch"}`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			t.Setenv("COMMIT_GATE_MODEL", "")
			dir := filepath.Join(home, ".steer")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			doc := `{"reviewer":` + tc.reviewer + `}`
			if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(doc), 0o644); err != nil {
				t.Fatal(err)
			}
			if got := Collect().Model; got != tc.want {
				t.Fatalf("Collect().Model = %q; want %q", got, tc.want)
			}
		})
	}
}
