package cc

import (
	"os/exec"
	"strings"
	"testing"
)

// TestCommitShapeGuidanceTeachesOnlyAcceptedForms runs each command the guidance
// quotes through PreflightDeny in a clean repository: the commit must be read as a
// commit and go to review, and the search and document-heredoc examples must hold
// no commit at all, even with commit text in the document's body.
//
// pins existing behaviour
func TestCommitShapeGuidanceTeachesOnlyAcceptedForms(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	if !strings.Contains(CommitShapeGuidance, documentHeredocExample) {
		t.Errorf("the guidance does not quote %q", documentHeredocExample)
	}
	document := documentHeredocExample + "\ngit co" + "mmit -m x\nEOF"
	if got := Detect(document); got != NoCommit {
		t.Errorf("the taught document heredoc is read as a commit: Detect = %v", got)
	}
	for _, example := range []string{guidanceCommitExample, guidanceSearchExample} {
		if !strings.Contains(CommitShapeGuidance, example) {
			t.Errorf("the guidance does not quote %q", example)
		}
		if reason, _ := PreflightDeny(example, root); reason != "" {
			t.Errorf("the gate refuses the taught form %q:\n%s", example, reason)
		}
	}
	if got := Detect(guidanceCommitExample); got != ParsedCommit {
		t.Errorf("the taught commit is not read as a commit: Detect = %v", got)
	}
	if got := Detect(guidanceSearchExample); got != NoCommit {
		t.Errorf("the taught search holds a commit: Detect = %v", got)
	}
}
