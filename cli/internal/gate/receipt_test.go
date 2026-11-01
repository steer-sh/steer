package gate

import (
	"strings"
	"testing"
)

// A placeholder subject tells the reviewer to judge the diff alone; the operator's
// receipt names the repository without it.
func TestReceiptLeavesOutPlaceholderSubjects(t *testing.T) {
	for _, message := range []string{SubjectUnavailable, SubjectNotRecoverable} {
		req := Request{Repo: "/work/repo", Message: message}
		if got := CommitName(req); got != "repo" {
			t.Errorf("CommitName(%q) = %q, want %q", message, got, "repo")
		}
		if got := CommitIdentity(req, "", 1); got != "repo (firing 1)" {
			t.Errorf("CommitIdentity(%q) = %q, want %q", message, got, "repo (firing 1)")
		}
	}
	req := Request{Repo: "/work/repo", Message: "add mod\n\nbody"}
	if got := CommitName(req); got != `repo "add mod"` {
		t.Errorf("CommitName = %q, want the real subject quoted", got)
	}
}

// Every let-through receipt marks itself, so a one-stream channel can print it once.
func TestProceedMarksTheReceipt(t *testing.T) {
	d := proceed(Allow, Request{Repo: "/work/repo", Message: "add mod"}, "", 1, "commit proceeds.", nil)
	if !d.Receipt {
		t.Errorf("proceed left Receipt false")
	}
	if !strings.HasSuffix(d.Reason, "commit proceeds.") || !strings.HasSuffix(d.SystemMessage, "commit proceeds.") {
		t.Errorf("Reason %q and SystemMessage %q must carry the same sentence", d.Reason, d.SystemMessage)
	}
}
