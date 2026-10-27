package gate

import (
	"context"
	"os"
	"path/filepath"
	"time"
)

// trialFile is the file the sample diff adds. It is written into the trial's working
// directory, so a reviewer that reads files finds the code it was shown.
const trialFile = "stats.py"

const trialSource = `def mean(values):
    """Return the arithmetic mean of a non-empty list."""
    total = 0
    for v in values:
        total += v
    return total / (len(values) - 1)
`

const trialDiff = `diff --git a/stats.py b/stats.py
new file mode 100644
--- /dev/null
+++ b/stats.py
@@ -0,0 +1,6 @@
+def mean(values):
+    """Return the arithmetic mean of a non-empty list."""
+    total = 0
+    for v in values:
+        total += v
+    return total / (len(values) - 1)
`

const trialMessage = "Add a mean helper"

// TrialResult is one reviewer run over the sample diff.
type TrialResult struct {
	Run      ReviewRun
	Findings int
	Elapsed  time.Duration
}

// Answered reports whether the reviewer returned a review in the protocol the gate
// parses: findings, or the clean reply.
func (r TrialResult) Answered() bool { return r.Run.Text != "" }

// TrialReview runs the configured reviewer once over a fixed sample diff, through the
// same prompt and the same outcome classification as a commit review. It writes no
// ledger row and no chain state. It proves that the reviewer runs and answers here,
// not that a harness delivers the commit hook.
func TrialReview(ctx context.Context, cfg Config, reviewer Reviewer) (TrialResult, error) {
	dir, err := os.MkdirTemp("", "steer-reviewer-test-")
	if err != nil {
		return TrialResult{}, err
	}
	defer os.RemoveAll(dir)
	if err := os.WriteFile(filepath.Join(dir, trialFile), []byte(trialSource), 0o644); err != nil {
		return TrialResult{}, err
	}
	prompt := BuildReviewPrompt(NeutralizeTags(trialMessage), NeutralizeTags(trialDiff), "", Addendum{})
	review := reviewer.Review
	if vr, ok := wantsView(reviewer); ok {
		// A reviewer that reads files reads the sample through a view, as a commit
		// review would: the directory becomes a repository with the file staged.
		var view *CommitView
		_, initOK := Git(dir, gitMedium, "init", "-q", ".")
		_, addOK := Git(dir, gitMedium, "add", trialFile)
		if initOK && addOK {
			view, _ = NewCommitView(cfg, dir, DiffSpec{StagedOnly: true})
		}
		review = func(ctx context.Context, prompt, cwd string) RawRun {
			return vr.ReviewView(ctx, prompt, cwd, view)
		}
	}
	runCtx, cancel := context.WithTimeout(ctx, cfg.ReviewTimeout)
	defer cancel()
	start := time.Now()
	run := Classify(review(runCtx, prompt, dir))
	return TrialResult{Run: run, Findings: CountFindings(run.Text), Elapsed: time.Since(start)}, nil
}
