package mine

import (
	"regexp"
	"strconv"
)

// Observation is what the corpus recorded about a class, beside the verdict. It
// makes no efficacy claim and states no direction. Both engines build one, and a
// field that only one corpus can answer says which.
type Observation struct {
	Counts     ObservationCounts     `json:"counts"`
	Recurrence Recurrence            `json:"recurrence"`
	Firing     FiringActivity        `json:"firing"`
	Checks     CheckActivity         `json:"checks"`
	Coverage   ObservationCoverage   `json:"coverage"`
	Provenance ObservationProvenance `json:"provenance"`
	// Declined marks a record that installed no fix. Its after-window is the
	// period since the refusal, so its recurrence implies no fix that failed.
	Declined bool `json:"declined"`
}

// ObservationCounts is the observation period and what was counted inside it.
//
// The two timestamp pairs are not the same thing and neither replaces the other.
// SplitTS/ObservedTS are the period the reader asked about: they express an empty
// window, and they say how long a window has been quiet. First/Last activity are
// the eligible work actually seen, which an empty window cannot express at all.
type ObservationCounts struct {
	// SplitTS is the boundary between the windows: installed_ts or rejected_ts,
	// or scope_set_ts where a legacy record was migrated.
	SplitTS float64 `json:"split_ts"`
	// ObservedTS is the read's clock, closing the after-window.
	ObservedTS float64 `json:"observed_ts"`
	// Unit names what an eligible count counts: a review chain or a session.
	Unit   string          `json:"unit"`
	Before ObservationSide `json:"before"`
	After  ObservationSide `json:"after"`
}

// ObservationSide is one window's hits over its eligible units, and the bounds of
// the eligible activity itself. A zero bound means the window held no eligible
// unit, which is why the period bounds are carried separately.
type ObservationSide struct {
	Hits     int     `json:"hits"`
	Eligible int     `json:"eligible"`
	FirstTS  float64 `json:"first_activity_ts"`
	LastTS   float64 `json:"last_activity_ts"`
}

// Example kinds a recurrence can carry. An adjudicated finding is a reviewer's
// confirmed concern; a cluster key is the text a classifier grouped a session's
// friction by, and is not a confirmed escaped failure.
const (
	ExampleAdjudicatedFinding = "adjudicated_finding"
	ExampleClusterKey         = "cluster_key"
)

// Recurrence is whether the class matched after the split, and the newest match.
type Recurrence struct {
	Occurred bool `json:"occurred"`
	// Matches is how many eligible after-window units carried the class.
	Matches int `json:"matches"`
	// ExampleKind says what Example is, because the two corpora store different
	// things and only one of them is adjudicated.
	ExampleKind string  `json:"example_kind,omitempty"`
	Example     string  `json:"example,omitempty"`
	ExampleTS   float64 `json:"example_ts,omitempty"`
}

// Firing aggregation scopes. EligibleUnits is the class-scoped join: rows landing
// in the after-window's own eligible units. WindowPeriod is broader — every row
// this hook wrote inside the window's bounds, whatever work it landed on.
const (
	FiringScopeEligibleUnits = "eligible_units"
	FiringScopeWindowPeriod  = "window_period"
)

// FiringActivity is the guard audit stream beside the counts. A firing proves an
// interception; it never proves the block was correct.
//
// Scope is load-bearing and every renderer states it. A window-period count
// cannot establish that the guard fired on this class's eligible work, so
// ClassScoped is false there and class-scoped coverage stays unknown.
type FiringActivity struct {
	// Applicable is false for a tier that writes no audit rows, and for a record
	// naming no hook id.
	Applicable bool `json:"applicable"`
	// LogPresent reports that a guard audit stream was readable at all. A missing
	// log is unknown activity, never a known zero.
	LogPresent bool   `json:"log_present"`
	HookID     string `json:"hook_id,omitempty"`
	// Scope names what Rows was counted over.
	Scope string `json:"scope,omitempty"`
	// ClassScoped reports whether the rows were joined to this class's own
	// eligible units.
	ClassScoped bool `json:"class_scoped"`
	Rows        int  `json:"rows"`
	// Units is how many distinct eligible units the rows landed in, and is
	// answered only by a class-scoped join.
	Units    int     `json:"units"`
	NewestTS float64 `json:"newest_ts,omitempty"`
}

// Known reports whether a zero row count is a fact about the guard rather than
// about the evidence. A reader must not read an unknown as a silent guard.
func (f FiringActivity) Known() bool { return f.Applicable && f.LogPresent }

// ObservationCoverage carries each window's coverage map plus the one flag no map
// holds. Zero eligible after-units is absent opportunity, which is distinct from
// an observed quiet window and is not a fault.
type ObservationCoverage struct {
	Before            map[string]any `json:"before"`
	After             map[string]any `json:"after"`
	ZeroEligibleAfter bool           `json:"zero_eligible_after"`
}

// Provenance kinds: the reviewer identities that counted a chain window, and the
// parser-and-adapter identities that produced a session window's signals.
const (
	ProvenanceReviewer = "reviewer"
	ProvenanceParser   = "parser"
)

// ObservationProvenance lists every identity that counted each window. Labels
// only: no rule here compares them, and drift is graded elsewhere.
type ObservationProvenance struct {
	Kind   string   `json:"kind"`
	Before []string `json:"before"`
	After  []string `json:"after"`
}

// chainObservation builds the observation for a review-chain record. Its firing
// count is window-level: rows are counted once by hook id, harness and timestamp
// inside the window's bounds, and labelled as the broader thing they are.
func chainObservation(record Record, before, after []Chain, beforeCov, afterCov Coverage,
	pattern patternMatcher, kind string, ev EvidenceSet, splitTS, clock float64) Observation {

	bHits, _, _ := Incidence(before, pattern, kind)
	aHits, _, _ := Incidence(after, pattern, kind)

	hookID := str(record["hook_id"])
	firing := FiringActivity{
		Applicable:  firingsApplicable(str(record["tier"])) && hookID != "",
		LogPresent:  ev.AuditPresent,
		HookID:      hookID,
		Scope:       FiringScopeWindowPeriod,
		ClassScoped: false,
	}
	if firing.Applicable {
		firing.Rows, firing.NewestTS = countFiringsInPeriod(ev.Firings, hookID, splitTS, clock)
	}

	return Observation{
		Counts: ObservationCounts{
			SplitTS:    splitTS,
			ObservedTS: clock,
			Unit:       UnitChain,
			Before:     chainSide(bHits, before),
			After:      chainSide(aHits, after),
		},
		Recurrence: chainRecurrence(after, pattern, kind),
		Firing:     firing,
		Checks:     chainCheckActivity(record, ev, splitTS, clock),
		Coverage: ObservationCoverage{
			Before:            coverageMap(beforeCov),
			After:             coverageMap(afterCov),
			ZeroEligibleAfter: afterCov.Eligible == 0,
		},
		Provenance: ObservationProvenance{
			Kind:   ProvenanceReviewer,
			Before: emptyIfNil(InstrumentsIn(before)),
			After:  emptyIfNil(InstrumentsIn(after)),
		},
		Declined: IsRejected(record),
	}
}

// sessionObservation builds the observation for a transcript-friction record. Its
// firing join is the class-scoped one the session engine already computes: a
// session IS the unit here, so the rows can be tied to the eligible work.
func sessionObservation(record Record, before, after []SessionUnit,
	beforeCov, afterCov SessionCoverage, pattern patternMatcher, signalKind string,
	attr Attribution, ev EvidenceSet, splitTS, clock float64) Observation {

	bHits, _, _ := SessionIncidence(before, pattern, signalKind)
	aHits, _, _ := SessionIncidence(after, pattern, signalKind)

	firing := FiringActivity{
		Applicable:  attr.Applicable,
		LogPresent:  attr.LogPresent,
		HookID:      attr.HookID,
		Scope:       FiringScopeEligibleUnits,
		ClassScoped: true,
		Rows:        attr.Firings,
		Units:       attr.Sessions,
	}
	if firing.Applicable {
		firing.NewestTS = newestFiringIn(after, ev.Firings, attr.HookID)
	}

	return Observation{
		Counts: ObservationCounts{
			SplitTS:    splitTS,
			ObservedTS: clock,
			Unit:       UnitSession,
			Before:     sessionSide(bHits, before),
			After:      sessionSide(aHits, after),
		},
		Recurrence: sessionRecurrence(after, pattern, signalKind),
		Firing:     firing,
		Checks:     sessionCheckActivity(record, after, ev),
		Coverage: ObservationCoverage{
			Before:            sessionCoverageMap(beforeCov),
			After:             sessionCoverageMap(afterCov),
			ZeroEligibleAfter: afterCov.Eligible == 0,
		},
		Provenance: ObservationProvenance{
			Kind:   ProvenanceParser,
			Before: emptyIfNil(sessionInstruments(before)),
			After:  emptyIfNil(sessionInstruments(after)),
		},
		Declined: IsRejected(record),
	}
}

// patternMatcher is what both incidence passes need of a compiled class regex.
type patternMatcher = *regexp.Regexp

func chainSide(hits int, chains []Chain) ObservationSide {
	side := ObservationSide{Hits: hits, Eligible: len(chains)}
	for _, ch := range chains {
		side.FirstTS, side.LastTS = extend(side.FirstTS, side.LastTS, numFloat(ch.StartTS), numFloat(ch.EndTS))
	}
	return side
}

func sessionSide(hits int, units []SessionUnit) ObservationSide {
	side := ObservationSide{Hits: hits, Eligible: len(units)}
	for _, u := range units {
		side.FirstTS, side.LastTS = extend(side.FirstTS, side.LastTS, u.StartTS, u.EndTS)
	}
	return side
}

// extend widens the activity bounds by one unit. A zero timestamp is an unstamped
// unit and contributes no bound, so a missing stamp never reads as the epoch.
func extend(first, last, start, end float64) (float64, float64) {
	if end < start {
		end = start
	}
	if start > 0 && (first == 0 || start < first) {
		first = start
	}
	if end > last {
		last = end
	}
	return first, last
}

// chainRecurrence reads the newest matching adjudicated finding of the window.
func chainRecurrence(after []Chain, pattern patternMatcher, kind string) Recurrence {
	r := Recurrence{ExampleKind: ExampleAdjudicatedFinding}
	for _, ch := range after {
		hits := ChainHits(ch, pattern, kind)
		if len(hits) == 0 {
			continue
		}
		r.Matches++
		for _, f := range hits {
			if ts := numFloat(f.TS); ts >= r.ExampleTS || r.Example == "" {
				r.Example, r.ExampleTS = f.Text, ts
			}
		}
	}
	r.Occurred = r.Matches > 0
	if !r.Occurred {
		r.ExampleKind = ""
	}
	return r
}

// sessionRecurrence reads the newest matching signal of the window. A signal is a
// cluster key, not a confirmed escaped failure, which is what ExampleKind says.
func sessionRecurrence(after []SessionUnit, pattern patternMatcher, signalKind string) Recurrence {
	r := Recurrence{ExampleKind: ExampleClusterKey}
	for _, u := range after {
		hits := SessionHits(u, pattern, signalKind)
		if len(hits) == 0 {
			continue
		}
		r.Matches++
		for _, s := range hits {
			if s.TS >= r.ExampleTS || r.Example == "" {
				r.Example, r.ExampleTS = s.Text, s.TS
			}
		}
	}
	r.Occurred = r.Matches > 0
	if !r.Occurred {
		r.ExampleKind = ""
	}
	return r
}

// countFiringsInPeriod counts a hook's audit rows inside [from, to], once per hook
// id, harness and timestamp. The dedup key is the row itself rather than the
// session, so a guard that fired in a session producing no commit is still counted
// and a session producing several commits is not counted several times.
func countFiringsInPeriod(firings []Firing, hookID string, from, to float64) (rows int, newest float64) {
	if hookID == "" {
		return 0, 0
	}
	seen := map[string]bool{}
	for _, f := range firings {
		if f.HookID != hookID || f.TS < from || (to > 0 && f.TS > to) {
			continue
		}
		key := f.HookID + "\x1f" + f.Harness + "\x1f" + strconv.FormatFloat(f.TS, 'f', -1, 64)
		if seen[key] {
			continue
		}
		seen[key] = true
		rows++
		if f.TS > newest {
			newest = f.TS
		}
	}
	return rows, newest
}

// newestFiringIn is the newest timestamp of the class-scoped join the session
// engine counted, read back over the same exact key.
func newestFiringIn(units []SessionUnit, firings []Firing, hookID string) float64 {
	if hookID == "" {
		return 0
	}
	index := map[string]string{}
	for _, u := range units {
		index[u.SessionID] = u.Harness
	}
	var newest float64
	for _, f := range firings {
		if f.HookID != hookID || f.SessionID == "" {
			continue
		}
		if h, ok := index[f.SessionID]; !ok || h != f.Harness {
			continue
		}
		if f.TS > newest {
			newest = f.TS
		}
	}
	return newest
}

// UnitsExcluded reports a window that left units out of its counts: a chain with no
// scope metadata or no adjudication, or a session with no project. None has a
// repair that recovers it, and the counts over the rest stand, so it qualifies a
// reading and asks nothing of the reader.
func UnitsExcluded(obs Observation) bool {
	return numInt(obs.Coverage.Before["unknown"]) > 0 || numInt(obs.Coverage.After["unknown"]) > 0
}

// MissingCoverage reports the audit stream a guard's activity cannot be read
// without, and the check evidence a bound check's activity cannot be read without.
// It is the one predicate the scored line, the action table and the cadence sweep
// read.
func MissingCoverage(obs Observation) bool {
	if obs.Firing.Applicable && !obs.Firing.LogPresent {
		return true
	}
	if obs.Checks.Missing() {
		return true
	}
	return false
}
