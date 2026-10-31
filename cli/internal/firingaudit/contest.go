package firingaudit

import (
	"sort"
	"time"

	"github.com/steer-sh/steer-cli/internal/adjudication"
)

// Contest writes one contest of the firing firingID names, copying the row's own
// fields when a row carries the identifier. A contest whose firing no row names is
// still written, unmatched: the row may sit in a store this command did not read,
// or the master may have failed to log it.
func Contest(rows []Row, dir, firingID, reason, cwd string) (adjudication.Contest, error) {
	c := adjudication.Contest{
		Subject:    adjudication.SubjectGuard,
		FiringID:   firingID,
		Reason:     reason,
		RecordedAt: time.Now().UTC().Format(time.RFC3339),
		Cwd:        cwd,
	}
	if row, ok := Find(rows, firingID); ok {
		c.Matched = true
		c.HookID = row.HookName
		c.Harness = row.Harness
		c.SessionID = row.SessionID
		c.ThreadID = row.ThreadID
		c.StoreDir = row.StoreDir
		c.Timestamp = row.Timestamp
		c.HookDigest = row.HookDigest
		c.RuleHash = row.RuleHash
	}
	if err := adjudication.AppendContest(dir, c); err != nil {
		return adjudication.Contest{}, err
	}
	return c, nil
}

// ContestCheck writes one contest of the check block of run runID, copying the
// run's own fields when a recorded block has that run. A contest no recorded
// block matches is still written, unmatched: the run may be another machine's.
func ContestCheck(cases []CheckCase, dir, runID, reason, cwd string) (adjudication.Contest, error) {
	c := adjudication.Contest{
		Subject:    adjudication.SubjectCheck,
		RunID:      runID,
		Reason:     reason,
		RecordedAt: time.Now().UTC().Format(time.RFC3339),
		Cwd:        cwd,
	}
	if block, ok := FindCheckCase(cases, runID); ok {
		c.Matched = true
		c.Repo = block.Run.Repo
		c.Harness = block.Run.Harness
		c.SessionID = block.Run.Session
		c.Timestamp = block.Run.Started.UTC().Format(time.RFC3339Nano)
	}
	if err := adjudication.AppendContest(dir, c); err != nil {
		return adjudication.Contest{}, err
	}
	return c, nil
}

// ContestReason is one submission of a contest, as the agent wrote it.
type ContestReason struct {
	Reason     string `json:"reason"`
	RecordedAt string `json:"recorded_at"`
}

// ContestedFiring is one firing that at least one contest names.
type ContestedFiring struct {
	FiringID  string `json:"firing_id"`
	HookName  string `json:"hook_name,omitempty"`
	Harness   string `json:"harness,omitempty"`
	Timestamp string `json:"timestamp,omitempty"`
	// Applies is the row's own implementation against the installed guard:
	// adjudication.Current, Historic or Unknown. Empty for an unmatched contest.
	Applies string `json:"applies,omitempty"`
	// Judgment is the latest receipt's judgment of the firing, empty when no
	// receipt names it.
	Judgment string          `json:"judgment,omitempty"`
	Reasons  []ContestReason `json:"reasons"`
}

// GuardContests is one guard's contests in one store, beside the counts they
// are read against. Every count is of distinct firings, never of submissions.
type GuardContests struct {
	HookName string `json:"hook_name"`
	StoreDir string `json:"store_dir"`
	// Firings is the guard's rows that no exclusion retires.
	Firings int `json:"firings"`
	// NotOffered is the rows whose block carried no contest line: an older
	// master wrote them, or the master could not allocate an identifier.
	NotOffered int `json:"not_offered"`
	// Contested is the firings at least one contest names.
	Contested int `json:"contested"`
	// Unread is the identified firings no receipt names, contested or not.
	Unread int `json:"unread"`
	// ContestedExcluded is the contested firings an exclusion retires. They are
	// in no other count.
	ContestedExcluded int               `json:"contested_excluded"`
	ContestedFirings  []ContestedFiring `json:"contested_firings,omitempty"`
}

// ContestReport is every guard's and every repository's contests, and the
// contests no row or recorded block read here carries.
type ContestReport struct {
	Guards []GuardContests `json:"guards"`
	// Unmatched is the contested firings no loaded row carries. HookName is the
	// guard the contest recorded when it was written, if any.
	Unmatched []ContestedFiring `json:"unmatched,omitempty"`
	// Checks is each repository's check blocks, and UnmatchedChecks the
	// contested runs no recorded block carries.
	Checks          []RepoContests   `json:"checks,omitempty"`
	UnmatchedChecks []ContestedBlock `json:"unmatched_checks,omitempty"`
}

// RepoContests is one repository's check blocks and the contests that name
// them. Every count is of distinct blocks, never of submissions.
type RepoContests struct {
	Repo string `json:"repo"`
	// Blocks is the repository's blocks that no exclusion retires.
	Blocks int `json:"blocks"`
	// NotOffered is the blocks whose reason carried no contest line.
	NotOffered int `json:"not_offered"`
	Contested  int `json:"contested"`
	// Unread is the blocks no check receipt names, contested or not.
	Unread int `json:"unread"`
	// ContestedExcluded is the contested blocks an exclusion retires. They are
	// in no other count.
	ContestedExcluded int              `json:"contested_excluded"`
	ContestedBlocks   []ContestedBlock `json:"contested_blocks,omitempty"`
}

// ContestedBlock is one check block at least one contest names. Failures is the
// latest reading of it, empty when no receipt names it.
type ContestedBlock struct {
	RunID     string                 `json:"run_id"`
	Repo      string                 `json:"repo,omitempty"`
	Harness   string                 `json:"harness,omitempty"`
	Timestamp string                 `json:"timestamp,omitempty"`
	Failures  []adjudication.Failure `json:"failures,omitempty"`
	Reasons   []ContestReason        `json:"reasons"`
}

// Contests joins the contest stream to the rows and recorded check blocks on
// disk now, per guard and store and per repository. hookName narrows to one
// guard and so to no check block; installed classifies each row's
// implementation and may be nil. It reads no case evidence.
func Contests(rows []Row, cases []CheckCase, contests []adjudication.Contest, receipts []adjudication.Receipt,
	excluded, excludedRuns map[string]adjudication.Exclusion, installed func(Row) adjudication.Identity,
	hookName string) ContestReport {
	byFiring := adjudication.Contested(contests, adjudication.SubjectGuard)
	latest := adjudication.Latest(receipts, adjudication.SubjectGuard)
	byKey := map[string]*GuardContests{}
	var order []string
	matched := map[string]bool{}
	for _, row := range rows {
		if hookName != "" && row.HookName != hookName {
			continue
		}
		key := row.StoreDir + "\x1f" + row.HookName
		g, ok := byKey[key]
		if !ok {
			g = &GuardContests{HookName: row.HookName, StoreDir: row.StoreDir}
			byKey[key] = g
			order = append(order, key)
		}
		submissions := byFiring[row.FiringID]
		if row.FiringID != "" && len(submissions) > 0 {
			matched[row.FiringID] = true
		}
		if _, ok := excluded[row.RowDigest]; ok && row.RowDigest != "" {
			if row.FiringID != "" && len(submissions) > 0 {
				g.ContestedExcluded++
			}
			continue
		}
		g.Firings++
		if row.ContestProtocol == 0 {
			g.NotOffered++
		}
		if row.FiringID == "" {
			continue
		}
		receipt, judged := latest[row.FiringID]
		if !judged {
			g.Unread++
		}
		if len(submissions) == 0 {
			continue
		}
		g.Contested++
		var identity adjudication.Identity
		if installed != nil {
			identity = installed(row)
		}
		g.ContestedFirings = append(g.ContestedFirings, ContestedFiring{
			FiringID: row.FiringID, HookName: row.HookName, Harness: row.Harness,
			Timestamp: row.Timestamp, Judgment: receipt.Judgment,
			Applies: adjudication.AppliesStamps(row.HookDigest, row.RuleHash, identity),
			Reasons: reasonsOf(submissions),
		})
	}
	sort.Strings(order)
	report := ContestReport{Guards: []GuardContests{}}
	for _, key := range order {
		g := byKey[key]
		sort.SliceStable(g.ContestedFirings, func(i, j int) bool {
			return g.ContestedFirings[i].Timestamp < g.ContestedFirings[j].Timestamp
		})
		report.Guards = append(report.Guards, *g)
	}
	for id, submissions := range byFiring {
		if matched[id] {
			continue
		}
		recorded := submissions[len(submissions)-1]
		if hookName != "" && recorded.HookID != hookName {
			continue
		}
		report.Unmatched = append(report.Unmatched, ContestedFiring{
			FiringID: id, HookName: recorded.HookID, Harness: recorded.Harness,
			Timestamp: recorded.Timestamp, Judgment: latest[id].Judgment,
			Reasons: reasonsOf(submissions),
		})
	}
	sort.Slice(report.Unmatched, func(i, j int) bool {
		return report.Unmatched[i].FiringID < report.Unmatched[j].FiringID
	})
	if hookName == "" {
		report.Checks, report.UnmatchedChecks = checkContests(cases, contests, receipts, excludedRuns)
	}
	return report
}

// checkContests joins the check contests to the recorded blocks, per repository
// in the order the cases first name each.
func checkContests(cases []CheckCase, contests []adjudication.Contest,
	receipts []adjudication.Receipt, excludedRuns map[string]adjudication.Exclusion) ([]RepoContests, []ContestedBlock) {
	byRun := adjudication.Contested(contests, adjudication.SubjectCheck)
	latest := adjudication.Latest(receipts, adjudication.SubjectCheck)
	var repos []RepoContests
	index := map[string]int{}
	matched := map[string]bool{}
	for _, c := range cases {
		i, ok := index[c.Run.Repo]
		if !ok {
			i = len(repos)
			index[c.Run.Repo] = i
			repos = append(repos, RepoContests{Repo: c.Run.Repo})
		}
		r := &repos[i]
		submissions := byRun[c.Run.RunID]
		if len(submissions) > 0 {
			matched[c.Run.RunID] = true
		}
		if _, retired := excludedRuns[c.Run.RunID]; retired {
			if len(submissions) > 0 {
				r.ContestedExcluded++
			}
			continue
		}
		r.Blocks++
		if c.Run.ContestProtocol == 0 {
			r.NotOffered++
		}
		receipt, judged := latest[c.Run.RunID]
		if !judged {
			r.Unread++
		}
		if len(submissions) == 0 {
			continue
		}
		r.Contested++
		r.ContestedBlocks = append(r.ContestedBlocks, ContestedBlock{
			RunID: c.Run.RunID, Repo: c.Run.Repo, Harness: c.Run.Harness,
			Timestamp: c.Run.Started.UTC().Format(time.RFC3339Nano),
			Failures:  receipt.Failures, Reasons: reasonsOf(submissions),
		})
	}
	var unmatched []ContestedBlock
	for id, submissions := range byRun {
		if matched[id] {
			continue
		}
		recorded := submissions[len(submissions)-1]
		unmatched = append(unmatched, ContestedBlock{RunID: id, Repo: recorded.Repo,
			Harness: recorded.Harness, Timestamp: recorded.Timestamp,
			Failures: latest[id].Failures, Reasons: reasonsOf(submissions)})
	}
	sort.Slice(unmatched, func(i, j int) bool { return unmatched[i].RunID < unmatched[j].RunID })
	return repos, unmatched
}

func reasonsOf(submissions []adjudication.Contest) []ContestReason {
	out := make([]ContestReason, 0, len(submissions))
	for _, c := range submissions {
		out = append(out, ContestReason{Reason: c.Reason, RecordedAt: c.RecordedAt})
	}
	return out
}
