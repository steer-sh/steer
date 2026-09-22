package firingaudit

import (
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
