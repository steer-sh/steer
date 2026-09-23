package firingaudit

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/checks"
	"github.com/steer-sh/steer-cli/internal/transcript"
)

func readableCase(t *testing.T, harnessID, turnKey string) CheckCase {
	t.Helper()
	run := blockRun("/r", "1a", 1)
	run.Harness = harnessID
	run.BlockReason = "check.sh failed (exit 1).\n\n" + checks.RunIDLine("1a")
	path := filepath.Join(t.TempDir(), "t.jsonl")
	var body string
	if harnessID == "claude-code" {
		body = fmt.Sprintf(`{"uuid":"fb","parentUuid":"","type":"user","isMeta":true,"promptId":"p1","message":{"content":%q}}`+"\n",
			"Stop hook feedback:\n"+run.BlockReason)
	} else {
		body = fmt.Sprintf(`{"ordinal":4,"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":%q}],"internal_chat_message_metadata_passthrough":{"turn_id":"p1"}}}`+"\n",
			"<hook_prompt hook_run_id=\"stop:0:h\">"+run.BlockReason+"</hook_prompt>")
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	run.Locators = map[string]string{"transcript_path": path, turnKey: "p1"}
	return CheckCase{Run: run}
}

// Each harness's reader finds the block by the run's own line and the turn key
// its Stop payload carried.
func TestCheckCaseReadingDispatchesToTheHarnessThatBlocked(t *testing.T) {
	for harnessID, key := range map[string]string{"claude-code": "prompt_id", "codex": "turn_id"} {
		c := readableCase(t, harnessID, key)
		if r := c.Reach(); r.Granularity != transcript.JoinEntry || r.TurnID != "p1" {
			t.Errorf("%s reach = %+v; want the entry join in turn p1", harnessID, r)
		}
		got := c.Reading(transcript.ContinuationBudget{})
		if got.Granularity != transcript.JoinEntry || len(got.Steps) != 1 ||
			got.Steps[0].Kind != transcript.StepHookFeedback {
			t.Errorf("%s reading = %s %+v (%s); want the feedback step", harnessID, got.Granularity, got.Steps, got.Reason)
		}
	}
}

// What a case lacks decides why it reaches nothing, before a transcript opens.
func TestCheckCaseReachNamesWhatIsMissing(t *testing.T) {
	legacy := readableCase(t, "codex", "turn_id")
	legacy.Run.BlockReason = ""
	noPath := readableCase(t, "codex", "turn_id")
	delete(noPath.Run.Locators, "transcript_path")
	gone := readableCase(t, "codex", "turn_id")
	gone.Run.Locators["transcript_path"] = filepath.Join(t.TempDir(), "absent.jsonl")
	pi := readableCase(t, "pi", "turn_id")
	for name, tc := range map[string]struct {
		c    CheckCase
		want string
	}{
		"a run without its line": {legacy, "no block reason"},
		"no transcript path":     {noPath, "no transcript path"},
		"a transcript gone":      {gone, "is gone"},
		"a harness with no Stop": {pi, "no Stop block reader"},
	} {
		r := tc.c.Reach()
		if r.Granularity != transcript.JoinUnavailable || !strings.Contains(r.Reason, tc.want) {
			t.Errorf("%s: reach %s (%q); want unavailable naming %q", name, r.Granularity, r.Reason, tc.want)
		}
		if got := tc.c.Reading(transcript.ContinuationBudget{}); got.Reached() || got.Reason != r.Reason {
			t.Errorf("%s: reading %s (%q); want the reach's reason", name, got.Granularity, got.Reason)
		}
	}
}
