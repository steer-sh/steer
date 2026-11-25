package pi

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/transcript"
)

// blockedSession is a Pi session in which a guard blocked call c1, the
// assistant took two paths after the denial r1, and the person spoke on the
// later one. Lines after it are the active branch.
const blockedSession = `{"type":"session","version":3,"id":"s","cwd":"/r"}
{"type":"message","id":"u1","parentId":null,"message":{"role":"user","content":"run it"}}
{"type":"message","id":"a1","parentId":"u1","message":{"role":"assistant","content":[{"type":"toolCall","id":"c1","name":"bash","arguments":{"command":"python x.py"}}]}}
{"type":"message","id":"r1","parentId":"a1","message":{"role":"toolResult","toolCallId":"c1","toolName":"bash","content":[{"type":"text","text":"blocked: use python3"}],"isError":true}}
{"type":"message","id":"x1","parentId":"r1","message":{"role":"assistant","content":[{"type":"text","text":"abandoned"}]}}
{"type":"message","id":"a2","parentId":"r1","message":{"role":"assistant","content":[{"type":"toolCall","id":"c2","name":"bash","arguments":{"command":"python3 x.py"}}]}}
{"type":"message","id":"r2","parentId":"a2","message":{"role":"toolResult","toolCallId":"c2","toolName":"bash","content":[{"type":"text","text":"ok"}],"isError":false}}
{"type":"message","id":"u2","parentId":"r2","message":{"role":"user","content":"thanks"}}
{"type":"message","id":"a3","parentId":"u2","message":{"role":"assistant","content":[{"type":"text","text":"later"}]}}
`

func writeSession(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "s.jsonl")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// stepTexts renders each step as its kind and its text or target.
func stepTexts(steps []transcript.ContinuationStep) string {
	var out []string
	for _, s := range steps {
		text := s.Text
		if s.Kind == transcript.StepToolCall {
			text = s.Target
		}
		out = append(out, string(s.Kind)+":"+text)
	}
	return strings.Join(out, "|")
}

// TestContinuationReadsFromTheDenialAlongOneBranch checks the walk from the
// denial: what it reads, where it stops, and the branches it counts unread.
func TestContinuationReadsFromTheDenialAlongOneBranch(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		budget   transcript.ContinuationBudget
		steps    string
		end      string
		branches int
	}{
		{
			name: "the active branch, to the person",
			body: blockedSession,
			steps: "tool_result:blocked: use python3|tool_call:python3 x.py|tool_result:ok|" +
				"human_turn:thanks",
			end:      transcript.EndHumanTurn,
			branches: 1,
		},
		{
			name:     "a spent budget",
			body:     blockedSession,
			budget:   transcript.ContinuationBudget{Steps: 2},
			steps:    "tool_result:blocked: use python3|tool_call:python3 x.py",
			end:      transcript.EndBudget,
			branches: 1,
		},
		{
			name: "two abandoned children of one entry, one fork",
			body: strings.Replace(blockedSession, `"abandoned"}]}}`+"\n", `"abandoned"}]}}`+"\n"+
				`{"type":"message","id":"x2","parentId":"r1","message":{"role":"assistant","content":[{"type":"text","text":"also abandoned"}]}}`+"\n", 1),
			steps: "tool_result:blocked: use python3|tool_call:python3 x.py|tool_result:ok|" +
				"human_turn:thanks",
			end:      transcript.EndHumanTurn,
			branches: 1,
		},
		{
			name: "a fork after the person spoke, not passed",
			body: blockedSession +
				`{"type":"message","id":"a4","parentId":"u2","message":{"role":"assistant","content":[{"type":"text","text":"retried"}]}}` + "\n",
			steps: "tool_result:blocked: use python3|tool_call:python3 x.py|tool_result:ok|" +
				"human_turn:thanks",
			end:      transcript.EndHumanTurn,
			branches: 1,
		},
		{
			name: "a denial the session rewound past",
			body: blockedSession +
				`{"type":"message","id":"u3","parentId":null,"message":{"role":"user","content":"start over"}}` + "\n",
			steps: "tool_result:blocked: use python3|tool_call:python3 x.py|tool_result:ok|" +
				"human_turn:thanks",
			end:      transcript.EndHumanTurn,
			branches: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Continuation(writeSession(t, tc.body), "c1", tc.budget)
			if err != nil {
				t.Fatal(err)
			}
			if got.Granularity != transcript.JoinCall ||
				got.Anchors.BlockedCallEntry != "a1" || got.Anchors.DenialEntry != "r1" {
				t.Fatalf("reading = %+v", got)
			}
			if s := stepTexts(got.Steps); s != tc.steps {
				t.Errorf("steps = %q; want %q", s, tc.steps)
			}
			if got.EndReason != tc.end || got.BranchPoints != tc.branches {
				t.Errorf("end = %q, branches = %d; want %q, %d",
					got.EndReason, got.BranchPoints, tc.end, tc.branches)
			}
		})
	}
}

// TestContinuationWithoutTheCallReachesNothingAndNamesWhy checks a call the
// transcript holds no result for, including a nested call Pi never records.
func TestContinuationWithoutTheCallReachesNothingAndNamesWhy(t *testing.T) {
	path := writeSession(t, blockedSession)
	for id, want := range map[string]string{
		"":     "no call identifier",
		"c9":   "no result for call c9",
		"c1/1": "made inside call c1",
	} {
		got, err := Continuation(path, id, transcript.ContinuationBudget{})
		if err != nil {
			t.Fatal(err)
		}
		if got.Reached() || !strings.Contains(got.Reason, want) {
			t.Errorf("call %q = %q (%s); want unavailable naming %q", id, got.Granularity, got.Reason, want)
		}
	}
	if _, err := Continuation(filepath.Join(t.TempDir(), "absent.jsonl"), "c1", transcript.ContinuationBudget{}); err == nil {
		t.Error("an unreadable transcript read without error")
	}
}
