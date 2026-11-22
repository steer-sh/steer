package pi

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/transcript"
)

// TestStopBlockReadsARecordedCheckRepair checks the reading of a session Pi
// recorded: the context is the turn that created bad.txt, and the continuation
// starts at the block and holds the repair.
func TestStopBlockReadsARecordedCheckRepair(t *testing.T) {
	path := filepath.Join("testdata", "stopblock", "check-repair.jsonl")
	got, err := StopBlock(path, "steer check run: 85359a47189bde26", transcript.ContinuationBudget{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Granularity != transcript.JoinEntry || got.Anchors.DenialEntry != "610e3d2e" || got.Matches != 1 {
		t.Fatalf("reading = %+v", got.Continuation)
	}
	if len(got.Context) == 0 || got.Context[0].Kind != transcript.StepHumanTurn ||
		!strings.Contains(got.Context[0].Text, "bad.txt") {
		t.Fatalf("context = %+v", got.Context)
	}
	if len(got.Steps) < 3 || got.Steps[0].Kind != transcript.StepHookFeedback ||
		got.Steps[1].Kind != transcript.StepToolCall || !strings.Contains(got.Steps[1].Target, "rm bad.txt") {
		t.Fatalf("steps = %+v", got.Steps)
	}
	if got.EndReason != transcript.EndTranscript {
		t.Fatalf("end = %q", got.EndReason)
	}
}

// TestStopBlockFollowsTheActiveBranchAndStopsAtThePerson checks that an
// abandoned child of the block is skipped, and that a person's message ends
// the continuation.
func TestStopBlockFollowsTheActiveBranchAndStopsAtThePerson(t *testing.T) {
	body := `{"type":"session","version":3,"id":"s","cwd":"/r"}
{"type":"message","id":"u1","parentId":null,"message":{"role":"user","content":"make it"}}
{"type":"message","id":"a1","parentId":"u1","message":{"role":"assistant","content":[{"type":"text","text":"made"}]}}
{"type":"custom_message","id":"b1","parentId":"a1","customType":"steer-check","content":"failed\nsteer check run: r1","display":true}
{"type":"message","id":"x1","parentId":"b1","message":{"role":"assistant","content":[{"type":"text","text":"abandoned"}]}}
{"type":"message","id":"a2","parentId":"b1","message":{"role":"assistant","content":[{"type":"text","text":"repaired"}]}}
{"type":"message","id":"u2","parentId":"a2","message":{"role":"user","content":"thanks"}}
{"type":"message","id":"a3","parentId":"u2","message":{"role":"assistant","content":[{"type":"text","text":"later"}]}}
`
	path := filepath.Join(t.TempDir(), "s.jsonl")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := StopBlock(path, "steer check run: r1", transcript.ContinuationBudget{})
	if err != nil {
		t.Fatal(err)
	}
	var texts []string
	for _, s := range got.Steps {
		texts = append(texts, s.Text)
	}
	if strings.Join(texts, "|") != "failed\nsteer check run: r1|repaired" || got.EndReason != transcript.EndHumanTurn {
		t.Fatalf("steps = %q, end = %q", texts, got.EndReason)
	}
	if got.BranchPoints != 1 {
		t.Errorf("BranchPoints = %d; want the abandoned child of the block counted", got.BranchPoints)
	}
	if missing, _ := StopBlock(path, "steer check run: other", transcript.ContinuationBudget{}); missing.Granularity != transcript.JoinUnavailable {
		t.Fatalf("a line no block carries reached %q", missing.Granularity)
	}
}

// TestStopBlockOffTheActiveBranchFollowsItsLatestDescendant checks a block the
// session later rewound past: of the block's two children, the continuation
// reads only the branch to the later one, and none of the active branch.
func TestStopBlockOffTheActiveBranchFollowsItsLatestDescendant(t *testing.T) {
	body := `{"type":"session","version":3,"id":"s","cwd":"/r"}
{"type":"message","id":"u1","parentId":null,"message":{"role":"user","content":"make it"}}
{"type":"message","id":"a1","parentId":"u1","message":{"role":"assistant","content":[{"type":"text","text":"made"}]}}
{"type":"custom_message","id":"b1","parentId":"a1","customType":"steer-check","content":"failed\nsteer check run: r1","display":true}
{"type":"message","id":"x1","parentId":"b1","message":{"role":"user","content":"stop"}}
{"type":"message","id":"a2","parentId":"b1","message":{"role":"assistant","content":[{"type":"text","text":"retried"}]}}
{"type":"message","id":"a3","parentId":"a2","message":{"role":"assistant","content":[{"type":"text","text":"repaired"}]}}
{"type":"message","id":"u2","parentId":null,"message":{"role":"user","content":"start over"}}
{"type":"message","id":"a4","parentId":"u2","message":{"role":"assistant","content":[{"type":"text","text":"active"}]}}
`
	path := filepath.Join(t.TempDir(), "s.jsonl")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := StopBlock(path, "steer check run: r1", transcript.ContinuationBudget{})
	if err != nil {
		t.Fatal(err)
	}
	var texts []string
	for _, s := range got.Steps {
		texts = append(texts, s.Text)
	}
	if strings.Join(texts, "|") != "failed\nsteer check run: r1|retried|repaired" || got.EndReason != transcript.EndTranscript {
		t.Fatalf("steps = %q, end = %q", texts, got.EndReason)
	}
	if got.BranchPoints != 1 {
		t.Errorf("BranchPoints = %d; want the person's rewound child of the block counted", got.BranchPoints)
	}
}
