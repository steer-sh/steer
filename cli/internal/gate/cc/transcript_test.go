package cc

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/gate"
)

// Ledger-recovery cases ported from hooks/commit-review/test_ledger.py. The two
// failure directions are NOT symmetric: a MISSED ledger loses one row and the
// transcript is still on disk, while a FORGED ledger — verdict bullets scraped out of
// ordinary prose about the gate — writes a row indistinguishable from a real one,
// permanently. Every case asserting nil is that guard.

// t0 stands in for the deny row's ts and MUST sit between before and after.
const (
	tsBefore = "2026-08-03T16:00:00.000Z"
	t0       = 1785774000.0
	tsAfter  = "2026-08-03T17:05:47.482Z"
	tsLater  = "2026-08-03T17:30:00.000Z"
)

const realLedger = "**Commit gate fired** — 3 reviewers, 8 raw findings, 3 distinct concerns.\n\n" +
	"- **ACCEPTED** — a run could be reported by two consecutive heartbeats → stamps a fresh clock read, `runner.py:186`\n" +
	"- **ACCEPTED** — an explicit `\"epoch\": null` would crash the heartbeat → numeric guard at `engine/logs.py:88`\n" +
	"- **REJECTED** — all three reviewers claimed `--dry-run` pollutes the outcomes log → the premise is that a dry run reaches those lines; it doesn't.\n" +
	"- Weakest surviving: the null-epoch crash — no current writer can produce it\n"

// The forgery hazard, verbatim in shape: prose about the gate, with a bullet that
// would match a verdict line on its own. No header, so it must not parse.
const proseLedger = "I checked how the gate records things. The template asks for lines like\n\n" +
	"- **ACCEPTED** — <the concern in one clause> → <the change you made, file:line>\n\n" +
	"and nothing currently counts them.\n"

const asciiArrowLedger = "**Commit gate fired** — 3 reviewers, 2 raw findings, 1 distinct concern.\n\n" +
	"- **REJECTED** - the caller is never reached -> the premise is false\n"

func transcriptRowJSON(t *testing.T, text, ts, kind string, sidechain bool) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"type": kind, "timestamp": ts, "isSidechain": sidechain,
		"message": map[string]any{
			"role":    "assistant",
			"content": []map[string]string{{"type": "text", "text": text}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func writeTranscript(t *testing.T, rows ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(rows, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func assistantRow(t *testing.T, text string) string {
	return transcriptRowJSON(t, text, tsAfter, "assistant", false)
}

func TestReadLedgerHappyPath(t *testing.T) {
	led := ReadLedger(writeTranscript(t, assistantRow(t, realLedger)), t0, 0)
	if led == nil {
		t.Fatal("a real ledger must parse")
	}
	if led["self_reported_accepted"] != 2 {
		t.Errorf("accepted = %v, want 2 — two ACCEPTED bullets", led["self_reported_accepted"])
	}
	if led["self_reported_rejected"] != 1 {
		t.Errorf("rejected = %v, want 1 — one REJECTED bullet", led["self_reported_rejected"])
	}
	concerns := led["concerns"].([]gate.Concern)
	if len(concerns) != 3 {
		t.Fatalf("concerns = %d, want 3 — `Weakest surviving:` is not a verdict line", len(concerns))
	}
	if want := "a run could be reported by two consecutive heartbeats"; concerns[0].Concern != want {
		t.Errorf("concern = %q, want %q — the text BEFORE the arrow, not the resolution",
			concerns[0].Concern, want)
	}
	if concerns[2].Verdict != "REJECTED" {
		t.Errorf("verdict = %q, want REJECTED", concerns[2].Verdict)
	}
	if led["concerns_total"] != 3 {
		t.Errorf("concerns_total = %v, want 3", led["concerns_total"])
	}
}

func TestReadLedgerASCIIArrow(t *testing.T) {
	led := ReadLedger(writeTranscript(t, assistantRow(t, asciiArrowLedger)), t0, 0)
	if led == nil {
		t.Fatal("an ASCII-arrow ledger must parse")
	}
	concerns := led["concerns"].([]gate.Concern)
	if want := "the caller is never reached"; concerns[0].Concern != want {
		t.Errorf("concern = %q, want %q — `->` splits like `→`, and the separator "+
			"hyphen must not eat the arrow's head", concerns[0].Concern, want)
	}
}

// The guards are the actual feature: the counts are worth less than the guarantee that
// a recorded count was actually claimed by the agent.
func TestReadLedgerGuards(t *testing.T) {
	cases := []struct {
		name, path string
		why        string
	}{
		{"forgery", writeTranscript(t, assistantRow(t, proseLedger)),
			"verdict bullets in prose, no header: must NOT forge a row"},
		{"stale", writeTranscript(t, transcriptRowJSON(t, realLedger, tsBefore, "assistant", false)),
			"a ledger predating the deny belongs to an earlier gate"},
		{"sidechain", writeTranscript(t, transcriptRowJSON(t, realLedger, tsAfter, "assistant", true)),
			"a subagent's text never reached the operator, so it is not a report"},
		{"user-role", writeTranscript(t, transcriptRowJSON(t, realLedger, tsAfter, "user", false)),
			"tool results echo hook text back; only assistant messages are the report"},
		{"no-transcript", "", "payload without a path"},
		{"missing-file", "/nonexistent/x.jsonl", "unreadable transcript fails to nil, never raises"},
		{"empty", writeTranscript(t, assistantRow(t, "nothing to see")),
			"a fired gate with no ledger is ledger_found: false, which is itself signal"},
	}
	for _, tc := range cases {
		if got := ReadLedger(tc.path, t0, 0); got != nil {
			t.Errorf("%s: ReadLedger() = %v, want nil — %s", tc.name, got, tc.why)
		}
	}
}

func TestReadLedgerTruncation(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("**Commit gate fired** — 3 reviewers.\n\n")
	total := gate.MaxConcerns + 5
	for i := 0; i < total; i++ {
		sb.WriteString("- **ACCEPTED** — concern → fixed\n")
	}
	led := ReadLedger(writeTranscript(t, assistantRow(t, sb.String())), t0, 0)
	if got := len(led["concerns"].([]gate.Concern)); got != gate.MaxConcerns {
		t.Errorf("stored concerns = %d, want %d — the cap bounds a row's size", got, gate.MaxConcerns)
	}
	if led["concerns_total"] != total {
		t.Errorf("concerns_total = %v, want %d — the true total the agent adjudicated",
			led["concerns_total"], total)
	}
	if led["self_reported_accepted"] != total {
		t.Errorf("accepted = %v, want %d — the rate is measured over every verdict, "+
			"not the surviving text", led["self_reported_accepted"], total)
	}
}

func TestReadLedgerRestatementWins(t *testing.T) {
	restated := strings.ReplaceAll(realLedger, "- **REJECTED**", "- **ACCEPTED**")
	led := ReadLedger(writeTranscript(t,
		assistantRow(t, realLedger),
		transcriptRowJSON(t, restated, tsLater, "assistant", false)), t0, 0)
	if led["self_reported_accepted"] != 3 {
		t.Errorf("accepted = %v, want 3 — a later restatement is the more accurate account",
			led["self_reported_accepted"])
	}
}

// The round marker is the join key that files a ledger against the round it answers.
func TestLedgerRound(t *testing.T) {
	r2 := strings.Replace(realLedger, "**Commit gate fired**", "**Commit gate fired (round 2/4)**", 1)
	r3 := strings.Replace(realLedger, "**Commit gate fired**", "**Commit gate fired (round 3/4)**", 1)

	if got, ok := gate.LedgerRound(r2); !ok || got != 2 {
		t.Errorf("gate.LedgerRound(r2) = %d,%v, want 2,true — the marker is read off the header", got, ok)
	}
	if _, ok := gate.LedgerRound(realLedger); ok {
		t.Error("an unmarked ledger must report no round rather than a wrong one")
	}
	if led := ReadLedger(writeTranscript(t, assistantRow(t, r2)), t0, 2); led["ledger_round"] != 2 {
		t.Errorf("ledger_round = %v, want 2 — the round travels onto the row", led["ledger_round"])
	}
	if led := ReadLedger(writeTranscript(t, assistantRow(t, r3)), t0, 2); led != nil {
		t.Error("a ledger naming round 3 does not answer round 2, however close in time")
	}
	if led := ReadLedger(writeTranscript(t, assistantRow(t, realLedger)), t0, 2); led == nil {
		t.Error("the marker is newer than some transcripts; an unmarked ledger still counts")
	}
	// A chain ledger carries no header round either, but it is not an old-shape ledger:
	// it covers every round, so summing it onto this one round is a wrong join that the
	// resolver can never revisit.
	chained := "**Commit gate fired — chain ledger**\n\n**Round 1/4** — 3 reviewers.\n" +
		"\n- **ACCEPTED** — a concern → a fix\n\n**Round 2/4** — 3 reviewers.\n" +
		"\n- **REJECTED** — another concern → a reason\n"
	if !gate.HasRoundSections(chained) || gate.HasRoundSections(realLedger) {
		t.Error("a section is `**Round N/`; a per-round ledger's header is not one")
	}
	if led := ReadLedger(writeTranscript(t, assistantRow(t, chained)), t0, 2); led != nil {
		t.Error("a chain ledger answers the whole chain, so no single round captures it")
	}
	if led := ReadLedger(writeTranscript(t, assistantRow(t, chained)), t0, 0); led != nil {
		t.Error("no round owns a chain ledger, so the round asked for does not decide " +
			"it — firing 0 must not be a way past the check")
	}
	marked := strings.Replace(chained, "**Commit gate fired —",
		"**Commit gate fired (round 2/4) —", 1)
	if led := ReadLedger(writeTranscript(t, assistantRow(t, marked)), t0, 2); led != nil {
		t.Error("sections decide, not the header; a stray marker matches this firing " +
			"exactly when the text is this chain's own ledger")
	}
	led := ReadLedger(writeTranscript(t,
		assistantRow(t, r3), transcriptRowJSON(t, r2, tsLater, "assistant", false)), t0, 3)
	if led["ledger_round"] != 3 {
		t.Errorf("ledger_round = %v, want 3 — the LAST qualifying message wins, but only "+
			"among those for this round", led["ledger_round"])
	}
	led = ReadLedger(writeTranscript(t,
		assistantRow(t, r2), transcriptRowJSON(t, realLedger, tsLater, "assistant", false)), t0, 2)
	if led["ledger_round"] != 2 {
		t.Errorf("ledger_round = %v, want 2 — recency decides among equals, not between "+
			"a round match and a stranger", led["ledger_round"])
	}

	// A reply that discusses the gate before reporting on it. The round is read off the
	// header line, so the sentence above it is not a candidate.
	proseFirst := "As noted when the commit gate fired (round 1), the caller was already fixed.\n\n" + r2
	if got, _ := gate.LedgerRound(proseFirst); got != 2 {
		t.Errorf("gate.LedgerRound(prose decoy) = %d, want 2 — the header line owns the round", got)
	}
	if _, ok := gate.LedgerRound("recall that the Commit gate fired (round 3) earlier today"); ok {
		t.Error("a round named only in a sentence is not a marker at all")
	}
	if got, _ := gate.LedgerRound("- " + r2); got != 2 {
		t.Errorf("gate.LedgerRound(bulleted) = %d, want 2 — a bulleted header is still a header", got)
	}
	if got, _ := gate.LedgerRound("> " + r2); got != 2 {
		t.Errorf("gate.LedgerRound(quoted) = %d, want 2 — likewise a blockquote", got)
	}
}

// The shape the agent is told to write is a contract with its readers, so this asserts
// against the RENDERED template rather than a copy of it. The ledger's unit is the
// chain: its header names no round, and each round appears as its own section. Only the
// Python resolver parses those sections, so the sections are pinned here as text.
func TestTemplateHeadersCarryNoRound(t *testing.T) {
	deny := gate.BuildDeny(3, "r", 2, 4, "", "s")
	if _, ok := gate.LedgerRound(deny); ok {
		t.Error("the chain ledger's header names no round")
	}
	for _, want := range []string{"**Round 1/4**", "**Round 2/4**"} {
		if !strings.Contains(deny, want) {
			t.Errorf("deny template is missing the %s section", want)
		}
	}
	capped := gate.BuildCap(3, "r", 5, 4, "s")
	if _, ok := gate.LedgerRound(capped); ok {
		t.Error("the terminal template hands over that same chain header")
	}
	for _, want := range []string{"**Round 1/4**", "**Round 5, terminal**"} {
		if !strings.Contains(capped, want) {
			t.Errorf("cap template is missing the %s section", want)
		}
	}
}

// The harness writes its own text into user turns. Stripping it needs the closing tag
// to be the SAME tag, which RE2 cannot express, so an unpaired opener must be kept
// rather than swallowing the rest of the turn.
func TestStripHarnessText(t *testing.T) {
	cases := []struct{ in, want, why string }{
		{"a<system-reminder>noise</system-reminder>b", "ab", "a paired block is removed"},
		{"a<system-reminder>x</command-name>b", "a<system-reminder>x</command-name>b",
			"a mismatched closer is not this tag's closer, so nothing is removed"},
		{"<local-command-stdout>out</local-command-stdout>ask", "ask", "slash-command stdout"},
		{"keep <b>bold</b>", "keep <b>bold</b>", "an unrelated tag is ordinary text"},
		{"<system-reminder>a</system-reminder>mid<system-reminder>b</system-reminder>", "mid",
			"every occurrence, not just the first"},
	}
	for _, tc := range cases {
		if got := stripHarnessText(tc.in); got != tc.want {
			t.Errorf("stripHarnessText(%q) = %q, want %q — %s", tc.in, got, tc.want, tc.why)
		}
	}
}
