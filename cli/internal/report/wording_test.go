package report

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/steer-sh/steer-cli/internal/harness"
	"github.com/steer-sh/steer-cli/internal/transcript"
)

func scanEvents(t *testing.T, events []transcript.Event) *Digest {
	t.Helper()
	scanner, projects := Scan([]transcript.Adapter{fakeAdapter{id: harness.Codex, events: events}},
		time.Time{}, transcript.Filter{AllProjects: true}, nil)
	return scanner.Finalize(30, projects, nil)
}

func humanTurn(session, ts, text string) transcript.Event {
	return transcript.Event{Kind: transcript.KindHumanTurn, SessionID: session, Timestamp: ts, Text: text}
}

// Only typed root-thread text counts, whole and after whitespace normalization:
// a punctuation difference is another wording, and a wording typed in one
// session is no repeat.
func TestRepeatedWordingMatchesWholeTypedText(t *testing.T) {
	const habit = "commit when complete (don't commit the plan file)"
	meta := humanTurn("s4", "2026-09-01T03:00:00Z", habit)
	meta.Meta = true
	thread := humanTurn("s5", "2026-09-01T04:00:00Z", habit)
	thread.ThreadID = "sub-1"
	d := scanEvents(t, []transcript.Event{
		humanTurn("s1", "2026-09-01T00:00:00Z", habit),
		humanTurn("s2", "2026-09-01T01:00:00Z", "commit  when\ncomplete (don't commit the plan file)"),
		humanTurn("s3", "2026-09-01T02:00:00Z", "commit when complete, don't commit the plan file"),
		meta, thread,
		humanTurn("s6", "2026-09-01T05:00:00Z", habit+"<pasted_content>a long log</pasted_content>"),
		humanTurn("s1", "2026-09-01T06:00:00Z", "run the tests"),
		humanTurn("s1", "2026-09-01T07:00:00Z", "run the tests"),
		humanTurn("s1", "2026-09-01T08:00:00Z", "[Request interrupted by user]"),
		humanTurn("s2", "2026-09-01T09:00:00Z", "[Request interrupted by user]"),
		humanTurn("s1", "2026-09-01T10:00:00Z", strings.Repeat("x", maxRepeatRunes+1)),
		humanTurn("s2", "2026-09-01T11:00:00Z", strings.Repeat("x", maxRepeatRunes+1)),
	})
	w := d.Signals.RepeatedWording
	if len(w) != 1 {
		t.Fatalf("repeated wording = %+v, want the one habit", w)
	}
	if w[0].ID != "w1" || w[0].Pattern != habit || w[0].Sessions != 3 || w[0].Count != 3 {
		t.Errorf("habit = %+v", w[0])
	}
	if len(d.Signals.UserCorrections) != 0 {
		t.Errorf("repeated wording reached the correction signal: %+v", d.Signals.UserCorrections)
	}
}

// A send group is bounded from its earliest member, and a second send to a
// session already in the group opens the next one.
func TestSendGroupsBoundFromTheEarliestMember(t *testing.T) {
	const text = "update future steps of the plan as needed"
	d := scanEvents(t, []transcript.Event{
		humanTurn("d", "2026-09-01T00:11:00Z", text),
		humanTurn("a", "2026-09-01T00:00:00Z", text),
		humanTurn("c", "2026-09-01T00:09:00Z", text),
		humanTurn("b", "2026-09-01T00:04:00Z", text),
		humanTurn("d", "2026-09-01T00:12:00Z", text),
	})
	w := d.Signals.RepeatedWording
	if len(w) != 1 {
		t.Fatalf("repeated wording = %+v", w)
	}
	var got []string
	for _, o := range w[0].Occurrences {
		got = append(got, o.Session+":"+string(rune('0'+o.Group)))
	}
	if strings.Join(got, ",") != "a:1,b:1,c:1,d:2,d:3" {
		t.Errorf("groups = %v, want a:1,b:1,c:1,d:2,d:3", got)
	}
	if w[0].SendGroups != 3 || w[0].Sessions != 4 || w[0].Count != 5 {
		t.Errorf("counts = %+v", w[0])
	}
}

// A correction carries the human text its session's thread showed before it,
// as details-only context that no key, count or match reads.
func TestCorrectionCarriesObservedHumanText(t *testing.T) {
	banner := humanTurn("s1", "2026-09-01T00:01:00Z", "session resumed")
	banner.Meta = true
	sub := humanTurn("s1", "2026-09-01T00:02:00Z", "a subagent prompt")
	sub.ThreadID = "sub-1"
	long := strings.Repeat("y", maxObservedLen+10)
	d := scanEvents(t, []transcript.Event{
		humanTurn("s1", "2026-09-01T00:00:00Z", "<command-name>/clear</command-name>"),
		humanTurn("s1", "2026-09-01T00:00:30Z", "first question"),
		banner, sub,
		humanTurn("s1", "2026-09-01T00:03:00Z", "second   message"),
		humanTurn("s1", "2026-09-01T00:03:30Z", "<local-command-stdout>Set effort level</local-command-stdout>"),
		humanTurn("s1", "2026-09-01T00:04:00Z", "[Request interrupted by user for tool use]"),
		humanTurn("s1", "2026-09-01T00:05:00Z", "don't use var"),
		humanTurn("s2", "2026-09-01T00:00:00Z", "<pasted_content>a build log</pasted_content>"),
		humanTurn("s2", "2026-09-01T00:00:10Z", "don't use var"),
		humanTurn("s4", "2026-09-01T00:00:00Z", "don't use var"),
		humanTurn("s3", "2026-09-01T00:00:00Z", long),
		humanTurn("s3", "2026-09-01T00:01:00Z", "don't use var"),
	})
	if len(d.Signals.UserCorrections) != 1 {
		t.Fatalf("corrections = %+v", d.Signals.UserCorrections)
	}
	c := d.Signals.UserCorrections[0]
	if c.Count != 4 || c.Pattern != "don t use var" {
		t.Fatalf("cluster = %+v", c)
	}
	s1, s2, s4, s3 := c.Occurrences[0], c.Occurrences[1], c.Occurrences[2], c.Occurrences[3]
	if s1.PreviousHumanText == nil || s1.PreviousHumanText.Text != "second message" || s1.PreviousHumanText.Cut {
		t.Errorf("s1 previous = %+v", s1.PreviousHumanText)
	}
	if s1.FirstObservedHumanTextInScan == nil || s1.FirstObservedHumanTextInScan.Text != "first question" {
		t.Errorf("s1 first = %+v", s1.FirstObservedHumanTextInScan)
	}
	if s2.PreviousHumanText == nil || s2.PreviousHumanText.Text != pastedPlaceholder {
		t.Errorf("s2 previous = %+v, want the paste placeholder", s2.PreviousHumanText)
	}
	if s4.PreviousHumanText != nil || s4.FirstObservedHumanTextInScan != nil {
		t.Errorf("s4 observed nothing before its correction, got %+v", s4)
	}
	if s3.PreviousHumanText == nil || !s3.PreviousHumanText.Cut || len(s3.PreviousHumanText.Text) != maxObservedLen {
		t.Errorf("s3 previous = %+v, want a cut text", s3.PreviousHumanText)
	}
	if s3.FirstObservedHumanTextInScan != nil {
		t.Errorf("s3 first repeats its previous text: %+v", s3.FirstObservedHumanTextInScan)
	}
	for _, o := range c.Occurrences {
		if o.Text != "don't use var" {
			t.Errorf("occurrence text carries context: %q", o.Text)
		}
	}

	data, err := SignalPage(d, kindCorrection, regexp.MustCompile("second message"), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	var listing Listing
	if err := json.Unmarshal(data, &listing); err != nil {
		t.Fatal(err)
	}
	if len(listing.Rows) != 0 {
		t.Errorf("--match read the context field: %+v", listing.Rows)
	}
	detail, err := DetailPage(d, []string{"c1"}, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(detail), `"previous_human_text"`) || !strings.Contains(string(detail), `"first_observed_human_text_in_scan"`) {
		t.Errorf("details lack the context fields:\n%s", detail)
	}
	index, err := IndexPage(d, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(index), "second message") {
		t.Errorf("the index carries context text:\n%s", index)
	}
}
