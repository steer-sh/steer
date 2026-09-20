package adjudication

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/steer-sh/steer-cli/internal/gate/mine"
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

// A changed guard invalidates the judgments already made, so it is due whatever the
// unjudged count is. An unjudged count alone waits for the caller's threshold.
func TestRefreshTreatsAChangedGuardAndUnreadRowsSeparately(t *testing.T) {
	due, reason := Refresh(Coverage{Judged: 3, Superseded: 1}, 0)
	check(t, "a changed guard is due with no unjudged rows", due, true)
	check(t, "and says why", reason, "the guard changed since it was judged")

	due, _ = Refresh(Coverage{Unjudged: 4}, 5)
	check(t, "unjudged rows below the threshold are not due", due, false)

	due, reason = Refresh(Coverage{Unjudged: 5}, 5)
	check(t, "unjudged rows at the threshold are due", due, true)
	check(t, "and say why", reason, "unjudged firings reached the refresh threshold")

	due, _ = Refresh(Coverage{Unjudged: 400}, 0)
	check(t, "no threshold never comes due on count alone", due, false)
}
