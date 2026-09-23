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

// A guard's counts are of distinct firings: two contests of one firing count
// once, and contested and unread are separate counts a firing can sit in both.
func TestContestsCountDistinctFiringsBesideUnread(t *testing.T) {
	judged := "11111111111111111111111111111111"
	quiet := "22222222222222222222222222222222"
	rows := []Row{
		{FiringID: contestedID, HookName: "g", StoreDir: "/s", Timestamp: "t1", ContestProtocol: 1},
		{FiringID: judged, HookName: "g", StoreDir: "/s", Timestamp: "t2", ContestProtocol: 1},
		{FiringID: quiet, HookName: "g", StoreDir: "/s", Timestamp: "t3"},
		{HookName: "g", StoreDir: "/s", Timestamp: "t0"},
	}
	contests := []adjudication.Contest{
		{FiringID: contestedID, Reason: "first"},
		{FiringID: contestedID, Reason: "second"},
		{FiringID: judged, Reason: "third"},
	}
	receipts := []adjudication.Receipt{{Subject: adjudication.SubjectGuard, FiringID: judged, Judgment: adjudication.Incorrect}}
	report := Contests(rows, contests, receipts, nil, nil, "")
	if len(report.Guards) != 1 || len(report.Unmatched) != 0 {
		t.Fatalf("report = %+v, want one guard and nothing unmatched", report)
	}
	g := report.Guards[0]
	if g.Firings != 4 || g.NotOffered != 2 || g.Contested != 2 || g.Unread != 2 {
		t.Fatalf("counts = firings %d, not offered %d, contested %d, unread %d; want 4, 2, 2, 2",
			g.Firings, g.NotOffered, g.Contested, g.Unread)
	}
	first := g.ContestedFirings[0]
	if first.FiringID != contestedID || len(first.Reasons) != 2 || first.Judgment != "" {
		t.Fatalf("first contested firing = %+v, want both reasons and no judgment", first)
	}
	if second := g.ContestedFirings[1]; second.Judgment != adjudication.Incorrect {
		t.Fatalf("second contested firing = %+v, want its receipt's judgment", second)
	}
}

// A contest of an excluded row is disclosed apart from every other count, and a
// contest no loaded row carries is listed unmatched under the guard it recorded.
func TestContestsDiscloseExcludedAndUnmatchedFirings(t *testing.T) {
	orphan := "33333333333333333333333333333333"
	rows := []Row{{FiringID: contestedID, HookName: "g", StoreDir: "/s", RowDigest: "d1", ContestProtocol: 1}}
	contests := []adjudication.Contest{
		{FiringID: contestedID, Reason: "test run"},
		{FiringID: orphan, Reason: "elsewhere", HookID: "g", Matched: true},
		{FiringID: "44444444444444444444444444444444", Reason: "other guard", HookID: "h"},
	}
	excluded := map[string]adjudication.Exclusion{"d1": {RowDigest: "d1"}}
	report := Contests(rows, contests, nil, excluded, nil, "g")
	g := report.Guards[0]
	if g.Firings != 0 || g.Contested != 0 || g.ContestedExcluded != 1 {
		t.Fatalf("guard = %+v, want the excluded contest counted only as excluded", g)
	}
	if len(report.Unmatched) != 1 || report.Unmatched[0].FiringID != orphan ||
		report.Unmatched[0].HookName != "g" {
		t.Fatalf("unmatched = %+v, want only the orphan of guard g", report.Unmatched)
	}
}
