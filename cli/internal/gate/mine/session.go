package mine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// The signal kinds a transcript-friction class may be counted over. They are the
// scanner's own cluster kinds; SignalAny counts a session carrying the class in any.
const (
	SignalAny         = "any"
	SignalCorrection  = "user_corrections"
	SignalRejection   = "user_rejections"
	SignalToolFailure = "tool_failures"
	SignalHookBlock   = "hook_blocks"
)

// SignalKinds lists the kinds a record may name, for a caller validating a flag.
func SignalKinds() []string {
	return []string{SignalAny, SignalCorrection, SignalRejection, SignalToolFailure, SignalHookBlock}
}

// SessionSignal is one classified friction event inside a session.
type SessionSignal struct {
	Kind string  `json:"kind"`
	Text string  `json:"text"`
	TS   float64 `json:"ts"`
}

// SessionUnit is one root session as the scorer reads it: the statistical unit of the
// transcript-friction corpus. It carries no transcript format — a snapshot store
// normalizes both harnesses into this before the engine sees it. Project is the
// normalized project key, which a scope's repo predicate matches.
type SessionUnit struct {
	Harness   string          `json:"harness"`
	SessionID string          `json:"session_id"`
	Project   string          `json:"project"`
	StartTS   float64         `json:"start_ts"`
	EndTS     float64         `json:"end_ts"`
	Signals   []SessionSignal `json:"signals"`
	// Instrument is the pipeline that produced this session's signals: the
	// classifier, and the adapter that normalized this harness's transcript.
	Instrument string `json:"instrument,omitempty"`
}

// Subject reduces a session to the metadata a scope reads. A session changes no known
// file list, so the path predicate is undecidable here and a record naming one is
// refused at write time.
func (u SessionUnit) Subject() Subject {
	return Subject{
		Harness:      u.Harness,
		HarnessKnown: u.Harness != "",
		Repo:         u.Project,
		RepoKnown:    u.Project != "",
	}
}

// Firing is one guard block the master hook recorded.
type Firing struct {
	HookID    string  `json:"hook_id"`
	Harness   string  `json:"harness"`
	SessionID string  `json:"session_id"`
	TS        float64 `json:"ts"`
}

// AuditLogName is the guard store's audit stream, one JSON row per block decision.
const AuditLogName = "steer-audit.jsonl"

// LoadFirings reads the guard audit streams in the named store directories. The bool
// reports whether any store was readable, which separates "the guard never fired" from
// "no audit evidence exists here".
func LoadFirings(dirs ...string) (firings []Firing, present bool) {
	seen := map[string]bool{}
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, AuditLogName))
		if err != nil {
			continue
		}
		present = true
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var row struct {
				HookName  string `json:"hook_name"`
				Harness   string `json:"harness"`
				SessionID string `json:"session_id"`
				Timestamp string `json:"timestamp"`
			}
			if err := json.Unmarshal([]byte(line), &row); err != nil {
				continue
			}
			// The two scopes share one store on a migrated machine, so the same row can
			// be read twice. Key on the whole row.
			key := row.HookName + "\x1f" + row.Harness + "\x1f" + row.SessionID + "\x1f" + row.Timestamp
			if seen[key] {
				continue
			}
			seen[key] = true
			firings = append(firings, Firing{
				HookID:    row.HookName,
				Harness:   row.Harness,
				SessionID: row.SessionID,
				TS:        parseISO(row.Timestamp),
			})
		}
	}
	sort.SliceStable(firings, func(i, j int) bool { return firings[i].TS < firings[j].TS })
	return firings, present
}

// SessionHits are the signals in one session matching the pattern and the signal kind.
func SessionHits(u SessionUnit, pattern *regexp.Regexp, signalKind string) []SessionSignal {
	var out []SessionSignal
	for _, s := range u.Signals {
		if signalKind != "" && signalKind != SignalAny && s.Kind != signalKind {
			continue
		}
		if pattern.MatchString(s.Text) {
			out = append(out, s)
		}
	}
	return out
}

// SessionHitSequence is one bool per session, in the order the sessions started.
func SessionHitSequence(units []SessionUnit, pattern *regexp.Regexp, signalKind string) []bool {
	seq := make([]bool, len(units))
	for i, u := range units {
		seq[i] = len(SessionHits(u, pattern, signalKind)) > 0
	}
	return seq
}

// SessionIncidence is session-level: a class either occurred in a session or it did not.
func SessionIncidence(units []SessionUnit, pattern *regexp.Regexp, signalKind string) (hits, total int, rate float64) {
	for _, hit := range SessionHitSequence(units, pattern, signalKind) {
		if hit {
			hits++
		}
	}
	total = len(units)
	if total > 0 {
		rate = float64(hits) / float64(total)
	}
	return hits, total, rate
}

// SessionCoverage accounts for one window's sessions: Total == Eligible + Ineligible +
// Unknown + Partial. Partial is its own category: Unknown means the scope could not
// decide, Partial means the window could not.
type SessionCoverage struct {
	Total      int `json:"total"`
	Eligible   int `json:"eligible"`
	Ineligible int `json:"ineligible"`
	Unknown    int `json:"unknown"`
	Partial    int `json:"partial_exposure"`
	units      []SessionUnit
}

// EligibleUnits is the subset that counts toward incidence.
func (c SessionCoverage) EligibleUnits() []SessionUnit { return c.units }

// SplitSessions partitions sessions into those wholly before ts, those wholly at or
// after it, and those spanning it. A spanning session ran under both regimes.
func SplitSessions(units []SessionUnit, ts float64) (before, after, partial []SessionUnit) {
	for _, u := range units {
		end := u.EndTS
		if end < u.StartTS {
			end = u.StartTS
		}
		switch {
		case ts <= 0:
			after = append(after, u)
		case end < ts:
			before = append(before, u)
		case u.StartTS >= ts:
			after = append(after, u)
		default:
			partial = append(partial, u)
		}
	}
	return before, after, partial
}

// CoverSessions classifies a window against the scope. partial is the count
// SplitSessions held out of this window, carried here so one row accounts for every
// session the window started from.
func CoverSessions(units []SessionUnit, scope *Scope, partial int) SessionCoverage {
	cov := SessionCoverage{Total: len(units) + partial, Partial: partial}
	for _, u := range units {
		e := Eligible
		if scope != nil {
			e = scope.ClassifySubject(u.Subject())
		}
		switch e {
		case Eligible:
			cov.Eligible++
			cov.units = append(cov.units, u)
		case Ineligible:
			cov.Ineligible++
		case Unknown:
			cov.Unknown++
		}
	}
	return cov
}

// SessionBaselineWindow is the eligible before-window a writer freezes its baseline
// over, the same pass ScoreSessionRecord runs.
func SessionBaselineWindow(before []SessionUnit, scope *Scope) []SessionUnit {
	return CoverSessions(before, scope, 0).EligibleUnits()
}

// Attribution is the audit-log join beside the range: whether the guard this record
// installed fired inside the after-window.
type Attribution struct {
	// Applicable is false for a tier that writes no audit rows, and for a record
	// naming no hook id. The verdict then reads the range alone.
	Applicable bool `json:"applicable"`
	// LogPresent reports that a guard audit stream was readable at all.
	LogPresent bool   `json:"log_present"`
	HookID     string `json:"hook_id,omitempty"`
	// Firings is the join's row count; Sessions is how many distinct eligible
	// after-window sessions those rows landed in.
	Firings  int `json:"firings"`
	Sessions int `json:"sessions"`
}

// joinFirings counts this record's hook's firings that landed in the window's eligible
// sessions. The join is exact on hook id, harness and root session id.
func joinFirings(units []SessionUnit, firings []Firing, hookID string) (rows int, sessions int) {
	if hookID == "" {
		return 0, 0
	}
	index := map[string]string{} // session id -> harness
	for _, u := range units {
		index[u.SessionID] = u.Harness
	}
	hit := map[string]bool{}
	for _, f := range firings {
		if f.HookID != hookID || f.SessionID == "" {
			continue
		}
		harnessOf, ok := index[f.SessionID]
		if !ok || harnessOf != f.Harness {
			continue
		}
		rows++
		hit[f.SessionID] = true
	}
	return rows, len(hit)
}

// SessionSignalKind is the signal kind a record's regex was authored against. A record
// naming none counts a session carrying the class in any kind.
func SessionSignalKind(record Record) string {
	if k := str(record["signal_kind"]); k != "" {
		return k
	}
	return SignalAny
}

// ScoreSessionRecord is the session engine's scorer: it recomputes a transcript-class
// record's before/after incidence over root sessions, joins the guard's audit firings,
// and returns the scored shape the chain engine emits plus an attribution block.
func ScoreSessionRecord(record Record, ev EvidenceSet, minAfter int) (map[string]any, error) {
	pattern, err := compilePattern(str(record["match"]))
	if err != nil {
		return nil, err
	}
	signalKind := SessionSignalKind(record)
	scope, hasScope, err := ParseScope(record)
	if err != nil {
		return nil, err
	}
	splitTS := SplitTS(record)
	afterStart := splitTS
	if v := numFloat(record["scope_set_ts"]); v != 0 {
		afterStart = v
	}
	beforeRaw, _, beforePartial := SplitSessions(ev.Sessions, splitTS)
	_, afterRaw, afterPartial := SplitSessions(ev.Sessions, afterStart)
	beforeCov := CoverSessions(beforeRaw, scope, len(beforePartial))
	afterCov := CoverSessions(afterRaw, scope, len(afterPartial))
	before, after := beforeCov.EligibleUnits(), afterCov.EligibleUnits()

	bHits, bTotal, bRate := SessionIncidence(before, pattern, signalKind)
	aHits, aTotal, aRate := SessionIncidence(after, pattern, signalKind)
	m := Measure(bHits, bTotal, bRate, SessionHitSequence(after, pattern, signalKind), aRate)
	p := FisherOneSided(bHits, bTotal-bHits, aHits, aTotal-aHits)

	hookID := str(record["hook_id"])
	attr := Attribution{
		Applicable: firingsApplicable(str(record["tier"])) && hookID != "",
		LogPresent: ev.AuditPresent,
		HookID:     hookID,
	}
	if attr.Applicable {
		attr.Firings, attr.Sessions = joinFirings(after, ev.Firings, hookID)
	}

	authored := str(record["instrument"])
	afterInstruments := sessionInstruments(after)
	drift := authored != "" && len(driftingSessionInstruments(after, authored)) > 0
	hard, soft := SessionConfounds(before, after, authored)

	label := sessionVerdict(record, m, aTotal, minAfter, hasScope, beforeCov, afterCov, hard, attr)

	beforeHarness, afterHarness := sessionHarnesses(before), sessionHarnesses(after)
	return map[string]any{
		"id":                str(record["id"]),
		"class":             str(record["class"]),
		"tier":              str(record["tier"]),
		"target":            getString(record, "target", ""),
		"hook_id":           hookID,
		"status":            getString(record, "status", "active"),
		"installed":         installedLabel(record),
		"reason":            getString(record, "reason", ""),
		"reopen":            getString(record, "reopen", ""),
		"signal_kind":       signalKind,
		"before":            incidenceMap(bHits, bTotal, bRate),
		"after":             incidenceMap(aHits, aTotal, aRate),
		"before_coverage":   sessionCoverageMap(beforeCov),
		"after_coverage":    sessionCoverageMap(afterCov),
		"scope":             record["scope"],
		"effect":            effectMap(m.Effect),
		"required_after":    requiredAfterValue(m),
		"attribution":       attr,
		"confounds":         confoundList(hard),
		"composition":       confoundList(soft),
		"p":                 pyFloat(round4(p)),
		"verdict":           label,
		"instrument":        record["instrument"],
		"instrument_drift":  drift,
		"after_instruments": emptyIfNil(afterInstruments),
		"before_hosts":      emptyIfNil(beforeHarness),
		"after_hosts":       emptyIfNil(afterHarness),
		"host_mix_drift":    len(beforeHarness) > 0 && len(afterHarness) > 0 && !sameStringSet(beforeHarness, afterHarness),
		"observation": sessionObservation(record, before, after, beforeCov, afterCov,
			pattern, signalKind, attr, ev, afterStart, float64(now())),
	}, nil
}

// sessionVerdict is the session engine's ladder: no scope, then missing evidence, then
// a confound, then the range. Attribution refines the range's answer, never replaces a
// withheld one.
func sessionVerdict(record Record, m Measurement, aTotal, minAfter int, hasScope bool,
	before, after SessionCoverage, hard []Confound, attr Attribution) string {
	switch {
	case !hasScope:
		return "legacy-unscoped"
	case sessionEvidenceVerdict(before, after) != "":
		return sessionEvidenceVerdict(before, after)
	case attr.Applicable && !attr.LogPresent:
		return "evidence-incomplete"
	case len(hard) > 0:
		return "confounded"
	case IsRejected(record):
		return TrendFor(m, aTotal, minAfter)
	}
	label := VerdictFor(m, aTotal, minAfter)
	if !attr.Applicable || attr.Firings > 0 {
		return label
	}
	// The guard stayed silent across the window, so the range describes something else.
	switch label {
	case "effective":
		return "unattributed"
	case "ineffective", "harmful":
		return "not-triggered"
	}
	return label
}

// sessionEvidenceVerdict withholds when a window kept no eligible session. Unknown
// scope metadata is permanent; a window emptied only by partial exposure fills on its
// own, so it is collecting.
func sessionEvidenceVerdict(before, after SessionCoverage) string {
	for _, c := range []SessionCoverage{before, after} {
		if c.Eligible > 0 {
			continue
		}
		switch {
		case c.Unknown > 0:
			return "evidence-incomplete"
		case c.Partial > 0:
			return "collecting"
		}
	}
	return ""
}

func sessionCoverageMap(c SessionCoverage) map[string]any {
	return map[string]any{
		"total":            c.Total,
		"eligible":         c.Eligible,
		"ineligible":       c.Ineligible,
		"unknown":          c.Unknown,
		"partial_exposure": c.Partial,
	}
}

// sessionInstruments lists the parser instruments a window's snapshots were normalized
// by, in sorted order. A snapshot written before the stamp existed contributes nothing.
func sessionInstruments(units []SessionUnit) []string {
	var seen []string
	for _, u := range units {
		if u.Instrument != "" {
			seen = appendUnique(seen, u.Instrument)
		}
	}
	sort.Strings(seen)
	return seen
}

func sessionHarnesses(units []SessionUnit) []string {
	var seen []string
	for _, u := range units {
		if u.Harness != "" {
			seen = appendUnique(seen, u.Harness)
		}
	}
	sort.Strings(seen)
	return seen
}

func sessionProjects(units []SessionUnit) []string {
	var seen []string
	for _, u := range units {
		if u.Project != "" {
			seen = appendUnique(seen, u.Project)
		}
	}
	sort.Strings(seen)
	return seen
}

// SessionConfounds compares the two windows' categorical supports, the session corpus's
// analogue of Confounds. A session carries a harness and a project; it changes no file
// list, so path_kind has no analogue here.
func SessionConfounds(before, after []SessionUnit, authored string) (hard, soft []Confound) {
	if authored != "" {
		if drifted := driftingSessionInstruments(after, authored); len(drifted) > 0 {
			hard = append(hard, Confound{
				Category: "instrument",
				Reason:   "the after-window was normalized by a different parser or transcript adapter than the registered baseline",
				Before:   []string{authored},
				After:    drifted,
			})
		}
	}
	categories := []struct {
		name string
		of   func([]SessionUnit) []string
	}{
		{"harness", sessionHarnesses},
		{"project", sessionProjects},
	}
	for _, cat := range categories {
		b, a := cat.of(before), cat.of(after)
		if len(b) == 0 || len(a) == 0 {
			continue
		}
		switch {
		case disjoint(b, a):
			hard = append(hard, Confound{
				Category: cat.name,
				Reason:   "the before and after windows share no " + cat.name,
				Before:   b, After: a,
			})
		case !sameStringSet(b, a):
			soft = append(soft, Confound{
				Category: cat.name,
				Reason:   "the " + cat.name + " mix changed between the windows",
				Before:   b, After: a,
			})
		}
	}
	return hard, soft
}

// firingsApplicable reports whether a tier writes guard audit rows. Only the master
// PreToolUse hook does, under either spelling of its tier name.
func firingsApplicable(tier string) bool { return tier == "guard" || tier == "steer" }

// parseISO reads the RFC3339 timestamp the audit runtime writes into a unix float. An
// unparsable stamp reads as 0, which joins on the id all the same.
func parseISO(s string) float64 {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05.999999-07:00"} {
		if t, err := time.Parse(layout, s); err == nil {
			return float64(t.Unix())
		}
	}
	return 0
}
