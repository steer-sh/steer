package gate

import (
	"strings"
	"testing"
)

// TestLargeUntrackedReviewAndClearance checks review delivery and clearance invalidation.
func TestLargeUntrackedReviewAndClearance(t *testing.T) {
	h := newHarness(t, findingReply("NO FINDINGS"))
	h.cfg.MaxDiffChars = 500
	h.repo = buildRepo(t, map[string]string{"base.py": lines(20)})
	gitCmd(t, h.repo, verb, "-qm", "base")
	body := lines(300)
	writeFileAt(t, h.repo, "new.py", body)
	h.spec = DiffSpec{IncludeUntracked: true, UntrackedPathspecs: []string{"new.py"}}
	first, ok := GetDiffSized(h.cfg, h.repo, h.spec)
	if !ok || !strings.Contains(first.Raw, "+line x\n") {
		t.Fatalf("raw addition is missing: %+v, ok=%t", first, ok)
	}
	if !strings.Contains(first.Text, "too large to inline") || !strings.Contains(first.Text, "new.py") || strings.Contains(first.Text, "@@") || len(first.Text) > h.cfg.MaxDiffChars {
		t.Fatalf("large addition's render is not a bounded disclosed fallback: %q", first.Text)
	}
	if d := h.handle(); d.Kind != Allow || h.lastRow()["phase"] != "clean" || h.reviewer.calls != 2 {
		t.Fatalf("large new file was not reviewed: decision=%+v calls=%d", d, h.reviewer.calls)
	}
	if d := h.handle(); d.Kind != ContextOnly || h.lastRow()["phase"] != "skip-cleared" || h.reviewer.calls != 2 {
		t.Fatalf("unchanged addition did not reuse clearance: decision=%+v calls=%d", d, h.reviewer.calls)
	}
	writeFileAt(t, h.repo, "new.py", strings.Replace(body, "line x", "line y", 1))
	changed, ok := GetDiffSized(h.cfg, h.repo, h.spec)
	if !ok || changed.Text != first.Text || changed.Raw == first.Raw {
		t.Fatal("equal-size edit must retain the fallback while changing raw identity")
	}
	if d := h.handle(); d.Kind != Allow || h.lastRow()["phase"] != "clean" || h.reviewer.calls != 4 {
		t.Fatalf("changed addition reused old clearance: decision=%+v calls=%d", d, h.reviewer.calls)
	}
}

// TestLargeUntrackedRetrySeesEdit checks edits confined to the dropped addition.
func TestLargeUntrackedRetrySeesEdit(t *testing.T) {
	h := newHarness(t, findingReply(oneFinding), findingReply("NO FINDINGS"))
	h.cfg.ReviewerRuns = 1
	h.cfg.MaxDiffChars = 2000
	h.repo = buildRepo(t, map[string]string{"a.py": lines(120)})
	gitCmd(t, h.repo, verb, "-qm", "base")
	writeFileAt(t, h.repo, "a.py", lines(200))
	body := lines(1000)
	writeFileAt(t, h.repo, "new.py", body)
	h.spec = DiffSpec{IncludeUntracked: true}
	rendered, ok := GetDiff(h.cfg, h.repo, h.spec)
	if !ok || !strings.Contains(rendered, "dropped 1 file") || !strings.Contains(rendered, "new.py") || strings.Contains(rendered, "diff --git a/new.py") {
		t.Fatalf("large new file must be disclosed outside the kept hunks: %q", rendered)
	}
	if d := h.handle(); d.Kind != Deny || h.reviewer.calls != 1 {
		t.Fatalf("first review=%+v calls=%d", d, h.reviewer.calls)
	}
	writeFileAt(t, h.repo, "new.py", strings.Replace(body, "line x", "line y", 1))
	if d := h.handle(); d.Kind != Allow || h.lastRow()["phase"] != "clean" || h.reviewer.calls != 2 {
		t.Fatalf("edit to dropped addition did not re-review: decision=%+v calls=%d", d, h.reviewer.calls)
	}
}
