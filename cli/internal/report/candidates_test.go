package report

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/transcript"
)

// flaggedTurn and quietTurn sit on each side of the threshold with no tool call.
const (
	flaggedTurn = "stop wrong"
	quietTurn   = "please rename the helper"
)

func toolCallEvent(session, thread, tool string) transcript.Event {
	return transcript.Event{Kind: transcript.KindToolCall, SessionID: session, ThreadID: thread, Tool: tool, ToolUseID: tool}
}

func candidateTexts(d *Digest) []string {
	var out []string
	for _, c := range d.Signals.CorrectionCandidates {
		out = append(out, c.Pattern)
	}
	return out
}

// The scoring agrees with the reference implementation on every feature and on
// the score, over the synthetic turns in the golden file.
func TestCorrectionScoreMatchesReference(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "correction_list_golden.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name           string   `json:"name"`
		Text           string   `json:"text"`
		Tools          []string `json:"tools"`
		Features       int      `json:"features"`
		FeaturesSHA256 string   `json:"features_sha256"`
		Listed         []string `json:"listed"`
		Score          float64  `json:"score"`
		Flagged        bool     `json:"flagged"`
	}
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) == 0 {
		t.Fatal("no golden cases")
	}
	weights := correctionList().Weights
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			features := correctionFeatures(c.Text, c.Tools)
			sum := sha256.Sum256([]byte(strings.Join(features, "\n")))
			if len(features) != c.Features || hex.EncodeToString(sum[:]) != c.FeaturesSHA256 {
				t.Errorf("features (%d) differ from the reference (%d)", len(features), c.Features)
			}
			listed := []string{}
			for _, f := range features {
				if _, ok := weights[f]; ok {
					listed = append(listed, f)
				}
			}
			if !reflect.DeepEqual(listed, c.Listed) {
				t.Errorf("listed features = %v, want %v", listed, c.Listed)
			}
			score := correctionScore(c.Text, c.Tools)
			if math.Abs(score-c.Score) > 1e-9 {
				t.Errorf("score = %v, want %v", score, c.Score)
			}
			if got := correctionFlagged(score); got != c.Flagged {
				t.Errorf("flagged = %v at %v, want %v", got, score, c.Flagged)
			}
		})
	}
}

// A score must clear zero, and a floating-point sum that lands just above it does not.
func TestCorrectionFlagThresholdAbsorbsSumError(t *testing.T) {
	for score, want := range map[float64]bool{
		0:                     false,
		2.220446049250313e-16: false,
		-0.1:                  false,
		0.00000001:            true,
		0.1:                   true,
	} {
		if got := correctionFlagged(score); got != want {
			t.Errorf("flagged(%v) = %v, want %v", score, got, want)
		}
	}
}

// A flagged turn appears with its score and context through the whole report
// path: the index row, the signal listing and the paged details.
func TestCandidateAppearsThroughTheReportPath(t *testing.T) {
	d := scanEvents(t, []transcript.Event{
		humanTurn("s1", "2026-09-01T00:00:00Z", quietTurn),
		interrupt("s1", "2026-09-01T00:01:00Z", transcript.InterruptTurn),
		humanTurn("s1", "2026-09-01T00:02:00Z", flaggedTurn),
		humanTurn("s2", "2026-09-01T00:00:00Z", "stop  wrong"),
	})
	if got := candidateTexts(d); !reflect.DeepEqual(got, []string{flaggedTurn}) {
		t.Fatalf("candidates = %v", got)
	}
	k := d.Signals.CorrectionCandidates[0]
	if k.ID != "k1" || k.Count != 2 || k.Sessions != 2 {
		t.Errorf("cluster = %+v", k)
	}
	o := k.Occurrences[0]
	if o.Score == nil || *o.Score != 0.9 {
		t.Errorf("score = %v, want -0.8", o.Score)
	}
	if o.PreviousHumanText == nil || o.PreviousHumanText.Text != quietTurn || !o.AfterInterrupt {
		t.Errorf("context = %+v", o)
	}

	raw, err := IndexPage(d, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	var idx Index
	if err := json.Unmarshal(raw, &idx); err != nil {
		t.Fatal(err)
	}
	if idx.CorrectionList != CorrectionListDigest() || !strings.HasPrefix(idx.CorrectionList, "list-") {
		t.Errorf("index correction_list = %q", idx.CorrectionList)
	}
	found := false
	for _, r := range idx.Rows {
		found = found || r.ID == "k1"
	}
	if !found {
		t.Errorf("index rows lack k1: %+v", idx.Rows)
	}

	listing, err := SignalPage(d, kindCandidate, nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(listing), `"k1"`) {
		t.Errorf("listing lacks k1:\n%s", listing)
	}
	raw, err = DetailPage(d, []string{"k1"}, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	var detail Detail
	if err := json.Unmarshal(raw, &detail); err != nil {
		t.Fatal(err)
	}
	if detail.Items[0].Signal != kindCandidate || len(detail.Rows) != 2 ||
		detail.Rows[0].Score == nil || *detail.Rows[0].Score != 0.9 {
		t.Errorf("detail = %s", raw)
	}
}

// A long turn shows all the text its score read: a head past the usual occurrence
// clip, and the tail it cut before collapsing whitespace.
func TestCandidateShowsAllTheTextItsScoreRead(t *testing.T) {
	const tail = "that is wrong, you broke it, you missed it, you forgot it"
	long := strings.Repeat("é", 700) + " midword " + strings.Repeat("é", 1000) + " " + strings.Repeat("é", 300) + strings.Repeat("\n", 600) + tail
	d := scanEvents(t, []transcript.Event{humanTurn("s", "2026-09-01T00:00:00Z", long)})
	k := d.Signals.CorrectionCandidates
	if len(k) != 1 {
		t.Fatalf("candidates = %v", candidateTexts(d))
	}
	if text := k[0].Occurrences[0].Text; text != whitespaceRun.ReplaceAllString(scoreCut(long), " ") || !strings.Contains(text, "midword") || !strings.HasSuffix(text, " "+tail) {
		t.Errorf("occurrence (%d bytes) = %q", len(text), text)
	}
}

// Paging a candidate's occurrences drops none of them, however small the bound.
func TestCandidatePagingIsLossless(t *testing.T) {
	var events []transcript.Event
	for i := 0; i < 40; i++ {
		events = append(events, humanTurn("s"+strings.Repeat("x", i), "2026-09-01T00:00:00Z", flaggedTurn))
	}
	d := scanEvents(t, events)
	seen, cursor := 0, 0
	for {
		raw, err := DetailPage(d, []string{"k1"}, cursor, 500)
		if err != nil {
			t.Fatal(err)
		}
		if len(raw) > 2000 {
			t.Fatalf("a page of %d bytes", len(raw))
		}
		var page Detail
		if err := json.Unmarshal(raw, &page); err != nil {
			t.Fatal(err)
		}
		seen += len(page.Rows)
		if page.Page.NextCursor == 0 {
			break
		}
		cursor = page.Page.NextCursor
	}
	if seen != 40 {
		t.Errorf("paged %d occurrences, want 40", seen)
	}
}

// A higher score ranks first, whatever the text or the spread of a lower one.
func TestCandidatesRankByHighestScore(t *testing.T) {
	d := scanEvents(t, []transcript.Event{
		humanTurn("s1", "2026-09-01T00:00:00Z", "stop wrong"),
		humanTurn("s2", "2026-09-01T00:00:00Z", "stop wrong"),
		humanTurn("s3", "2026-09-01T00:00:00Z", "you forgot the wrong file again"),
	})
	k := d.Signals.CorrectionCandidates
	if len(k) != 2 || k[0].Pattern != "you forgot the wrong file again" {
		t.Errorf("rank = %v", candidateTexts(d))
	}
}

// A candidate is a turn no pattern flagged, typed by the person on the root thread.
func TestCandidateExcludesEveryOtherTurn(t *testing.T) {
	meta := humanTurn("s1", "2026-09-01T00:01:00Z", flaggedTurn)
	meta.Meta = true
	sub := humanTurn("s1", "2026-09-01T00:02:00Z", flaggedTurn)
	sub.ThreadID = "sub-1"
	const regexFlagged = "you forgot it and it is wrong, stop adding files"
	if !correctionFlagged(correctionScore(regexFlagged, nil)) {
		t.Fatalf("the regex turn must also be one the list flags: %v", correctionScore(regexFlagged, nil))
	}
	const invocation = "/steer that is wrong, you forgot it again and you broke the wrong file"
	if !correctionFlagged(correctionScore(invocation, nil)) {
		t.Fatalf("the invocation must be one the list flags: %v", correctionScore(invocation, nil))
	}
	d := scanEvents(t, []transcript.Event{
		meta, sub,
		humanTurn("s1", "2026-09-01T00:03:00Z", regexFlagged),
		humanTurn("s1", "2026-09-01T00:04:00Z", invocation),
		humanTurn("s1", "2026-09-01T00:05:00Z", "<pasted_content>"+flaggedTurn+"</pasted_content>"),
		humanTurn("s1", "2026-09-01T00:06:00Z", "<command-name>"+flaggedTurn+"</command-name>"),
		humanTurn("s1", "2026-09-01T00:07:00Z", quietTurn),
	})
	if got := candidateTexts(d); len(got) != 0 {
		t.Errorf("candidates = %v", got)
	}
	if len(d.Signals.UserCorrections) != 1 {
		t.Errorf("corrections = %+v, want the regex turn alone", d.Signals.UserCorrections)
	}
}

// The tool window holds the last three root-thread calls since the previous typed
// turn. A Meta or paste-only turn leaves it open and a typed turn closes it.
func TestCandidateToolWindow(t *testing.T) {
	const text = "wrong"
	score := func(tools ...string) float64 { return roundScore(correctionScore(text, tools)) }
	meta := humanTurn("s", "2026-09-01T00:00:00Z", "a banner")
	meta.Meta = true
	paste := humanTurn("s", "2026-09-01T00:00:00Z", "<pasted_content>a log</pasted_content>")
	for _, tc := range []struct {
		name   string
		events []transcript.Event
		want   float64
	}{
		{"last three of four", []transcript.Event{
			toolCallEvent("s", "", "Edit"), toolCallEvent("s", "", "Bash"),
			toolCallEvent("s", "", "Bash"), toolCallEvent("s", "", "Bash"),
		}, score("Bash", "Bash", "Bash")},
		{"subagent calls ignored", []transcript.Event{
			toolCallEvent("s", "", "Bash"), toolCallEvent("s", "sub-1", "Edit"),
		}, score("Bash")},
		{"meta turn keeps the window", []transcript.Event{
			toolCallEvent("s", "", "Edit"), meta, toolCallEvent("s", "", "Bash"),
		}, score("Edit", "Bash")},
		{"paste-only turn keeps the window", []transcript.Event{
			toolCallEvent("s", "", "Edit"), paste,
		}, score("Edit")},
		{"typed turn closes the window", []transcript.Event{
			toolCallEvent("s", "", "Edit"), humanTurn("s", "2026-09-01T00:00:00Z", quietTurn),
			toolCallEvent("s", "", "Bash"),
		}, score("Bash")},
		{"steer invocation closes the window", []transcript.Event{
			toolCallEvent("s", "", "Edit"), humanTurn("s", "2026-09-01T00:00:00Z", "/steer add a rule"),
			toolCallEvent("s", "", "Bash"),
		}, score("Bash")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := scanEvents(t, append(tc.events, humanTurn("s", "2026-09-01T00:09:00Z", text)))
			for _, c := range d.Signals.CorrectionCandidates {
				if c.Pattern == text {
					if got := *c.Occurrences[0].Score; got != tc.want {
						t.Errorf("score = %v, want %v", got, tc.want)
					}
					return
				}
			}
			t.Fatalf("no candidate for %q: %v", text, candidateTexts(d))
		})
	}
	if roundScore(-0.0001) != 0 || math.Signbit(roundScore(-0.0001)) {
		t.Errorf("roundScore(-0.0001) = %v, want +0", roundScore(-0.0001))
	}
}

// The list moves no reading a snapshot or a fix score uses: the instrument keeps
// its value and the classifier gives a candidate turn no signal.
//
// pins existing behaviour
func TestCandidatesLeaveInstrumentAndClassifierAlone(t *testing.T) {
	if got := ParserInstrument(); got != "scan-4284cbbd25fd" {
		t.Errorf("ParserInstrument = %s, want scan-4284cbbd25fd", got)
	}
	c := NewClassifier()
	for _, e := range []transcript.Event{
		toolCallEvent("s", "", "Bash"),
		humanTurn("s", "2026-09-01T00:00:00Z", flaggedTurn),
	} {
		if sig, ok := c.Classify(e); ok {
			t.Errorf("classifier signal on %q: %+v", e.Text, sig)
		}
	}
}
