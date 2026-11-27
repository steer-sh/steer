package mine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/steer-sh/steer-cli/internal/branchreview"
	"github.com/steer-sh/steer-cli/internal/gate"
	"github.com/steer-sh/steer-cli/internal/steerpath"
	"github.com/steer-sh/steer-cli/internal/testhome"
)

// writeStream writes rows as one JSONL file at path.
func writeStream(t *testing.T, path string, rows ...any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	var data []byte
	for _, r := range rows {
		line, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		data = append(append(data, line...), '\n')
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// branchRow is a review row as branchreview writes it, with its parsed findings.
func branchRow(ts int, id, phase string, findings ...branchreview.Finding) gate.Row {
	return gate.Row{"ts": ts, "review_id": id, "phase": phase, "host": "h1", "channel": gate.ChannelBranch,
		"repo": "/r/app", "files": 2, "findings_unparsed": 0, "findings": findings}
}

func ruling(index int, verdict, qualifier, file, claim string) branchreview.Ruling {
	return branchreview.Ruling{RulingInput: branchreview.RulingInput{Index: index, Verdict: verdict,
		Qualifier: qualifier, Resolution: "done"}, File: file, Claim: claim}
}

func receipt(ts int64, id string, rulings ...branchreview.Ruling) branchreview.Receipt {
	return branchreview.Receipt{TS: ts, ReviewID: id, Host: "h1", Harness: "codex", Rulings: rulings}
}

// branchCorpus seeds a commit-review ledger and a branch-review store side by side
// under one root and returns the commit-review state directory.
func branchCorpus(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	stateDir := filepath.Join(root, "commit-review")
	writeStream(t, filepath.Join(stateDir, "adjudication.h1.jsonl"),
		host(row(1000, "deny"), "h1"),
		host(concerns(row(1010, "retry"), "round", c("commit concern")), "h1"))
	branch := filepath.Join(root, "branch-review")
	unparsed := branchRow(1030, "unparsed", branchreview.PhaseFindings)
	unparsed["findings_unparsed"] = 1
	open := branchRow(5000, "open", branchreview.PhaseFindings)
	open["review_elapsed_ms"] = 100_000
	writeStream(t, filepath.Join(branch, "reviews.h1.jsonl"),
		branchRow(1005, "clean1", branchreview.PhaseClean),
		branchRow(1020, "ruled", branchreview.PhaseFindings),
		branchRow(1025, "twin", branchreview.PhaseFindings),
		unparsed,
		open,
		gate.Row{"ts": 1040, "phase": branchreview.PhaseClean, "host": "h1"})
	writeStream(t, filepath.Join(branch, "rulings.h1.jsonl"),
		receipt(2000, "ruled",
			ruling(1, "ACCEPTED", "", "a.go", "nil map"),
			ruling(2, "ACCEPTED", "not fixed", "b.go", "slow loop"),
			ruling(3, "REJECTED", "", "", "no claim file"),
			ruling(4, "ACCEPTED", "", "a.go", "nil map")),
		receipt(1500, "ruled", ruling(1, "REJECTED", "", "a.go", "stale ruling")),
		receipt(2000, "twin", ruling(1, "ACCEPTED", "", "a.go", "nil map")))
	return stateDir
}

func chainByID(t *testing.T, chains []Chain, id string) Chain {
	t.Helper()
	for _, ch := range chains {
		if str(ch.Rows[0]["chain_id"]) == id {
			return ch
		}
	}
	t.Fatalf("no chain %s", id)
	return Chain{}
}

// TestLoadReviewRowsReadsBranchReviewsAsChains checks that each branch review becomes
// its own chain carrying the newest receipt's rulings, that it leaves the commit chain
// it interleaves with whole, and that two reviews ruled in one second keep their findings.
func TestLoadReviewRowsReadsBranchReviewsAsChains(t *testing.T) {
	rows, err := LoadReviewRows(branchCorpus(t), "")
	if err != nil {
		t.Fatal(err)
	}
	chains := BuildChains(rows, false)
	check(t, "one commit chain and five branch chains", len(chains), 6)

	commit := chains[0]
	check(t, "the commit chain keeps both rows", len(commit.Rows), 2)
	check(t, "the commit chain is the commit channel", commit.Channel, ChannelCommit)

	clean := chainByID(t, chains, "branch:clean1")
	check(t, "a branch review is the branch channel", clean.Channel, ChannelBranch)
	check(t, "a clean review ends clean", *clean.Terminal, branchreview.PhaseClean)
	check(t, "a clean review owes nothing", ChainEvidence(clean, 9_000_000), EvidenceComplete)

	ruled := chainByID(t, chains, "branch:ruled")
	check(t, "the findings phase ends its chain", *ruled.Terminal, branchreview.PhaseFindings)
	check(t, "the receipt's harness fills the row's", ruled.Harness, "codex")
	check(t, "a ruled review is complete", ChainEvidence(ruled, 9_000_000), EvidenceComplete)
	got := make([][3]string, len(ruled.Findings))
	for i, f := range ruled.Findings {
		got[i] = [3]string{f.Verdict, f.Qualifier, f.Text}
	}
	check(t, "the newest receipt's rulings, duplicates collapsed", got, [][3]string{
		{"ACCEPTED", "", "a.go: nil map"},
		{"ACCEPTED", "not fixed", "b.go: slow loop"},
		{"REJECTED", "", "no claim file"},
	})
	check(t, "not fixed is deferred", Deferred(ruled.Findings[1]), true)
	check(t, "a rejection is the rejected kind", KindFilter(ruled.Findings[2], "rejected"), true)

	twin := chainByID(t, chains, "branch:twin")
	check(t, "a ruling in the same second keeps its finding", len(twin.Findings), 1)

	strict := BuildChains(rows, true)
	check(t, "strict joins keep ruling joins", len(chainByID(t, strict, "branch:ruled").Findings), 3)

	unparsed := chainByID(t, chains, "branch:unparsed")
	check(t, "an unparsed review is missed at once", ChainEvidence(unparsed, 1031), EvidenceMissed)

	open := chainByID(t, chains, "branch:open")
	check(t, "an unruled review is pending for the grace after it ends",
		ChainEvidence(open, 5100+BranchRulingGrace-1), EvidencePending)
	check(t, "and missed after it", ChainEvidence(open, 5100+BranchRulingGrace), EvidenceMissed)
}

// TestLoadRowsReadsNoBranchRows checks the scoring boundary: LoadRows, and
// LoadReviewRows given a named ledger, read the commit ledger alone while a branch
// store sits beside it.
func TestLoadRowsReadsNoBranchRows(t *testing.T) {
	stateDir := branchCorpus(t)
	for name, load := range map[string]func() ([]gate.Row, error){
		"LoadRows": func() ([]gate.Row, error) { return LoadRows(stateDir, "") },
		"a named ledger": func() ([]gate.Row, error) {
			return LoadReviewRows(stateDir, filepath.Join(stateDir, "adjudication.h1.jsonl"))
		},
	} {
		rows, err := load()
		if err != nil {
			t.Fatal(err)
		}
		check(t, name+" reads the commit rows alone", len(rows), 2)
		for _, r := range rows {
			if r["review_id"] != nil || r["channel"] != nil {
				t.Errorf("%s read a branch row: %v", name, r)
			}
		}
	}
	all, err := LoadReviewRows(stateDir, "")
	if err != nil {
		t.Fatal(err)
	}
	check(t, "LoadReviewRows adds the five branch reviews with an id", len(all), 7)
}

// TestBranchReviewDirFor checks where the branch store is read from for each kind of
// commit-review state directory.
func TestBranchReviewDirFor(t *testing.T) {
	testhome.Set(t, t.TempDir())
	other := t.TempDir()
	for name, tc := range map[string][2]string{
		"the legacy store reads the neutral one": {steerpath.LegacyStateDir("commit-review"), steerpath.StateDir("branch-review")},
		"the neutral store reads its sibling":    {steerpath.StateDir("commit-review"), steerpath.StateDir("branch-review")},
		"a commit-review dir reads its sibling":  {"/x/commit-review", "/x/branch-review"},
		"any other dir reads inside itself":      {other, filepath.Join(other, "branch-review")},
	} {
		check(t, name, BranchReviewDirFor(tc[0]), tc[1])
	}
}
