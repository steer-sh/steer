package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/adjudication"
	"github.com/steer-sh/steer-cli/internal/firingaudit"
	"github.com/steer-sh/steer-cli/internal/transcript"
)

func TestGroupRowsKeepsEachStoresGuardApart(t *testing.T) {
	// One hook name in two stores is two guards. Each store's master answers
	// for its own rows, so the coverage arithmetic must not mix them.
	rows := []firingaudit.Row{
		{FiringID: "a", HookName: "guard", StoreDir: "/global"},
		{FiringID: "b", HookName: "guard", StoreDir: "/project"},
		{FiringID: "c", HookName: "guard", StoreDir: "/global"},
		{FiringID: "d", HookName: "other", StoreDir: "/global"},
	}
	installed := func(r firingaudit.Row) adjudication.Identity {
		return adjudication.Identity{HookDigest: r.StoreDir, RuleHash: "rule"}
	}

	groups := groupRows(rows, installed)
	if len(groups) != 3 {
		t.Fatalf("groups = %+v; want one per store and hook", groups)
	}
	for _, g := range groups {
		if g.Identity.HookDigest != g.StoreDir {
			t.Errorf("%s/%s took the identity %q; want its own store's",
				g.StoreDir, g.HookName, g.Identity.HookDigest)
		}
		for _, row := range g.Rows {
			if row.StoreDir != g.StoreDir || row.HookName != g.HookName {
				t.Errorf("%s/%s holds a row from %s/%s",
					g.StoreDir, g.HookName, row.StoreDir, row.HookName)
			}
		}
	}
}

func TestWriteQueueSeparatesTheCountsAReadingCanMove(t *testing.T) {
	coverage := []guardCoverage{{HookName: "guard", StoreDir: "/global",
		Coverage: adjudication.Coverage{
			Rows: 51, Adjudicable: 4, Legacy: 47,
			CurrentJudged: 1, CurrentUnjudged: 2, OtherUnjudged: 1,
		}}}
	items := []firingaudit.Item{{
		FiringID: "aa", HookName: "guard", Timestamp: "2026-09-20T01:00:00Z",
		Reason: firingaudit.Unjudged, Reach: transcript.JoinCall,
	}}

	var out bytes.Buffer
	if err := writeQueue(&out, true, items, firingaudit.Counts{Queued: len(items), Listed: len(items)}, coverage); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{
		"51 rows, 3 recorded by the installed guard, 1 of those read",
		"47 rows carry no identifier",
		"1 rows an earlier implementation recorded, 0 of those read",
		"aa", firingaudit.Unjudged, "reach=" + transcript.JoinCall,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("queue output missing %q:\n%s", want, text)
		}
	}

	// An empty queue is not an empty store, and neither is silence.
	var quiet bytes.Buffer
	if err := writeQueue(&quiet, true, nil, firingaudit.Counts{}, coverage); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(quiet.String(), "Nothing is queued") {
		t.Errorf("an empty queue said nothing:\n%s", quiet.String())
	}
	var absent bytes.Buffer
	if err := writeQueue(&absent, false, nil, firingaudit.Counts{}, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(absent.String(), "No guard store") {
		t.Errorf("an unreadable store read as a quiet one:\n%s", absent.String())
	}
}

func TestWriteCaseShowsTheActionAndWhatFollowed(t *testing.T) {
	c := firingaudit.Case{
		Row: firingaudit.Row{
			FiringID: "aa", HookName: "guard", Harness: "claude-code",
			Rule: "never use bare python", HookDigest: "hook1",
		},
		Evidence: firingaudit.Evidence{Source: firingaudit.Inline,
			Bundle: []byte(`{"tool_input":{"command":"python x.py"}}`)},
		Continuation: transcript.Continuation{
			Granularity:  transcript.JoinCall,
			BranchPoints: 2,
			EndReason:    transcript.EndHumanTurn,
			Steps: []transcript.ContinuationStep{
				{Kind: transcript.StepToolCall, Tool: "Bash", Target: "python3 x.py"},
			},
		},
		Prior: []adjudication.Receipt{{Judgment: adjudication.Unresolved,
			Rationale: "the task context was missing"}},
	}

	var out bytes.Buffer
	rendered, err := renderCase(c, firingaudit.Emission{StepsAvailable: len(c.Continuation.Steps), StepsEmitted: len(c.Continuation.Steps)})
	out.Write(rendered)
	if err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{
		"never use bare python", "hook_digest=hook1",
		`"command": "python x.py"`,
		"join=" + transcript.JoinCall, "2 fork(s)", "python3 x.py",
		"ended where the person spoke next",
		adjudication.Unresolved, "the task context was missing",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("case output missing %q:\n%s", want, text)
		}
	}
	// A rule_hash the master never stamped is named, not left blank.
	if !strings.Contains(text, "rule_hash=(none)") {
		t.Errorf("an absent stamp printed as an empty column:\n%s", text)
	}
}

func TestWriteCaseCarriesTheTurnReadingsAttributionLimit(t *testing.T) {
	// A Codex block reaches its turn, and the turn holds work that preceded
	// the block. The reader is told so here or nowhere.
	c := firingaudit.Case{
		Row: firingaudit.Row{FiringID: "aa", Harness: "codex"},
		Continuation: transcript.Continuation{
			Granularity: transcript.JoinTurn,
			EndReason:   transcript.EndTurn,
			Steps: []transcript.ContinuationStep{
				{Kind: transcript.StepHumanTurn, Text: "run python x.py"},
			},
		},
	}

	var out bytes.Buffer
	rendered, err := renderCase(c, firingaudit.Emission{StepsAvailable: len(c.Continuation.Steps), StepsEmitted: len(c.Continuation.Steps)})
	out.Write(rendered)
	if err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{
		"join=" + transcript.JoinTurn,
		"the whole turn is shown",
		"position attributes nothing here to the block",
		"ended where the turn ended",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("turn-granularity output missing %q:\n%s", want, text)
		}
	}

	// A call-granularity reading makes no such claim about its steps.
	c.Continuation.Granularity = transcript.JoinCall
	var call bytes.Buffer
	rendered, err = renderCase(c, firingaudit.Emission{StepsAvailable: len(c.Continuation.Steps), StepsEmitted: len(c.Continuation.Steps)})
	call.Write(rendered)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(call.String(), "the whole turn is shown") {
		t.Errorf("a per-call reading printed the turn reading's limit:\n%s", call.String())
	}
}

func TestWriteCaseSaysWhatItCouldNotResolve(t *testing.T) {
	// No bundle and no continuation. Neither makes the block wrong, so both
	// print their own reason and the row's truncated snippet still shows.
	c := firingaudit.Case{
		Row: firingaudit.Row{FiringID: "aa", Snippet: "python x.py"},
		Evidence: firingaudit.Evidence{Source: firingaudit.Unrecorded,
			Reason: "the master that wrote this row captured no evidence"},
		Continuation: transcript.Continuation{
			Granularity: transcript.JoinUnavailable,
			Reason:      "the Codex turn-level lookup is not implemented",
		},
	}

	var out bytes.Buffer
	rendered, err := renderCase(c, firingaudit.Emission{StepsAvailable: len(c.Continuation.Steps), StepsEmitted: len(c.Continuation.Steps)})
	out.Write(rendered)
	if err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{
		firingaudit.Unrecorded, "captured no evidence",
		"snippet (truncated at write time): python x.py",
		"join=" + transcript.JoinUnavailable, "not implemented",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("unresolved case output missing %q:\n%s", want, text)
		}
	}
}
