package report

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/steer-sh/steer-cli/internal/transcript"
)

func callEvent(session, id, tool, target string) transcript.Event {
	return transcript.Event{Kind: transcript.KindToolCall, SessionID: session, ToolUseID: id, Tool: tool, Target: target}
}

func resultEvent(session, id string, isError bool, text string) transcript.Event {
	return transcript.Event{Kind: transcript.KindToolResult, SessionID: session, ToolUseID: id, IsError: isError, Text: text}
}

func callSummary(r *RecentToolCalls) string {
	if r == nil {
		return "<none>"
	}
	var parts []string
	for _, c := range r.Calls {
		parts = append(parts, c.Tool+" "+c.Target+"="+c.Result)
	}
	return strings.Join(parts, "|") + " omitted=" + strconv.Itoa(r.Omitted)
}

// A correction carries the last calls its session and thread requested since
// the previous real human turn, with the results observed before it. Another
// thread's or session's calls, a later result, a wrapper and an interrupt marker
// change nothing; a real turn, even a paste-only one, starts a new interval.
func TestCorrectionCarriesRecentToolCalls(t *testing.T) {
	sub := callEvent("s1", "x1", "Bash", "rm -rf build")
	sub.ThreadID = "sub-1"
	long := "a" + strings.Repeat("é", 60)
	d := scanEvents(t, []transcript.Event{
		humanTurn("s1", "2026-09-01T00:00:00Z", "please fix the parser"),
		callEvent("s1", "c1", "Edit", "parser.go"),
		callEvent("s1", "c2", "Bash", "go test ./..."),
		callEvent("s1", "c3", "Bash", "git status"),
		resultEvent("s1", "c3", false, "clean"),
		resultEvent("s1", "c2", true, "FAIL parser"),
		sub,
		callEvent("s2", "o1", "Bash", "git push"),
		callEvent("s1", "c4", "Read", long),
		humanTurn("s1", "2026-09-01T00:01:00Z", "<command-name>/model</command-name>"),
		interrupt("s1", "2026-09-01T00:01:30Z", transcript.InterruptTurn),
		humanTurn("s1", "2026-09-01T00:02:00Z", "don't use var"),
		resultEvent("s1", "c4", false, "late"),
		humanTurn("s1", "2026-09-01T00:03:00Z", "why are you still here"),

		callEvent("s3", "p1", "Bash", "ls"),
		humanTurn("s3", "2026-09-01T00:00:00Z", "<pasted_content>a log</pasted_content>"),
		humanTurn("s3", "2026-09-01T00:00:10Z", "don't use var"),
	})
	var c, why *Cluster
	for _, cl := range d.Signals.UserCorrections {
		switch cl.Pattern {
		case "don t use var":
			c = cl
		case "why are you still here":
			why = cl
		}
	}
	if c == nil || why == nil || len(c.Occurrences) != 2 {
		t.Fatalf("corrections = %+v", d.Signals.UserCorrections)
	}
	got := c.Occurrences[0].RecentToolCalls
	if got == nil || len(got.Calls) != maxRecentToolCalls {
		t.Fatalf("s1 recent = %s", callSummary(got))
	}
	last := got.Calls[2]
	if s := callSummary(got); s != "Bash go test ./...=error|Bash git status=non_error|Read "+last.Target+"=unobserved omitted=1" {
		t.Errorf("s1 recent = %s", s)
	}
	if !last.Cut || len(last.Target) > maxToolTargetLen || !utf8.ValidString(last.Target) || !strings.HasPrefix(long, last.Target) {
		t.Errorf("long target = %+v, want a UTF-8-safe cut", last)
	}
	if s := callSummary(why.Occurrences[0].RecentToolCalls); s != "<none>" {
		t.Errorf("a correction right after a real turn carries %s", s)
	}
	if s := callSummary(c.Occurrences[1].RecentToolCalls); s != "<none>" {
		t.Errorf("a paste-only turn did not start a new interval: %s", s)
	}

	data, err := SignalPage(d, kindCorrection, regexp.MustCompile("git status"), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	var listing Listing
	if err := json.Unmarshal(data, &listing); err != nil {
		t.Fatal(err)
	}
	if len(listing.Rows) != 0 {
		t.Errorf("--match read the tool context: %+v", listing.Rows)
	}
	detail, err := DetailPage(d, []string{c.ID}, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(detail), `"recent_tool_calls"`) || !strings.Contains(string(detail), `"target": "git status"`) {
		t.Errorf("details lack the tool context:\n%s", detail)
	}
	index, err := IndexPage(d, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(index), "git status") {
		t.Errorf("the index carries tool context:\n%s", index)
	}
}

// An after-interrupt message carries the calls before the interrupt, a declined
// call reads as declined, and a deferred human turn carries no tool context.
func TestAfterInterruptCarriesRecentToolCalls(t *testing.T) {
	deferred := humanTurn("s2", "2026-09-01T00:00:10Z", "don't use var")
	deferred.DeferredHumanTurn = true
	d := scanEvents(t, []transcript.Event{
		humanTurn("s1", "2026-09-01T00:00:00Z", "ship it"),
		callEvent("s1", "d1", "Bash", "git push --force"),
		resultEvent("s1", "d1", true, "The user doesn't want to proceed with this tool use. The tool use was rejected."),
		interrupt("s1", "2026-09-01T00:00:30Z", transcript.InterruptTurn),
		humanTurn("s1", "2026-09-01T00:01:00Z", "what are you pushing"),

		callEvent("s2", "q1", "Bash", "ls"),
		deferred,
	})
	if len(d.Signals.AfterInterrupt) != 1 {
		t.Fatalf("after_interrupt = %+v", d.Signals.AfterInterrupt)
	}
	if s := callSummary(d.Signals.AfterInterrupt[0].Occurrences[0].RecentToolCalls); s != "Bash git push --force=declined omitted=0" {
		t.Errorf("after_interrupt recent = %s", s)
	}
	if len(d.Signals.UserCorrections) != 1 {
		t.Fatalf("corrections = %+v", d.Signals.UserCorrections)
	}
	if s := callSummary(d.Signals.UserCorrections[0].Occurrences[0].RecentToolCalls); s != "<none>" {
		t.Errorf("a deferred turn carries %s", s)
	}
}
