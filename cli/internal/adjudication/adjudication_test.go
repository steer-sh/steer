package adjudication

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/steer-sh/steer-cli/internal/gate/mine"
	"github.com/steer-sh/steer-cli/internal/transcript"
)

func check(t *testing.T, what string, got, want any) {
	t.Helper()
	if got != want {
		t.Errorf("%s: got %v, want %v", what, got, want)
	}
}

// A judgment attached to nothing validates nothing, and a judgment outside the
// three readings is not one of them.
func TestAppendRefusesAReceiptItCannotAttach(t *testing.T) {
	dir := t.TempDir()
	if err := Append(dir, Receipt{Judgment: Prevented}); err == nil {
		t.Error("a receipt naming no firing was accepted")
	}
	if err := Append(dir, Receipt{FiringID: "aa", Judgment: "effective"}); err == nil {
		t.Error("a judgment outside the three readings was accepted")
	}
	if _, err := os.Stat(filepath.Join(dir, LogName)); !os.IsNotExist(err) {
		t.Error("a refused receipt still opened the stream")
	}
}

// Reading a continuation no join reached is a claim nothing supports. An
// unread continuation is ordinary and leaves benefit unresolved.
func TestAppendRefusesAnInspectionNoJoinReached(t *testing.T) {
	dir := t.TempDir()
	for _, join := range []string{"", transcript.JoinUnavailable} {
		err := Append(dir, Receipt{FiringID: "aa", Judgment: Prevented,
			ContinuationInspected: true, Join: join})
		if err == nil {
			t.Errorf("join %q: an inspection with nothing behind it was accepted", join)
		}
	}
	if err := Append(dir, Receipt{FiringID: "aa", Judgment: Prevented,
		ContinuationInspected: true, Join: transcript.JoinCall}); err != nil {
		t.Errorf("a continuation read at call granularity was refused: %v", err)
	}
	if err := Append(dir, Receipt{FiringID: "bb", Judgment: Prevented,
		Join: transcript.JoinUnavailable}); err != nil {
		t.Errorf("an unread continuation was refused: %v", err)
	}
}

// The stream is append-only, so a re-read supersedes the earlier judgment and the
// earlier line stays on disk.
func TestLatestKeepsTheLastJudgmentOfEachFiring(t *testing.T) {
	dir := t.TempDir()
	for _, r := range []Receipt{
		{FiringID: "aa", Judgment: Unresolved},
		{FiringID: "bb", Judgment: Incorrect},
		{FiringID: "aa", Judgment: Prevented},
	} {
		if err := Append(dir, r); err != nil {
			t.Fatal(err)
		}
	}
	receipts, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	check(t, "every line is kept", len(receipts), 3)
	latest := Latest(receipts)
	check(t, "the re-read supersedes the first judgment", latest["aa"].Judgment, Prevented)
	check(t, "an untouched firing keeps its judgment", latest["bb"].Judgment, Incorrect)
}

// A machine that judged nothing reads the same as one whose store is absent, and a
// line no reader can attach is dropped rather than failing the corpus.
func TestLoadFailsSoftOnAnAbsentOrDamagedStream(t *testing.T) {
	dir := t.TempDir()
	receipts, err := Load(dir)
	check(t, "an absent stream is no error", err, nil)
	check(t, "and yields no receipts", len(receipts), 0)

	body := "not json\n\n" + `{"judgment":"prevented"}` + "\n" +
		`{"firing_id":"aa","judgment":"prevented"}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, LogName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	receipts, err = Load(dir)
	check(t, "a damaged line is no error", err, nil)
	check(t, "only the attachable line is read", len(receipts), 1)
}

// A judgment describes the block it read. Applicability is the separate question of
// whether it also describes the guard installed now, and any disagreement between
// the two stamps is a changed guard.
func TestAppliesSeparatesTheJudgmentFromTheInstalledGuard(t *testing.T) {
	installed := Identity{HookDigest: "d1", RuleHash: "r1"}
	judged := Receipt{FiringID: "aa", Judgment: Prevented, HookDigest: "d1", RuleHash: "r1"}

	check(t, "the same implementation", Applies(judged, installed), Current)

	ruleChanged := judged
	ruleChanged.RuleHash = "r2"
	check(t, "the same source under a new rule", Applies(ruleChanged, installed), Historic)

	sourceChanged := judged
	sourceChanged.HookDigest = "d2"
	check(t, "a new source under the same rule", Applies(sourceChanged, installed), Historic)

	unstamped := judged
	unstamped.HookDigest = ""
	check(t, "a receipt carrying no source", Applies(unstamped, installed), Unknown)
	check(t, "an installed guard carrying no stamps",
		Applies(judged, Identity{}), Unknown)
}

// Rows nothing can attach a judgment to are not unread work, and the counts must
// account for every row exactly once.
func TestCoverSeparatesLegacyRowsFromUnjudgedOnes(t *testing.T) {
	installed := Identity{HookDigest: "d1", RuleHash: "r1"}
	firings := []mine.Firing{
		{HookID: "g", FiringID: "aa", HookDigest: "d1", RuleHash: "r1"},
		{HookID: "g", FiringID: "bb", HookDigest: "d0", RuleHash: "r0"},
		{HookID: "g", FiringID: "cc"},
		{HookID: "g"},
		{HookID: "other", FiringID: "dd"},
	}
	receipts := []Receipt{
		{FiringID: "aa", Judgment: Prevented, HookDigest: "d1", RuleHash: "r1"},
		{FiringID: "bb", Judgment: Incorrect, HookDigest: "d0", RuleHash: "r0"},
		{FiringID: "dd", Judgment: Prevented},
	}
	c := Cover("g", firings, receipts, installed)

	check(t, "another guard's rows are not counted here", c.Rows, 4)
	check(t, "a row with no identifier cannot be judged", c.Legacy, 1)
	check(t, "the rest can be", c.Adjudicable, 3)
	check(t, "a receipt names each judged row", c.Judged, 2)
	check(t, "the remainder is a coverage limit", c.Unjudged, 1)
	check(t, "a judgment of an older guard is superseded", c.Superseded, 1)
	check(t, "rows account exactly once", c.Adjudicable+c.Legacy, c.Rows)
}

// The first unread firing of the installed guard is due at once; after that the
// backlog waits for the caller's threshold.
func TestRefreshReadsOnlyTheInstalledGuardsOwnRows(t *testing.T) {
	due, reason := Refresh(Coverage{CurrentUnjudged: 1}, 0)
	check(t, "the first unread current row is due with no threshold", due, true)
	check(t, "and says why", reason,
		"the installed guard has fired and none of it is read yet")

	due, _ = Refresh(Coverage{CurrentJudged: 1, CurrentUnjudged: 4}, 5)
	check(t, "a read guard with a backlog below the threshold is not due", due, false)

	due, reason = Refresh(Coverage{CurrentJudged: 1, CurrentUnjudged: 5}, 5)
	check(t, "the backlog at the threshold is due", due, true)
	check(t, "and says why", reason,
		"unread firings of the installed guard reached the refresh threshold")

	due, _ = Refresh(Coverage{CurrentJudged: 1, CurrentUnjudged: 400}, 0)
	check(t, "no threshold never comes due on count alone", due, false)
}

// Superseded receipts and unread rows of some older implementation are reported, and
// neither is work: a receipt carries the stamps of the row it judged, so no reading
// can move them out of those counts. Each pair below differs only in whether the
// installed guard has rows of its own, which is the only thing dueness may turn on.
func TestRefreshIsNotDueOnEvidenceNoAuditCanDischarge(t *testing.T) {
	superseded := Coverage{Judged: 3, Superseded: 3}
	due, _ := Refresh(superseded, 0)
	check(t, "superseded judgments alone are not due", due, false)
	due, _ = Refresh(superseded, 1)
	check(t, "and stay not due under any threshold", due, false)

	withOwnRow := superseded
	withOwnRow.CurrentUnjudged = 1
	due, _ = Refresh(withOwnRow, 0)
	check(t, "the same evidence plus one unread current row is due", due, true)

	older := Coverage{Unjudged: 40, OtherUnjudged: 40}
	due, _ = Refresh(older, 5)
	check(t, "forty unread rows of another implementation are not due", due, false)

	own := Coverage{Unjudged: 40, CurrentUnjudged: 40}
	due, _ = Refresh(own, 5)
	check(t, "the same count recorded by the installed guard is due", due, true)

	read := Coverage{CurrentJudged: 1, Superseded: 3, CurrentUnjudged: 2}
	due, _ = Refresh(read, 5)
	check(t, "a read installed guard below the threshold is not due", due, false)
	due, _ = Refresh(read, 2)
	check(t, "and is due once its own backlog reaches it", due, true)
}

// A row classifies by the stamps it recorded, so the counts an audit can move are
// separated from the evidence it cannot.
func TestCoverSplitsTheInstalledGuardsRowsFromOlderOnes(t *testing.T) {
	installed := Identity{HookDigest: "new", RuleHash: "r2"}
	firings := []mine.Firing{
		{HookID: "g", FiringID: "a", HookDigest: "new", RuleHash: "r2"},
		{HookID: "g", FiringID: "b", HookDigest: "new", RuleHash: "r2"},
		{HookID: "g", FiringID: "c", HookDigest: "old", RuleHash: "r1"},
		{HookID: "g", FiringID: "d"},
	}
	receipts := []Receipt{
		{FiringID: "a", Judgment: Prevented, HookDigest: "new", RuleHash: "r2"},
		{FiringID: "c", Judgment: Prevented, HookDigest: "old", RuleHash: "r1"},
	}
	c := Cover("g", firings, receipts, installed)

	check(t, "the installed guard's judged rows", c.CurrentJudged, 1)
	check(t, "its unread rows", c.CurrentUnjudged, 1)
	check(t, "judged rows of another implementation", c.OtherJudged, 1)
	check(t, "unread rows of another or no implementation", c.OtherUnjudged, 1)
	check(t, "the older judged row is superseded", c.Superseded, 1)
	check(t, "and the unjudged total still counts both", c.Unjudged, 2)
	check(t, "the four counts account for every adjudicable row",
		c.CurrentJudged+c.CurrentUnjudged+c.OtherJudged+c.OtherUnjudged, c.Adjudicable)
}
