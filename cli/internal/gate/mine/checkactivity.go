package mine

import "github.com/steer-sh/steer-cli/internal/tier"

// CheckRun is one live execution of a repository check, reduced to the fields the
// observation joins on.
type CheckRun struct {
	Repo           string
	CheckID        string
	Harness        string
	SessionID      string
	Outcome        string
	DetectorDigest string
	TS             float64
	// Blocked marks a failed execution whose oracle run blocked the turn.
	Blocked bool
}

// CheckActivity is the check execution stream beside the counts, for a check-tier
// record. It counts executions recorded on this machine only. Its scopes are the
// guard join's: a chain record counts every execution inside the window's bounds,
// and a session record counts executions whose Stop run carried an eligible session.
type CheckActivity struct {
	// Applicable is true for a check-tier record.
	Applicable bool `json:"applicable"`
	// Attribution is CheckAttribution of the record: an unbound record can join
	// no execution.
	Attribution string `json:"attribution,omitempty"`
	// StreamPresent reports that this machine holds any check evidence for the
	// record's repository. Without it a zero is unknown activity.
	StreamPresent bool   `json:"stream_present"`
	CheckID       string `json:"check_id,omitempty"`
	Repo          string `json:"repo,omitempty"`
	Scope         string `json:"scope,omitempty"`
	ClassScoped   bool   `json:"class_scoped"`
	Executions    int    `json:"executions"`
	// Outcomes counts the executions by outcome token.
	Outcomes map[string]int `json:"outcomes,omitempty"`
	// Blocked counts failed executions whose run blocked a turn.
	Blocked int `json:"blocked"`
	// ChangedDetector counts executions of a detector other than the one the
	// record registered.
	ChangedDetector int `json:"changed_detector"`
	// Units is the number of distinct eligible sessions the executions landed in,
	// answered only by the session join.
	Units    int     `json:"units"`
	NewestTS float64 `json:"newest_ts,omitempty"`
}

// Known reports whether a zero execution count is a fact about the check rather
// than about the evidence on this machine.
func (c CheckActivity) Known() bool {
	return c.Applicable && c.Attribution == AttributionCheckID && c.StreamPresent
}

// Missing reports a bound record whose repository has no check evidence here.
func (c CheckActivity) Missing() bool {
	return c.Applicable && c.Attribution == AttributionCheckID && !c.StreamPresent
}

// checkActivityBase fills the fields every scope shares, and reports the binding.
func checkActivityBase(record Record, ev EvidenceSet) (CheckActivity, CheckFix, bool) {
	if !tier.IsCheck(str(record["tier"])) {
		return CheckActivity{}, CheckFix{}, false
	}
	act := CheckActivity{Applicable: true, Attribution: CheckAttribution(record)}
	fix, bound := CheckFixOf(record)
	if !bound {
		return act, fix, false
	}
	act.CheckID, act.Repo = fix.ID, fix.Repo
	act.StreamPresent = ev.CheckRepos[fix.Repo]
	return act, fix, true
}

// chainCheckActivity counts the check's executions inside [from, to].
func chainCheckActivity(record Record, ev EvidenceSet, from, to float64) CheckActivity {
	act, fix, bound := checkActivityBase(record, ev)
	if !act.Applicable {
		return act
	}
	act.Scope = FiringScopeWindowPeriod
	if !bound {
		return act
	}
	for _, run := range ev.CheckRuns {
		if run.CheckID != fix.ID || run.Repo != fix.Repo || run.TS < from || (to > 0 && run.TS > to) {
			continue
		}
		act.add(run, fix)
	}
	return act
}

// sessionCheckActivity counts the check's executions whose Stop run carried one of
// the eligible sessions, on the same harness.
func sessionCheckActivity(record Record, units []SessionUnit, ev EvidenceSet) CheckActivity {
	act, fix, bound := checkActivityBase(record, ev)
	if !act.Applicable {
		return act
	}
	act.Scope, act.ClassScoped = FiringScopeEligibleUnits, true
	if !bound {
		return act
	}
	index := map[string]string{}
	for _, u := range units {
		index[u.SessionID] = u.Harness
	}
	hit := map[string]bool{}
	for _, run := range ev.CheckRuns {
		if run.CheckID != fix.ID || run.Repo != fix.Repo || run.SessionID == "" {
			continue
		}
		if h, ok := index[run.SessionID]; !ok || h != run.Harness {
			continue
		}
		act.add(run, fix)
		hit[run.SessionID] = true
	}
	act.Units = len(hit)
	return act
}

func (c *CheckActivity) add(run CheckRun, fix CheckFix) {
	c.Executions++
	if c.Outcomes == nil {
		c.Outcomes = map[string]int{}
	}
	c.Outcomes[run.Outcome]++
	if run.Blocked {
		c.Blocked++
	}
	if run.DetectorDigest != "" && run.DetectorDigest != fix.Digest {
		c.ChangedDetector++
	}
	if run.TS > c.NewestTS {
		c.NewestTS = run.TS
	}
}
