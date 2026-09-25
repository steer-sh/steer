package gate

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

// The digests of the five templates. review.md's digest is the instrument's prompt
// half, so a change there makes every row written afterwards a different instrument;
// deny.md and cap.md are read by the agent alone and move no instrument.
// These are golden values, not a checksum of convenience. Regenerate one only
// when the prompt change is the intended change.
var promptDigests = map[string]struct {
	text string
	want string
}{
	"review":     {reviewPrompt, "956536885eab8804f8029edc29953336a7c4664e09a0a09a58f59cf48df00b2c"},
	"deny":       {denyTemplate, "413e7e6b933fb6b79c9cfe91c2cec26d5d6e557beab1f9a87585f4ca7bc175bb"},
	"cap":        {capTemplate, "c48ff124e9ef6cbae111a3e66367ea2ec371b8840de9c76b8955ad7991e2a1bc"},
	"round-note": {roundNote, "f40acabd50e95481f309c08ccacf4cac025835d79e8e265601170f7c4a4d34c3"},
	"rerun-note": {rerunNoteText, "27d14c6916336301abd551b90d82d5fc799e027ae7d95b0520ef501c0efa9f1b"},
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
	if got, want := c.InstrumentID(), "gemini-3.7-flash-high/3/95653688/go-findings-v3-shell"; got != want {
		t.Errorf("InstrumentID() = %q, want %q", got, want)
	}
}

// Every placeholder the templates carry must be filled, or a literal `{firing}`
// reaches the agent.
func TestNoPlaceholderSurvivesFill(t *testing.T) {
	prompt := BuildReviewPrompt("msg", "diff", "ctx", Addendum{})
	deny := BuildDeny(3, "reviews", 2, 4, rerunNoteText, "feat: a subject", "", "a reviewer")
	capped := BuildCap(3, "reviews", 5, 4, "feat: a subject", "", "a reviewer")
	for name, out := range map[string]string{"review": prompt, "deny": deny, "cap": capped} {
		for _, placeholder := range []string{
			"{message}", "{diff}", "{context}", "{count}", "{review}",
			"{firing}", "{cap}", "{round_note}", "{rerun_note}", "{subject}",
			"{chain}", "{reviewer}",
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
	if !strings.Contains(BuildDeny(3, "r", 2, 4, "", subject, "", "a reviewer"), "review sequence, "+subject+"**") {
		t.Error("the deny header must carry the subject verbatim")
	}
	if !strings.Contains(BuildCap(3, "r", 5, 4, subject, "", "a reviewer"), "review sequence, "+subject+"**") {
		t.Error("the terminal header must carry it too")
	}
	if !strings.Contains(BuildDeny(3, "r", 2, 4, "", "", "", "a reviewer"), "review sequence, "+NoSubject+"**") {
		t.Error("an unread commit message still renders a header, matching no row")
	}
}

func TestChainIDReachesTheHeader(t *testing.T) {
	const subject = "fix(resolver): chain join"
	for name, out := range map[string]string{
		"deny": BuildDeny(3, "r", 2, 4, "", subject, "7f3a2b9c", "a reviewer"),
		"cap":  BuildCap(3, "r", 5, 4, subject, "7f3a2b9c", "a reviewer"),
	} {
		start := strings.Index(out, "**"+LedgerHeader+" — review sequence")
		if start < 0 {
			t.Fatalf("%s renders no chain header", name)
		}
		header := strings.SplitN(out[start:], "\n", 2)[0]
		if BlockChainID(header) != "7f3a2b9c" || BlockSubject(header) != subject {
			t.Errorf("%s header %q must read back as the ID and the subject", name, header)
		}
	}
	legacy := BuildDeny(3, "r", 2, 4, "", subject, "", "a reviewer")
	if !strings.Contains(legacy, "review sequence, "+subject+"**") {
		t.Error("a chain with no ID renders the header it had before")
	}
	if BlockChainID("**Commit gate fired — chain ledger, fix: #abc123 in the parser**") != "" {
		t.Error("a hash inside the subject is not a chain ID")
	}
}

func TestFiringOneHasNoRoundNote(t *testing.T) {
	if strings.Contains(BuildDeny(3, "r", 1, 4, "", "s", "", "a reviewer"), "This is firing") {
		t.Error("firing 1 must not carry the round note")
	}
	if !strings.Contains(BuildDeny(3, "r", 2, 4, "", "s", "", "a reviewer"), "This is firing 2 of at most 4") {
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

// The deny and cap templates tell the agent who wrote the reviews, and that name
// comes from the reviewer that ran. A provider or model spelled into the template
// would name the wrong panel on every machine configured differently.
func TestTemplatesNameNoReviewer(t *testing.T) {
	for name, tmpl := range map[string]string{"deny": denyTemplate, "cap": capTemplate, "review": reviewPrompt} {
		for _, word := range []string{"gemini", "agy", "codex", "claude", "anthropic"} {
			if strings.Contains(strings.ToLower(tmpl), word) {
				t.Errorf("%s.md names %q; the reviewer is a placeholder", name, word)
			}
		}
	}
}

func TestReviewerLabel(t *testing.T) {
	cases := []struct{ provider, model, want string }{
		{"agy", "gemini-3.7-flash-high", "gemini-3.7-flash-high, via `agy`"},
		{"codex", "codex-default", "via `codex`"},
		{"claude-code", "", "via `claude-code`"},
		{"", "anything", "the configured reviewer"},
	}
	for _, tc := range cases {
		c := Config{ReviewerProvider: tc.provider, ReviewerModel: tc.model}
		if got := c.ReviewerLabel(); got != tc.want {
			t.Errorf("ReviewerLabel(%q, %q) = %q, want %q", tc.provider, tc.model, got, tc.want)
		}
	}
}
