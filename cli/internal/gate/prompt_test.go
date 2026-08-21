package gate

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

// The digests of the five templates as commit-review.py shipped them. review.md's is
// the instrument's prompt half, so a change there reclassifies every row written
// after it — these are golden values, not a checksum of convenience. Regenerate one
// only when the prompt change is the intended change.
var promptDigests = map[string]struct {
	text string
	want string
}{
	"review":     {reviewPrompt, "f39dc72c7b990c0846eee4d85e831909b61b1efb08f2c0761a8a09998bbdef7b"},
	"deny":       {denyTemplate, "e6aac292d4c89503db59d10f42a51e738af6ace329e775506e2871dda827b094"},
	"cap":        {capTemplate, "2df543b083f47bb57de0d44df86a63c5afbfd9731c294bb9db1f26c903471b7c"},
	"round-note": {roundNote, "f40acabd50e95481f309c08ccacf4cac025835d79e8e265601170f7c4a4d34c3"},
	"rerun-note": {rerunNoteText, "e3e48ce9edf1a6b74b2fd347234805b8c26decd553b2549d582918d00922b4e8"},
}

func TestPromptsAreByteIdenticalToPython(t *testing.T) {
	for name, tc := range promptDigests {
		sum := sha256.Sum256([]byte(tc.text))
		if got := hex.EncodeToString(sum[:]); got != tc.want {
			t.Errorf("%s.md digest = %s, want %s", name, got, tc.want)
		}
	}
}

func TestInstrumentID(t *testing.T) {
	c := Defaults()
	if got, want := c.InstrumentID(), "gemini-3.7-flash-high/3/f39dc72c/go"; got != want {
		t.Errorf("InstrumentID() = %q, want %q", got, want)
	}
}

// Every placeholder the templates carry must be filled, or a literal `{firing}`
// reaches the agent.
func TestNoPlaceholderSurvivesFill(t *testing.T) {
	prompt := BuildReviewPrompt("msg", "diff", "ctx")
	deny := BuildDeny(3, "reviews", 2, 4, rerunNoteText)
	capped := BuildCap(3, "reviews", 5, 4)
	for name, out := range map[string]string{"review": prompt, "deny": deny, "cap": capped} {
		for _, placeholder := range []string{
			"{message}", "{diff}", "{context}", "{count}", "{review}",
			"{firing}", "{cap}", "{round_note}", "{rerun_note}",
		} {
			if strings.Contains(out, placeholder) {
				t.Errorf("%s output still carries %s", name, placeholder)
			}
		}
	}
}

func TestFiringOneHasNoRoundNote(t *testing.T) {
	if strings.Contains(BuildDeny(3, "r", 1, 4, ""), "This is firing") {
		t.Error("firing 1 must not carry the round note")
	}
	if !strings.Contains(BuildDeny(3, "r", 2, 4, ""), "This is firing 2 of at most 4") {
		t.Error("firing 2 must carry the round note")
	}
}
