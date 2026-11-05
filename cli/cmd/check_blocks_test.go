package cmd

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/steer-sh/steer-cli/internal/checks"
)

// A block renders where its reason landed and each follow-up fact, and says
// so when a fact is missing rather than leaving a line out.
func TestCheckBlocksNamesEachFollowupFact(t *testing.T) {
	now := time.Now()
	block := checks.OracleRun{RunID: "r1", Source: checks.SourceStop, Harness: "claude-code", Session: "s1", Started: now,
		Locators: map[string]string{"transcript_path": "/t/s1.jsonl", "prompt_id": "p-1"}}
	next := checks.OracleRun{RunID: "r2", Source: checks.SourceStop, Harness: "claude-code", Session: "s1",
		Outcome: checks.OutcomePassed, Disposition: checks.DispositionAllow, Started: now}
	failed := checks.Execution{CheckID: "a", Outcome: checks.OutcomeFailed}
	pass := checks.Execution{CheckID: "a", Outcome: checks.OutcomePassed, Started: now}
	var out bytes.Buffer
	writeBlockFollowups(&out, []checks.BlockFollowup{
		{Block: block, NextStop: &next, SessionTree: checks.FollowChanged, NextRun: &next,
			Checks: []checks.CheckFollowup{{Blocked: failed, Next: &pass, NextPass: &pass, Detector: checks.FollowChanged}}},
		{Block: checks.OracleRun{RunID: "r0", Harness: "codex"}, SessionTree: checks.FollowUnknown,
			Checks: []checks.CheckFollowup{{Blocked: failed, Detector: checks.FollowUnknown}}},
	})
	text := out.String()
	for _, want := range []string{
		"run r1 blocked a claude-code turn",
		"transcript: /t/s1.jsonl, turn p-1",
		"next Stop in this session: allow at", "the tree changed",
		"next check.sh run: passed at", "(stop, claude-code, session s1)",
		"a: failed at the block; next passed at", "the detector changed before the recorded pass",
		"transcript: not recorded",
		"next Stop in this session: none recorded",
		"next check.sh run: none recorded",
		"a: failed at the block; no later result recorded",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("output lacks %q:\n%s", want, text)
		}
	}
}
