package mine

import (
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/branchreview"
)

// branchFindingsDir is findingsDir's corpus of one commit chain, plus one branch
// review at ts 5000 whose author accepted the finding text.
func branchFindingsDir(t *testing.T, records []Record, text string) string {
	t.Helper()
	dir := findingsDir(t, records, []map[string]any{c("a commit defect no class names")})
	branch := filepath.Join(dir, branchreview.Component)
	writeStream(t, filepath.Join(branch, "reviews.h1.jsonl"), branchRow(5000, "b1", branchreview.PhaseFindings))
	writeStream(t, filepath.Join(branch, "rulings.h1.jsonl"),
		receipt(5100, "b1", ruling(1, "ACCEPTED", "", "", text)))
	return dir
}

// installedAt is an active review class whose windows split at ts.
func installedAt(id, match string, ts int) Record {
	r := reviewRecord(id, match, "defect", "active")
	r["installed_ts"] = ts
	return r
}

// scopedTo gives r a stored scope over the repositories repo matches.
func scopedTo(r Record, repo string) Record {
	r["scope"] = map[string]any{"version": ScopeVersionV1, "repo": repo}
	return r
}

// pathScoped gives r a stored scope over the changed paths path matches.
func pathScoped(r Record, path string) Record {
	r["scope"] = map[string]any{"version": ScopeVersionV1, "path": path}
	return r
}

// scopedLater gives r the scope a legacy record took at ts.
func scopedLater(r Record, ts int) Record {
	r["scope_set_ts"] = ts
	return scopedTo(r, "app")
}

// badScope gives r a stored scope of a version no binary reads.
func badScope(r Record) Record {
	r["scope"] = map[string]any{"version": 99, "all": true}
	return r
}

// TestUndecidedShowsABranchFindingUnderItsChannel checks that an undecided branch
// finding reaches the dump, labelled channel=branch in text and JSON, while the
// commit chain beside it carries no channel, and that a class's incidence in the
// header is read over the commit chains alone.
func TestUndecidedShowsABranchFindingUnderItsChannel(t *testing.T) {
	dir := branchFindingsDir(t, []Record{installedAt("other", "zzz", 4000)}, "the branch leaks a handle")
	out := findingsOut(t, dir, FindingsOpts{Kind: "accepted", Undecided: true})
	if !strings.Contains(out, "#     now 0/1 chains") {
		t.Errorf("a class's incidence counts commit chains alone:\n%s", out)
	}
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, "--- ") {
			continue
		}
		if branch := strings.Contains(line, "ts=5000"); branch != strings.HasSuffix(line, "  channel=branch") {
			t.Errorf("only the branch chain's header names its channel: %q", line)
		}
	}
	if !strings.Contains(out, "  [ACCEPTED] the branch leaks a handle") {
		t.Errorf("the undecided branch finding is shown:\n%s", out)
	}

	var doc struct {
		Chains []map[string]any `json:"chains"`
	}
	if err := json.Unmarshal([]byte(findingsOut(t, dir, FindingsOpts{Kind: "accepted", Undecided: true, JSON: true})), &doc); err != nil {
		t.Fatal(err)
	}
	channels := map[string]any{}
	for _, ch := range doc.Chains {
		channels[pyStr(ch["start_ts"])] = ch["channel"]
	}
	check(t, "JSON names the channel of the branch chain alone", channels,
		map[string]any{"1000.0": nil, "5000.0": ChannelBranch})
}

// TestUndecidedBranchFindingCoverage checks the branch rule of ChainCoverage in the
// dump: an active class hides a branch finding from before its split and shows one
// after it, marked with its id, even when another class covers it, only on a chain its
// scope holds; a rejected class hides it at any time.
func TestUndecidedBranchFindingCoverage(t *testing.T) {
	rejected := reviewRecord("noise", "handle", "defect", "rejected")
	rejected["rejected_ts"] = 1
	caught := "  [ACCEPTED] [caught-after=fix] the branch leaks a handle"
	for name, tc := range map[string]struct {
		records []Record
		want    string
	}{
		"an active class installed after the review covers it":   {[]Record{installedAt("fix", "handle", 9000)}, ""},
		"an active class installed before it shows it as caught": {[]Record{installedAt("fix", "handle", 4000)}, caught},
		"a later class covering it does not hide the catch": {
			[]Record{installedAt("fix", "handle", 4000), installedAt("late", "leaks", 9000)},
			"  [ACCEPTED] [caught-after=fix] [covered-by=late] the branch leaks a handle"},
		"a class scoped to another repository covers it":        {[]Record{scopedTo(installedAt("fix", "handle", 4000), "other")}, ""},
		"a class whose scope cannot decide the chain covers it": {[]Record{pathScoped(installedAt("fix", "handle", 4000), "src/")}, ""},
		"a class whose scope does not parse covers it":          {[]Record{badScope(installedAt("fix", "handle", 4000))}, ""},
		"a class scoped after the review covers it":             {[]Record{scopedLater(installedAt("fix", "handle", 4000), 9000)}, ""},
		"a class scoped to its repository shows it as caught":   {[]Record{scopedTo(installedAt("fix", "handle", 4000), "app")}, caught},
		"a rejected class covers it":                            {[]Record{rejected}, ""},
	} {
		out := findingsOut(t, branchFindingsDir(t, tc.records, "the branch leaks a handle"),
			FindingsOpts{Kind: "accepted", Undecided: true})
		if tc.want == "" {
			if strings.Contains(out, "leaks a handle") {
				t.Errorf("%s: the covered branch chain is dropped:\n%s", name, out)
			}
			continue
		}
		if !strings.Contains(out, tc.want+"\n") || !strings.Contains(out, "# 1 branch review finding(s)") {
			t.Errorf("%s: want %q and the header line naming it:\n%s", name, tc.want, out)
		}
	}
}

// TestACaughtBranchFindingFollowsTheReadLifecycle checks that a branch finding caught
// after an install is taken as read by a mark and returns as new when its class
// retires.
func TestACaughtBranchFindingFollowsTheReadLifecycle(t *testing.T) {
	dir := branchFindingsDir(t, []Record{installedAt("fix", "handle", 4000)}, "the branch leaks a handle")
	findingsOut(t, dir, FindingsOpts{Kind: "accepted", Undecided: true})
	markRead(t, dir)
	_, readPart := sections(t, findingsOut(t, dir, FindingsOpts{Kind: "accepted", Undecided: true}))
	if !strings.Contains(readPart, "leaks a handle") {
		t.Fatalf("a mark takes the caught finding as read:\n%s", readPart)
	}
	if err := Retire(io.Discard, dir, "fix", "wrong tell", "removed"); err != nil {
		t.Fatal(err)
	}
	newPart, _ := sections(t, findingsOut(t, dir, FindingsOpts{Kind: "accepted", Undecided: true}))
	if !strings.Contains(newPart, "leaks a handle") {
		t.Errorf("retiring its class returns the branch finding as new:\n%s", newPart)
	}
}
