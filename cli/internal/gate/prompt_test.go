package gate

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

// The digests of the five templates. Four are still byte-identical to what
// commit-review.py shipped; review.md is not, and its digest is the instrument's
// prompt half, so rows written after that change are a different instrument.
// These are golden values, not a checksum of convenience. Regenerate one only
// when the prompt change is the intended change.
var promptDigests = map[string]struct {
	text string
	want string
}{
	"review":     {reviewPrompt, "956536885eab8804f8029edc29953336a7c4664e09a0a09a58f59cf48df00b2c"},
	"deny":       {denyTemplate, "0d59cc3099d745f60df34ade4c059c8e4e1e2993663df5ae1e56f64a11582dbb"},
	"cap":        {capTemplate, "2879eff9c450d4a1469208c0f2037addcc74f62f3dfcb82345508add0fd592d8"},
	"round-note": {roundNote, "f40acabd50e95481f309c08ccacf4cac025835d79e8e265601170f7c4a4d34c3"},
	"rerun-note": {rerunNoteText, "e3e48ce9edf1a6b74b2fd347234805b8c26decd553b2549d582918d00922b4e8"},
}

func TestPromptDigestsArePinned(t *testing.T) {
	for name, tc := range promptDigests {
		sum := sha256.Sum256([]byte(tc.text))
		if got := hex.EncodeToString(sum[:]); got != tc.want {
			t.Errorf("%s.md digest = %s, want %s", name, got, tc.want)
		}
	}
}

func TestInstrumentID(t *testing.T) {
	c := Defaults()
	if got, want := c.InstrumentID(), "gemini-3.7-flash-high/3/95653688/go-findings-v2"; got != want {
		t.Errorf("InstrumentID() = %q, want %q", got, want)
	}
}

// Every placeholder the templates carry must be filled, or a literal `{firing}`
// reaches the agent.
func TestNoPlaceholderSurvivesFill(t *testing.T) {
	prompt := BuildReviewPrompt("msg", "diff", "ctx")
	deny := BuildDeny(3, "reviews", 2, 4, rerunNoteText, "feat: a subject")
	capped := BuildCap(3, "reviews", 5, 4, "feat: a subject")
	for name, out := range map[string]string{"review": prompt, "deny": deny, "cap": capped} {
		for _, placeholder := range []string{
			"{message}", "{diff}", "{context}", "{count}", "{review}",
			"{firing}", "{cap}", "{round_note}", "{rerun_note}", "{subject}",
		} {
			if strings.Contains(out, placeholder) {
				t.Errorf("%s output still carries %s", name, placeholder)
			}
		}
	}
}

// The subject keys one chain. Two chains in one repo differ by it and by nothing else
// a row carries, and same-repo chain pairs outnumber cross-repo ones in every session.
func TestCommitSubject(t *testing.T) {
	cases := []struct{ in, want, why string }{
		{"feat: a thing\n\nbody line\n", "feat: a thing", "the first real line, not the body"},
		{"\n\n  feat: padded  \n", "feat: padded", "leading blank lines and padding go"},
		{"# a comment\nfeat: real\n", "feat: real", "a git comment line is not the subject"},
		{"", "", "no message, no key — those chains keep the bound they had"},
		{"#only a comment\n", "", "a message of comments alone yields nothing"},
	}
	for _, tc := range cases {
		if got := CommitSubject(tc.in); got != tc.want {
			t.Errorf("CommitSubject(%q) = %q, want %q — %s", tc.in, got, tc.want, tc.why)
		}
	}
	// Truncation is by rune. A byte cut lands mid-rune on any subject with an em-dash,
	// and the row and the header would then disagree on the key.
	long := strings.Repeat("é", MaxSubjectChars+20)
	if got := []rune(CommitSubject(long)); len(got) != MaxSubjectChars {
		t.Errorf("truncated to %d runes, want %d", len(got), MaxSubjectChars)
	}
}

// The subject the row stores and the subject the agent is handed must be one string.
func TestSubjectReachesTheHeader(t *testing.T) {
	const subject = "fix(resolver): chain join — one ledger per chain"
	if !strings.Contains(BuildDeny(3, "r", 2, 4, "", subject), "chain ledger, "+subject+"**") {
		t.Error("the deny header must carry the subject verbatim")
	}
	if !strings.Contains(BuildCap(3, "r", 5, 4, subject), "chain ledger, "+subject+"**") {
		t.Error("the terminal header must carry it too")
	}
	if !strings.Contains(BuildDeny(3, "r", 2, 4, "", ""), "chain ledger, "+NoSubject+"**") {
		t.Error("an unread commit message still renders a header, matching no row")
	}
}

func TestFiringOneHasNoRoundNote(t *testing.T) {
	if strings.Contains(BuildDeny(3, "r", 1, 4, "", "s"), "This is firing") {
		t.Error("firing 1 must not carry the round note")
	}
	if !strings.Contains(BuildDeny(3, "r", 2, 4, "", "s"), "This is firing 2 of at most 4") {
		t.Error("firing 2 must carry the round note")
	}
}

// The prompt's own example is the parser's input contract. If a future edit rewrites
// the example into a shape CountFindings does not match, the gate would ask for a
// format it cannot count.
func TestReviewPromptExampleParsesAsFindings(t *testing.T) {
	if got := CountFindings(reviewPrompt); got != 2 {
		t.Errorf("strict counter reads %d findings in the prompt's example block, want 2", got)
	}
	if !strings.Contains(reviewPrompt, "NO FINDINGS") {
		t.Error("prompt no longer names the clean reply")
	}
}
