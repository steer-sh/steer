package gate

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// trialRecorder answers one scripted reply and records what the trial handed it.
type trialRecorder struct {
	reply     RawRun
	prompt    string
	sawSource bool
}

func (r *trialRecorder) Name() string       { return "recorder" }
func (r *trialRecorder) ModelLabel() string { return "recorder-model" }

func (r *trialRecorder) Review(_ context.Context, prompt, cwd string) RawRun {
	r.prompt = prompt
	body, err := os.ReadFile(filepath.Join(cwd, trialFile))
	r.sawSource = err == nil && string(body) == trialSource
	return r.reply
}

// TestTrialReviewAnswersThroughTheCommitPrompt: the trial sends the commit review's own
// prompt over the sample diff, beside the file it adds, and counts a protocol reply as
// an answer and anything else as none. It writes nothing to the ledger.
func TestTrialReviewAnswersThroughTheCommitPrompt(t *testing.T) {
	cfg := Defaults()
	cfg.StateDir = t.TempDir()

	answered := &trialRecorder{reply: findingReply(oneFinding)}
	got, err := TrialReview(context.Background(), cfg, answered)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Answered() || got.Findings != 1 {
		t.Errorf("result = %+v; want an answer with one finding", got)
	}
	if !strings.Contains(answered.prompt, "red-teaming a git commit") ||
		!strings.Contains(answered.prompt, "+++ b/stats.py") {
		t.Errorf("prompt is not the commit review prompt over the sample diff:\n%s", answered.prompt)
	}
	if !answered.sawSource {
		t.Error("the reviewer's working directory did not hold the file the diff adds")
	}

	refused := &trialRecorder{reply: RawRun{Response: "I cannot run tools here.", Status: "SUCCESS"}}
	got, err = TrialReview(context.Background(), cfg, refused)
	if err != nil {
		t.Fatal(err)
	}
	if got.Answered() || got.Run.Outcome != OutcomeLost {
		t.Errorf("result = %+v; a reply outside the protocol is no answer", got)
	}

	if entries, _ := os.ReadDir(cfg.StateDir); len(entries) != 0 {
		t.Errorf("the trial wrote state: %v", entries)
	}
}
