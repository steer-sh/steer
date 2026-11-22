package mine

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

// --- score ------------------------------------------------------------------

// ScoreOpts are the flags of `mine score`.
type ScoreOpts struct {
	Ledger string
	ID     string
	All    bool
	JSON   bool
	// Sessions is the transcript-friction corpus, and Firings the guard audit stream.
	// A caller that reads only the ledger leaves them empty; a record scored by the
	// session engine then reports missing evidence rather than a range.
	Sessions     []SessionUnit
	Firings      []Firing
	AuditPresent bool
	CheckRuns    []CheckRun
	CheckRepos   map[string]bool
	// FixtureReceipts and the two identities that resolved them travel together:
	// a receipt read on another machine or in another checkout binds no record here.
	FixtureReceipts    map[FixtureKey]FixtureReceipt
	FixtureHost        string
	FixtureProjectRoot string
}

// Score recomputes each intervention's before/after incidence and verdict. With
// JSON set it prints an array of scored rows, `[]` when none is selected; each
// row's before/after `chains` counts the record's own unit, whatever its corpus.
func Score(w io.Writer, stateDir string, o ScoreOpts) error {
	records, err := Load(stateDir)
	if err != nil {
		return err
	}
	if o.ID != "" {
		records = filterByID(records, o.ID)
		if len(records) == 0 {
			return fmt.Errorf("no intervention with id %s", pyRepr(o.ID))
		}
	} else if !o.All {
		var kept []Record
		for _, r := range records {
			s := str(r["status"])
			if s == "active" || s == "rejected" {
				kept = append(kept, r)
			}
		}
		records = kept
	}
	if len(records) == 0 {
		if o.JSON {
			return writeJSON(w, []map[string]any{})
		}
		fmt.Fprintln(w, "# no interventions recorded yet")
		return nil
	}
	rows, err := LoadRows(stateDir, o.Ledger)
	if err != nil {
		return err
	}
	ev := EvidenceSet{
		Chains:       BuildChains(rows, false),
		Sessions:     o.Sessions,
		Firings:      o.Firings,
		AuditPresent: o.AuditPresent,
		CheckRuns:    o.CheckRuns,
		CheckRepos:   o.CheckRepos,

		FixtureReceipts:    o.FixtureReceipts,
		FixtureHost:        o.FixtureHost,
		FixtureProjectRoot: o.FixtureProjectRoot,
	}
	results := make([]map[string]any, 0, len(records))
	var unscored []string
	for _, r := range records {
		// A record from a corpus no engine reads is carried unscored. Naming one by
		// --id is an error, because the caller asked for a verdict nothing can give.
		if !Scorable(r) {
			if o.ID != "" {
				return requireScorable(r)
			}
			unscored = append(unscored, str(r["id"]))
			continue
		}
		res, err := ScoreWith(r, ev)
		if err != nil {
			return err
		}
		results = append(results, res)
	}
	if o.JSON {
		return writeJSON(w, results)
	}
	if len(unscored) > 0 {
		sort.Strings(unscored)
		fmt.Fprintf(w, "# unscored here (no engine reads that corpus): %s\n", strings.Join(unscored, ", "))
	}
	for _, r := range results {
		where := firstNonEmpty(str(r["hook_id"]), str(r["target"]), "nothing installed")
		obs, _ := r["observation"].(Observation)
		fmt.Fprintf(w, "%s  [%s]  %s -> %s\n", r["id"], observationState(obs, r["scope"] != nil), r["tier"], where)
		fmt.Fprintf(w, "    class: %s\n", r["class"])
		writeObservation(w, obs)
		verb := "installed"
		if refusalStatuses[str(r["status"])] {
			verb = "rejected"
		}
		fmt.Fprintf(w, "    %s %s  status %s\n", verb, r["installed"], r["status"])
		if str(r["reopen"]) != "" {
			fmt.Fprintf(w, "    reason: %s\n", r["reason"])
			fmt.Fprintf(w, "    reopen if: %s\n", r["reopen"])
		}
		fmt.Fprintln(w)
	}
	return nil
}

// observationState is the bracketed state on a scored line. It names what the
// corpus recorded and claims no effect, so no reading of it can become one.
//
// It reads the frozen scope for the same reason status.ActionFor does: a record
// with no rule for which work counts had every unit treated as eligible, so its
// counts describe a window nobody defined. Reporting that as quiet would print a
// settled state over the one fault the reader can repair.
func observationState(obs Observation, scoped bool) string {
	switch {
	case obs.Recurrence.Occurred:
		return "recurred"
	case !scoped:
		return "no-scope"
	case MissingCoverage(obs):
		return "coverage-gap"
	case obs.Coverage.ZeroEligibleAfter:
		return "no-eligible-work"
	}
	return "quiet"
}

// writeObservation prints the field counts under one record: the windows, the
// recurrence, the guard activity and the identities that counted each window.
// writeFixtureReceipt prints a guard-tier record's fixture line: what this machine
// knows about the stored cases behind the installed guard. Every state carries its
// own limit, because a pass is over inputs frozen at verification time and says
// nothing about the guard being reached in the field.
func writeFixtureReceipt(w io.Writer, r FixtureReceipt) {
	if !r.Applicable {
		return
	}
	switch r.State {
	case FixtureCurrent:
		fmt.Fprintf(w, "    fixtures: current — %s passed%s; validates the stored cases only\n",
			casesWord(r.Cases), shadowedClause(r.Shadowed))
	case FixtureStale:
		fmt.Fprintf(w, "    fixtures: stale — changed: %s; retained pass: %s%s\n",
			joinOrNone(r.Changed), casesWord(r.Cases), shadowedClause(r.Shadowed))
		if r.Reason != "" {
			fmt.Fprintf(w, "      %s\n", r.Reason)
		}
	case FixtureNone:
		fmt.Fprintln(w, "    fixtures: none — no passing fixture run is retained for this guard")
	default:
		fmt.Fprintf(w, "    fixtures: unavailable — %s\n", firstNonEmpty(r.Reason, "this reading cannot reach the store"))
	}
}

// casesWord names the size of a retained pass, and says so when the manifest kept
// no count rather than printing a zero it did not read.
func casesWord(cases *int) string {
	if cases == nil {
		return "an unrecorded number of stored cases"
	}
	return fmt.Sprintf("%d stored cases", *cases)
}

// shadowedClause reports cases another guard intercepted first. A pass earned with
// shadowed cases did not establish that this guard is what blocked them.
func shadowedClause(shadowed *int) string {
	if shadowed == nil || *shadowed == 0 {
		return ""
	}
	return fmt.Sprintf("; %d shadowed by another guard", *shadowed)
}

// writeCheckActivity prints a check-tier record's execution line. Other tiers
// print nothing.
func writeCheckActivity(w io.Writer, c CheckActivity) {
	switch {
	case !c.Applicable:
	case c.Attribution != AttributionCheckID:
		fmt.Fprintln(w, "    checks: unavailable — the record binds no check")
	case c.Elsewhere():
		fmt.Fprintf(w, "    checks: unknown here — registered on host %s, whose check evidence this machine does not hold\n",
			c.RegisteredHost)
	case !c.StreamPresent:
		fmt.Fprintln(w, "    checks: unknown — no check evidence for this repository on this machine")
	default:
		fmt.Fprintf(w, "    checks: %d executions of %s on this machine, counted over %s (class-scoped=%t)\n",
			c.Executions, c.CheckID, c.Scope, c.ClassScoped)
		if c.Executions > 0 {
			fmt.Fprintf(w, "      outcomes: %s; %d blocked a turn; %d ran a detector digest other than the registered one\n",
				OutcomeCounts(c.Outcomes), c.Blocked, c.ChangedDetector)
		}
	}
}

// OutcomeCounts renders outcome counts in token order, such as "failed 2, passed 9".
func OutcomeCounts(outcomes map[string]int) string {
	keys := make([]string, 0, len(outcomes))
	for k := range outcomes {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s %d", k, outcomes[k]))
	}
	return strings.Join(parts, ", ")
}

func writeObservation(w io.Writer, obs Observation) {
	c := obs.Counts
	fmt.Fprintf(w, "    window: split %s .. read %s  unit %s\n",
		stamp(c.SplitTS), stamp(c.ObservedTS), orUnitWord(c.Unit))
	fmt.Fprintf(w, "    before %d/%d   after %d/%d\n",
		c.Before.Hits, c.Before.Eligible, c.After.Hits, c.After.Eligible)
	if obs.Recurrence.Occurred {
		fmt.Fprintf(w, "    recurrence: %d after the split (%s)\n",
			obs.Recurrence.Matches, firstNonEmpty(obs.Recurrence.ExampleKind, "no example"))
		if obs.Recurrence.Example != "" {
			fmt.Fprintf(w, "      newest: %s\n", obs.Recurrence.Example)
		}
	} else {
		fmt.Fprintln(w, "    recurrence: none after the split")
	}
	switch {
	case !obs.Firing.Applicable:
		fmt.Fprintln(w, "    firing: not applicable — this tier writes no audit rows")
	case !obs.Firing.LogPresent:
		fmt.Fprintln(w, "    firing: unknown — no guard audit log on this machine")
	default:
		fmt.Fprintf(w, "    firing: %d rows for %s, counted over %s (class-scoped=%t)\n",
			obs.Firing.Rows, obs.Firing.HookID, obs.Firing.Scope, obs.Firing.ClassScoped)
	}
	writeFixtureReceipt(w, obs.Fixtures)
	writeCheckActivity(w, obs.Checks)
	if obs.Coverage.ZeroEligibleAfter {
		fmt.Fprintln(w, "    coverage: no eligible work has arrived since the split")
	}
	if UnitsExcluded(obs) {
		fmt.Fprintf(w, "    coverage: %d before and %d after could not be counted and are left out of the counts\n",
			numInt(obs.Coverage.Before["unknown"]), numInt(obs.Coverage.After["unknown"]))
	}
	fmt.Fprintf(w, "    %s: before %s  after %s\n", obs.Provenance.Kind,
		joinOrNone(obs.Provenance.Before), joinOrNone(obs.Provenance.After))
	fmt.Fprintln(w, "    these counts describe each period; they do not establish that the fix caused a change")
}

func orUnitWord(unit string) string {
	if unit == "" {
		return "unit"
	}
	return unit
}

func joinOrNone(v []string) string {
	if len(v) == 0 {
		return "none recorded"
	}
	return strings.Join(v, ", ")
}

func rateOf(m map[string]any) float64 {
	f, _ := m["rate"].(json.Number).Float64()
	return f
}

func filterByID(records []Record, id string) []Record {
	var out []Record
	for _, r := range records {
		if str(r["id"]) == id {
			out = append(out, r)
		}
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// effectPoint renders the relative-risk point estimate, or "n/a" when the baseline
// carries no rate to divide by.
func effectPoint(v any) string {
	m, _ := v.(map[string]any)
	n, ok := m["estimate"].(json.Number)
	if !ok {
		return "n/a"
	}
	return n.String() + "x"
}

// effectRange renders the two bounds, with an unbounded upper bound as an infinity sign.
func effectRange(v any) string {
	m, _ := v.(map[string]any)
	lo := "0.0"
	if n, ok := m["lower"].(json.Number); ok {
		lo = n.String()
	}
	hi := "inf"
	if n, ok := m["upper"].(json.Number); ok {
		hi = n.String()
	}
	return lo + " - " + hi
}

// attributionLine names the guard's audit join, empty when the record's tier writes no
// audit rows. A silent guard is stated plainly: a range it did not cause is not its
// effect.
func attributionLine(v any) string {
	attr, ok := v.(Attribution)
	if !ok || !attr.Applicable {
		return ""
	}
	if !attr.LogPresent {
		return "attribution: no guard audit log on this machine — the firing join is unavailable"
	}
	if attr.Firings == 0 {
		return fmt.Sprintf("attribution: %s never fired in the after-window", pyRepr(attr.HookID))
	}
	return fmt.Sprintf("attribution: %s fired %d time(s) across %d after-window session(s)",
		pyRepr(attr.HookID), attr.Firings, attr.Sessions)
}

// requiredSuffix names the clean after-window a verdict still needs, when one exists.
func requiredSuffix(v any) string {
	n, ok := v.(int)
	if !ok || n == 0 {
		return ""
	}
	return fmt.Sprintf("  needs %d clean chains", n)
}
