package firingaudit

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/adjudication"
	"github.com/steer-sh/steer-cli/internal/transcript"
)

// caseWithSteps builds a case whose continuation and prior readings are large
// enough that an allowance has to withhold some of them.
func caseWithSteps(n int) Case {
	c := Case{
		Row: Row{FiringID: "f1", HookName: "no-bare-python", Harness: "claude-code",
			Rule: "never use bare python"},
		Evidence: Evidence{Source: Inline,
			Bundle: json.RawMessage(`{"command":"python x.py"}`)},
		Continuation: transcript.Continuation{Granularity: transcript.JoinCall},
		Prior: []adjudication.Receipt{{Judgment: adjudication.Unresolved,
			Rationale: strings.Repeat("prior ", 40)}},
	}
	for i := 0; i < n; i++ {
		c.Continuation.Steps = append(c.Continuation.Steps,
			transcript.ContinuationStep{Kind: transcript.StepToolCall, Tool: "Bash",
				Target: strings.Repeat("step ", 40)})
	}
	return c
}

func TestFitReportsItsOwnSizeExactly(t *testing.T) {
	for _, render := range []Render{RenderJSON, textRender} {
		out, e, err := Fit(caseWithSteps(6), 0, render)
		if err != nil {
			t.Fatal(err)
		}
		if e.EmittedBytes != len(out) {
			t.Errorf("the count %d is not the output's length %d", e.EmittedBytes, len(out))
		}
		if !strings.Contains(string(out), "emitted_bytes") {
			t.Errorf("the output does not carry its own count:\n%s", out)
		}
	}
}

func TestFitStaysWithinTheAllowanceAndSaysWhatItWithheld(t *testing.T) {
	full, _, err := Fit(caseWithSteps(6), 0, RenderJSON)
	if err != nil {
		t.Fatal(err)
	}
	max := len(full) / 2
	out, e, err := Fit(caseWithSteps(6), max, RenderJSON)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) > max {
		t.Fatalf("the rendering is %d bytes against an allowance of %d", len(out), max)
	}
	if e.EmittedBytes != len(out) {
		t.Errorf("the count %d is not the output's length %d", e.EmittedBytes, len(out))
	}
	if e.StepsEmitted >= e.StepsAvailable {
		t.Errorf("a clipped rendering reported %d of %d steps", e.StepsEmitted, e.StepsAvailable)
	}
	if len(e.Clipped) == 0 {
		t.Error("a clipped rendering named nothing it withheld")
	}
	// The JSON survives the clip as JSON: a clip drops whole values.
	var decoded map[string]any
	if err := json.Unmarshal(out, &decoded); err != nil {
		t.Fatalf("the clipped rendering is not valid JSON: %v\n%s", err, out)
	}
}

func TestFitWithholdsPriorReadingsBeforeContinuationSteps(t *testing.T) {
	c := caseWithSteps(6)
	full, _, err := Fit(c, 0, RenderJSON)
	if err != nil {
		t.Fatal(err)
	}
	// An allowance one byte short of everything drops the prior reading and
	// keeps every step: the block's own evidence outranks an earlier reading.
	out, e, err := Fit(c, len(full)-1, RenderJSON)
	if err != nil {
		t.Fatal(err)
	}
	if e.StepsEmitted != e.StepsAvailable {
		t.Errorf("steps were withheld before the prior reading was: %d of %d",
			e.StepsEmitted, e.StepsAvailable)
	}
	var decoded map[string]any
	if err := json.Unmarshal(out, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["prior"] != nil {
		t.Errorf("the prior reading survived the clip:\n%s", out)
	}
}

func TestFitRefusesAnAllowanceThatCannotCarryTheBlockedAction(t *testing.T) {
	_, _, err := Fit(caseWithSteps(6), 20, RenderJSON)
	if err == nil {
		t.Fatal("a 20-byte allowance rendered a case")
	}
	for _, want := range []string{"row and blocked action", "--max-bytes"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
}

// textRender is a minimal non-JSON rendering, so the fixed point is tested on
// a shape whose count is not inside a JSON object.
func textRender(c Case, e Emission) ([]byte, error) {
	var b strings.Builder
	b.WriteString("firing " + c.Row.FiringID + "\n")
	b.WriteString(string(c.Evidence.Bundle) + "\n")
	for _, s := range c.Continuation.Steps {
		b.WriteString(s.Target + "\n")
	}
	for _, p := range c.Prior {
		b.WriteString("prior " + p.Rationale + "\n")
	}
	for _, clip := range e.Clipped {
		b.WriteString(clip + "\n")
	}
	b.WriteString("emitted_bytes ")
	b.WriteString(itoa(e.EmittedBytes))
	b.WriteString("\n")
	return []byte(b.String()), nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var d []byte
	for n > 0 {
		d = append([]byte{byte('0' + n%10)}, d...)
		n /= 10
	}
	return string(d)
}
