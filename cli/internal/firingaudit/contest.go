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

// ContestReport is every guard's contests, and the contests no row read here
// carries.
type ContestReport struct {
	Guards []GuardContests `json:"guards"`
	// Unmatched is the contested firings no loaded row carries. HookName is the
	// guard the contest recorded when it was written, if any.
	Unmatched []ContestedFiring `json:"unmatched,omitempty"`
}

// Contests joins the contest stream to the rows on disk now, per guard and
// store. hookName narrows to one guard; installed classifies each row's
// implementation and may be nil. It reads no case evidence.
func Contests(rows []Row, contests []adjudication.Contest, receipts []adjudication.Receipt,
	excluded map[string]adjudication.Exclusion, installed func(Row) adjudication.Identity,
	hookName string) ContestReport {
	byFiring := adjudication.Contested(contests)
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
	return report
}

func reasonsOf(submissions []adjudication.Contest) []ContestReason {
	out := make([]ContestReason, 0, len(submissions))
	for _, c := range submissions {
		out = append(out, ContestReason{Reason: c.Reason, RecordedAt: c.RecordedAt})
	}
	return out
}
