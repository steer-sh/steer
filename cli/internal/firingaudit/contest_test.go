package firingaudit

import (
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/adjudication"
)

const contestedID = "0123456789abcdef0123456789abcdef"

// A contest copies the identity of the row it names; the agent supplies only the
// identifier and the reason.
func TestAContestCopiesTheRowItNames(t *testing.T) {
	dir := t.TempDir()
	rows := []Row{{FiringID: contestedID, HookName: "no-bare-python", Harness: "codex",
		SessionID: "s1", ThreadID: "t1", StoreDir: "/store", Timestamp: "2026-09-21T00:00:00Z",
		HookDigest: "hd", RuleHash: "rh"}}
	c, err := Contest(rows, dir, contestedID, "python is the venv interpreter here", "/work")
	if err != nil {
		t.Fatal(err)
	}
	want := adjudication.Contest{FiringID: contestedID, Reason: "python is the venv interpreter here",
		RecordedAt: c.RecordedAt, Cwd: "/work", Matched: true, HookID: "no-bare-python",
		Harness: "codex", SessionID: "s1", ThreadID: "t1", StoreDir: "/store",
		Timestamp: "2026-09-21T00:00:00Z", HookDigest: "hd", RuleHash: "rh"}
	if c != want {
		t.Fatalf("contest = %+v\nwant %+v", c, want)
	}
	stored, err := adjudication.LoadContests(dir)
	if err != nil || len(stored) != 1 || stored[0] != want {
		t.Fatalf("stored %+v (%v), want the one contest", stored, err)
	}
}

// A firing no row names is still recorded, unmatched and with no identity, because
// its row may sit in a store the command did not read.
func TestAnUnmatchedContestIsRecordedWithoutAnIdentity(t *testing.T) {
	dir := t.TempDir()
	c, err := Contest([]Row{{FiringID: "ffffffffffffffffffffffffffffffff", HookName: "other"}},
		dir, contestedID, "reason", "/work")
	if err != nil {
		t.Fatal(err)
	}
	if c.Matched || c.HookID != "" || c.HookDigest != "" {
		t.Fatalf("an unmatched contest carries an identity: %+v", c)
	}
	stored, _ := adjudication.LoadContests(dir)
	if len(stored) != 1 || stored[0].Matched {
		t.Fatalf("stored %+v, want one unmatched contest", stored)
	}
}

func TestAContestRefusesAMalformedIdentifierOrReason(t *testing.T) {
	dir := t.TempDir()
	cases := []struct{ id, reason, want string }{
		{"not-an-id", "reason", "invalid firing identifier"},
		{strings.ToUpper(contestedID), "reason", "invalid firing identifier"},
		{contestedID, "   ", "must state why"},
		{contestedID, strings.Repeat("x", adjudication.MaxContestReason+1), "keep it under"},
	}
	for _, tc := range cases {
		if _, err := Contest(nil, dir, tc.id, tc.reason, ""); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("Contest(%q, %d-byte reason) error = %v, want %q", tc.id, len(tc.reason), err, tc.want)
		}
	}
	if stored, _ := adjudication.LoadContests(dir); len(stored) != 0 {
		t.Fatalf("a refused contest was stored: %+v", stored)
	}
}

// Repeated contests of one firing count once and keep every submission.
func TestContestedCountsDistinctFirings(t *testing.T) {
	other := "ffffffffffffffffffffffffffffffff"
	byFiring := adjudication.Contested([]adjudication.Contest{
		{FiringID: contestedID, Reason: "first"},
		{FiringID: contestedID, Reason: "second"},
		{FiringID: other, Reason: "third"},
	})
	if len(byFiring) != 2 {
		t.Fatalf("%d contested firings, want 2", len(byFiring))
	}
	if got := byFiring[contestedID]; len(got) != 2 || got[0].Reason != "first" || got[1].Reason != "second" {
		t.Fatalf("submissions for one firing = %+v, want both in order", got)
	}
}
