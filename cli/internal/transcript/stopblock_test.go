package transcript

import "testing"

// A run line matches only as a whole line, so one run's line never finds a run
// whose identifier it prefixes.
func TestHasLineMatchesOnlyAWholeLine(t *testing.T) {
	for text, want := range map[string]bool{
		"steer check run: 1a":                       true,
		"failed\n\nsteer check run: 1a":             true,
		"steer check run: 1a\nanother hook":         true,
		"steer check run: 1a\r\n":                   true,
		"failed\nsteer check run: 1ab":              false,
		"failed\nxsteer check run: 1a":              false,
		"steer check run: 1ab\nsteer check run: 1a": true,
	} {
		if got := HasLine(text, "steer check run: 1a"); got != want {
			t.Errorf("HasLine(%q) = %v; want %v", text, got, want)
		}
	}
	if HasLine("anything", "") {
		t.Error("an empty line matched")
	}
}

// The context keeps the prompt that opened the turn and the steps closest to
// the block, and says how many it left between them.
func TestKeepContextKeepsThePromptAndTheNearestSteps(t *testing.T) {
	steps := []ContinuationStep{{Kind: StepHumanTurn, Text: "go"}}
	for _, text := range []string{"a", "b", "c", "d"} {
		steps = append(steps, ContinuationStep{Kind: StepAssistantText, Text: text})
	}
	kept, omitted := KeepContext(steps, ContinuationBudget{ContextSteps: 3, TextBytes: 1})
	if len(kept) != 3 || kept[0].Text != "g" || !kept[0].Truncated || kept[1].Text != "c" || kept[2].Text != "d" || omitted != 2 {
		t.Errorf("kept %+v, omitted %d; want the clipped prompt, c and d, two omitted", kept, omitted)
	}
	kept, omitted = KeepContext(steps[1:], ContinuationBudget{ContextSteps: 2})
	if len(kept) != 2 || kept[0].Text != "c" || omitted != 2 {
		t.Errorf("kept %+v, omitted %d; want c and d without a prompt to keep", kept, omitted)
	}
	if kept, omitted = KeepContext(steps, ContinuationBudget{ContextSteps: -1}); len(kept) != 0 || omitted != 5 {
		t.Errorf("a negative limit kept %d and omitted %d; want none kept", len(kept), omitted)
	}
}
