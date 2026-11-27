package mine

import (
	"path/filepath"
	"regexp"
	"sort"

	"github.com/steer-sh/steer-cli/internal/branchreview"
	"github.com/steer-sh/steer-cli/internal/gate"
	"github.com/steer-sh/steer-cli/internal/steerpath"
)

// The review channels a Chain comes from: a commit's review in the adjudication
// ledger, or one review of a whole branch in the branch-review store.
const (
	ChannelCommit = "commit"
	ChannelBranch = gate.ChannelBranch
)

// BranchRulingGrace is how long, in seconds, a branch review's findings wait for the
// author's rulings before its chain reads as missed.
const BranchRulingGrace = 3600

// RulingJoin is the ledger_join a branch review row takes from its ruling receipt.
const RulingJoin = "ruling"

var (
	branchReviewsRe = regexp.MustCompile(`^reviews\.(.+)\.jsonl$`)
	branchRulingsRe = regexp.MustCompile(`^rulings\.(.+)\.jsonl$`)
)

// BranchReviewDirFor is the branch-review store beside the commit-review state
// directory stateDir: the neutral store for the legacy commit-review directory,
// the sibling branch-review directory for any directory named commit-review, and a
// branch-review directory inside any other.
func BranchReviewDirFor(stateDir string) string {
	switch {
	case stateDir == steerpath.LegacyStateDir("commit-review"):
		return steerpath.StateDir(branchreview.Component)
	case filepath.Base(stateDir) == "commit-review":
		return filepath.Join(filepath.Dir(stateDir), branchreview.Component)
	}
	return filepath.Join(stateDir, branchreview.Component)
}

// LoadReviewRows is LoadRows plus the branch reviews beside stateDir, in timestamp
// order, for the readers that find candidates. A named ledger reads no branch rows.
// No reader that scores a fix or freezes a baseline calls it: those read LoadRows.
func LoadReviewRows(stateDir, ledger string) ([]gate.Row, error) {
	rows, err := LoadRows(stateDir, ledger)
	if err != nil || ledger != "" {
		return rows, err
	}
	branch, err := loadBranchRows(BranchReviewDirFor(stateDir))
	if err != nil {
		return nil, err
	}
	if len(branch) == 0 {
		return rows, nil
	}
	rows = append(rows, branch...)
	sort.SliceStable(rows, func(i, j int) bool {
		return rowTS(rows[i]) < rowTS(rows[j])
	})
	return rows, nil
}

// LoadBranchChains is the chains of the branch reviews beside stateDir: the chains
// BuildChains makes of LoadReviewRows that LoadRows lacks, since a branch row is
// placed by its own chain_id and shares no reply with a commit row. A named ledger
// reads none. A reader that counts candidates beside a commit corpus it already
// holds adds these; no reader that scores a fix calls it.
func LoadBranchChains(stateDir, ledger string) ([]Chain, error) {
	if ledger != "" {
		return nil, nil
	}
	rows, err := loadBranchRows(BranchReviewDirFor(stateDir))
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	sort.SliceStable(rows, func(i, j int) bool {
		return rowTS(rows[i]) < rowTS(rows[j])
	})
	return BuildChains(rows, false), nil
}

// loadBranchRows reads every host's review rows in dir, each keyed to its own chain
// "branch:<review_id>". A row whose review was ruled carries the newest receipt's
// rulings as its concerns, joined as RulingJoin. A missing dir holds no rows.
func loadBranchRows(dir string) ([]gate.Row, error) {
	receipts, err := latestReceipts(dir)
	if err != nil {
		return nil, err
	}
	paths, err := filepath.Glob(filepath.Join(dir, "reviews.*.jsonl"))
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	rows, err := loadShards(paths, func(p string) string { return branchShardHost(branchReviewsRe, p) },
		func(row gate.Row) { recoverChangedPaths(dir, row) })
	if err != nil {
		return nil, err
	}
	out := rows[:0]
	for _, row := range rows {
		id := str(row["review_id"])
		if id == "" {
			continue
		}
		row["channel"] = ChannelBranch
		row["chain_id"] = "branch:" + id
		if receipt, ok := receipts[rowHost(row)+"\x00"+id]; ok {
			applyReceipt(row, receipt)
		}
		out = append(out, row)
	}
	return out, nil
}

// latestReceipts indexes the newest ruling receipt of each review by host and
// review ID.
func latestReceipts(dir string) (map[string]branchreview.Receipt, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "rulings.*.jsonl"))
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	latest := map[string]branchreview.Receipt{}
	for _, p := range paths {
		shard := branchShardHost(branchRulingsRe, p)
		receipts, err := branchreview.LoadReceipts(dir, shard)
		if err != nil {
			return nil, err
		}
		for _, r := range receipts {
			host := r.Host
			if host == "" {
				host = shard
			}
			key := host + "\x00" + r.ReviewID
			if prev, ok := latest[key]; !ok || r.TS >= prev.TS {
				latest[key] = r
			}
		}
	}
	return latest, nil
}

// applyReceipt stamps receipt onto its review row: the harness the row lacks and,
// for a review whose findings were all parsed, each ruling as one concern.
func applyReceipt(row gate.Row, receipt branchreview.Receipt) {
	if str(row["harness"]) == "" && receipt.Harness != "" {
		row["harness"] = receipt.Harness
	}
	phase := str(row["phase"])
	if (phase != branchreview.PhaseFindings && phase != branchreview.PhaseNitpick) ||
		numInt(row["findings_unparsed"]) != 0 {
		return
	}
	list := make([]any, 0, len(receipt.Rulings))
	for _, r := range receipt.Rulings {
		text := r.Claim
		if r.File != "" {
			text = r.File + ": " + r.Claim
		}
		list = append(list, map[string]any{
			"verdict": r.Verdict, "qualifier": r.Qualifier, "concern": text, "resolution": r.Resolution,
		})
	}
	row["concerns"] = list
	row["ledger_found"] = true
	row["ledger_ts"] = receipt.TS
	row["ledger_join"] = RulingJoin
	row["ledger_round"] = 1
}

// branchShardHost is the host a branch-review stream's file name carries.
func branchShardHost(re *regexp.Regexp, path string) string {
	if m := re.FindStringSubmatch(filepath.Base(path)); m != nil {
		return m[1]
	}
	return "unknown"
}

// branchEvidence grades a branch review chain. A review whose findings were handed
// over owes the author's rulings: it is pending for BranchRulingGrace after the review
// ends and missed after that, since no resolver searches for a branch ruling; one with unparsed
// replies can never be ruled, so it is missed at once.
func branchEvidence(ch Chain, now float64) Evidence {
	worst := EvidenceComplete
	for _, row := range ch.Rows {
		phase := str(row["phase"])
		if phase != branchreview.PhaseFindings && phase != branchreview.PhaseNitpick {
			continue
		}
		if _, adjudicated := row["concerns"]; adjudicated || truthy(row["ledger_found"]) {
			continue
		}
		// The row is stamped when the review starts; its findings arrive when it ends.
		handed := rowTS(row) + numFloat(row["review_elapsed_ms"])/1000
		state := EvidenceMissed
		if numInt(row["findings_unparsed"]) == 0 && now < handed+BranchRulingGrace {
			state = EvidencePending
		}
		if state > worst {
			worst = state
		}
	}
	return worst
}
