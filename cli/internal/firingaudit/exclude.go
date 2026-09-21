package firingaudit

import (
	"fmt"
	"strings"
	"time"

	"github.com/steer-sh/steer-cli/internal/adjudication"
)

// minDigestPrefix is the shortest row digest prefix FindRef accepts.
const minDigestPrefix = 8

// FindRef returns the one row a reference names: a firing identifier, a full row
// digest, or a row digest prefix of at least eight characters that matches one
// row alone.
func FindRef(rows []Row, ref string) (Row, error) {
	if ref == "" {
		return Row{}, fmt.Errorf("name a firing identifier or a row digest")
	}
	if row, ok := Find(rows, ref); ok {
		return row, nil
	}
	if len(ref) < minDigestPrefix {
		return Row{}, fmt.Errorf("no audit row carries the identifier %q, and a row digest prefix needs %d characters",
			ref, minDigestPrefix)
	}
	var matches []Row
	for _, r := range rows {
		if strings.HasPrefix(r.RowDigest, ref) {
			matches = append(matches, r)
		}
	}
	switch len(matches) {
	case 0:
		return Row{}, fmt.Errorf("no audit row carries the identifier or row digest %q", ref)
	case 1:
		return matches[0], nil
	default:
		return Row{}, fmt.Errorf("the row digest prefix %q matches %d rows; give more of it", ref, len(matches))
	}
}

// Correction is what an operator supplies to retire or restore one row.
type Correction struct {
	Ref      string
	Action   string
	Reason   string
	Evidence []string
}

// Correct writes one exclusion entry for the row c.Ref names, copying the row's
// own fields. It refuses to exclude a row already excluded and to reinstate a row
// that is not, because either entry would record a change that did not happen.
func Correct(rows []Row, dir string, c Correction,
	excluded map[string]adjudication.Exclusion) (adjudication.Exclusion, error) {
	row, err := FindRef(rows, c.Ref)
	if err != nil {
		return adjudication.Exclusion{}, err
	}
	_, retired := excluded[row.RowDigest]
	if c.Action == adjudication.Exclude && retired {
		return adjudication.Exclusion{}, fmt.Errorf("row %s is already excluded", row.RowDigest)
	}
	if c.Action == adjudication.Reinstate && !retired {
		return adjudication.Exclusion{}, fmt.Errorf("row %s is not excluded", row.RowDigest)
	}
	e := adjudication.Exclusion{
		RowDigest:  row.RowDigest,
		FiringID:   row.FiringID,
		HookID:     row.HookName,
		Harness:    row.Harness,
		SessionID:  row.SessionID,
		Timestamp:  row.Timestamp,
		StoreDir:   row.StoreDir,
		Action:     c.Action,
		Reason:     c.Reason,
		Evidence:   c.Evidence,
		RecordedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if err := adjudication.AppendExclusion(dir, e); err != nil {
		return adjudication.Exclusion{}, err
	}
	return e, nil
}
