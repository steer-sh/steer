package mine

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/steer-sh/steer-cli/internal/tier"
)

// now is the wall clock for records that carry no explicit timestamp. A variable so a
// test can pin it.
var now = func() int64 { return time.Now().Unix() }

// stamp renders a unix timestamp as mine.py's stamp does: a local YYYY-MM-DD, or "?".
func stamp(ts float64) string {
	if ts == 0 {
		return "?"
	}
	return time.Unix(int64(ts), 0).Format("2006-01-02")
}

func pct(rate float64) string { return fmt.Sprintf("%.1f%%", rate*100) }

// pyStr renders a value the way Python's str() would in an f-string.
func pyStr(v any) string {
	switch t := v.(type) {
	case nil:
		return "None"
	case *string:
		if t == nil {
			return "None"
		}
		return *t
	case json.Number:
		return t.String()
	case string:
		return t
	case bool:
		if t {
			return "True"
		}
		return "False"
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case float64:
		return string(pyFloat(t))
	}
	return fmt.Sprint(v)
}

// --- findings ---------------------------------------------------------------

// FindingsOpts are the flags of `mine findings`.
type FindingsOpts struct {
	Ledger       string
	Kind         string
	Since        float64
	StrictJoins  bool
	IncludeEmpty bool
	JSON         bool
}

// Findings emits adjudicated findings grouped by chain, for a reader to cluster.
func Findings(w io.Writer, stateDir string, o FindingsOpts) error {
	rows, err := LoadRows(stateDir, o.Ledger)
	if err != nil {
		return err
	}
	chains := BuildChains(rows, o.StrictJoins)
	var cutoff float64
	if o.Since != 0 {
		cutoff = float64(now()) - o.Since*86400
	}
	var kept []Chain
	for _, ch := range chains {
		if numFloat(ch.StartTS) >= cutoff {
			kept = append(kept, ch)
		}
	}
	chains = kept

	var payload []map[string]any
	for _, ch := range chains {
		findings := filterFindings(ch.Findings, o.Kind)
		if len(findings) == 0 && !o.IncludeEmpty {
			continue
		}
		payload = append(payload, map[string]any{
			"date":     stamp(numFloat(ch.StartTS)),
			"start_ts": ch.StartTS,
			"terminal": ch.Terminal,
			"rounds":   ch.Rounds,
			"files":    ch.Files,
			"findings": findings,
		})
	}
	reviewed := len(chains)
	records, err := Load(stateDir)
	if err != nil {
		return err
	}

	if o.JSON {
		out := map[string]any{
			"chains_reviewed": reviewed,
			"decisions":       nonNilRecords(records),
			"chains":          nonNilMaps(payload),
		}
		return writeJSON(w, out)
	}

	if len(records) > 0 {
		fmt.Fprintf(w, "# %d class(es) already decided on this corpus:\n", len(records))
		for _, r := range records {
			printDecisionIncidence(w, r, chains)
		}
		fmt.Fprintln(w, "#")
	}
	fmt.Fprintf(w, "# %d chains reviewed, %d carrying %s findings\n", reviewed, len(payload), o.Kind)
	distinct := 0
	for _, c := range payload {
		distinct += len(c["findings"].([]Finding))
	}
	fmt.Fprintf(w, "# %d distinct findings\n\n", distinct)
	for _, c := range payload {
		fmt.Fprintf(w, "--- %s  ts=%s  rounds=%d  files=%d  end=%s\n",
			c["date"], pyStr(c["start_ts"]), c["rounds"], c["files"], pyStr(c["terminal"]))
		for _, f := range c["findings"].([]Finding) {
			fmt.Fprintf(w, "  [%s] %s\n", f.Verdict, f.Text)
		}
		fmt.Fprintln(w)
	}
	return nil
}

func filterFindings(findings []Finding, kind string) []Finding {
	out := []Finding{}
	for _, f := range findings {
		if KindFilter(f.Verdict, kind) {
			out = append(out, f)
		}
	}
	return out
}

// --- match ------------------------------------------------------------------

// MatchOpts are the flags of `intervention match`.
type MatchOpts struct {
	Ledger      string
	Regex       string
	Kind        string
	StrictJoins bool
	// Corpus names the evidence the regex is tested against; empty is review-findings.
	Corpus string
	// SignalKind narrows the session corpus to one classifier kind.
	SignalKind string
	// Sessions is the session corpus, supplied by the caller that reads the store.
	Sessions []SessionUnit
}

// Match tests a candidate class regex against the corpus its record will be counted
// over. It routes to the engine that corpus names, so an author sees the same
// denominator the register will freeze.
func Match(w io.Writer, stateDir string, o MatchOpts) error {
	spec, err := writableCorpus(o.Corpus)
	if err != nil {
		return err
	}
	if spec.Engine == EngineSession {
		return matchSessions(w, o)
	}
	regex, kind, ledger, strictJoins := o.Regex, o.Kind, o.Ledger, o.StrictJoins
	pattern, err := compilePattern(regex)
	if err != nil {
		return fmt.Errorf("bad regex %s: %w", pyRepr(regex), err)
	}
	rows, err := LoadRows(stateDir, ledger)
	if err != nil {
		return err
	}
	chains := BuildChains(rows, strictJoins)
	type match struct {
		ch    Chain
		found []Finding
	}
	var matched []match
	for _, ch := range chains {
		if h := ChainHits(ch, pattern, kind); len(h) > 0 {
			matched = append(matched, match{ch, h})
		}
	}
	hits, total, rate := Incidence(chains, pattern, kind)
	fmt.Fprintf(w, "# %s matches %d/%d chains (%s)\n", pyRepr(regex), hits, total, pct(rate))
	for _, m := range matched {
		fmt.Fprintf(w, "--- %s  ts=%s\n", stamp(numFloat(m.ch.StartTS)), pyStr(m.ch.StartTS))
		for _, f := range m.found {
			fmt.Fprintf(w, "  [%s] %s\n", f.Verdict, f.Text)
		}
	}
	if len(matched) == 0 {
		fmt.Fprintln(w, "# no matches — the regex is too narrow, or the class is misnamed")
	}
	records, err := Load(stateDir)
	if err != nil {
		return err
	}
	prior, err := OverlappingDecisions(records, chains, pattern, kind)
	if err != nil {
		return err
	}
	if len(prior) > 0 {
		fmt.Fprintf(w, "\n# %d prior decision(s) cover chains this regex matches:\n", len(prior))
		for _, d := range prior {
			printDecisionShared(w, d.Record, d.Shared, d.Total)
		}
	}
	return nil
}

// --- decision headlines -----------------------------------------------------

func decisionHeadline(record Record) string {
	class := str(record["class"])
	if IsRejected(record) {
		status := strings.ToUpper(getString(record, "status", "rejected"))
		return fmt.Sprintf("%s %s  %s — %s", status, getString(record, "rejected_date", "?"),
			class, getString(record, "reason", ""))
	}
	date := str(record["installed_date"])
	if date == "" {
		date = getString(record, "rejected_date", "?")
	}
	return fmt.Sprintf("%s %s  %s -> %s", strings.ToUpper(getString(record, "status", "active")),
		date, class, str(record["tier"]))
}

func printDecisionHead(w io.Writer, record Record) {
	fmt.Fprintf(w, "#   %s\n", decisionHeadline(record))
	fmt.Fprintf(w, "#     id=%s  match=%s\n", str(record["id"]), pyRepr(str(record["match"])))
}

func printDecisionReopen(w io.Writer, record Record) {
	if r := getString(record, "reopen", ""); r != "" {
		fmt.Fprintf(w, "#     reopen if: %s\n", r)
	}
}

func printDecisionShared(w io.Writer, record Record, shared, total int) {
	printDecisionHead(w, record)
	fmt.Fprintf(w, "#     shares %d/%d of the candidate's chains\n", shared, total)
	printDecisionReopen(w, record)
}

func printDecisionIncidence(w io.Writer, record Record, chains []Chain) {
	printDecisionHead(w, record)
	pattern, err := compilePattern(str(record["match"]))
	if err == nil {
		hits, denom, rate := Incidence(chains, pattern, RecordKindFilter(record))
		fmt.Fprintf(w, "#     now %d/%d chains (%s)\n", hits, denom, pct(rate))
	}
	printDecisionReopen(w, record)
}

// --- register / reject ------------------------------------------------------

// RegisterOpts are the flags of `mine register`.
type RegisterOpts struct {
	Ledger       string
	Class        string
	Match        string
	Kind         string
	Corpus       string
	SignalKind   string
	Sessions     []SessionUnit
	Tier         string
	Target       string
	Note         string
	Prose        Prose
	HookID       string
	HookScope    string
	Supersedes   string
	InstalledTS  int64
	Force        bool
	ScopeAll     bool
	ScopeHarness []string
	ScopeRepo    string
	ScopePath    string
}

// Register records an intervention with its baseline incidence.
func Register(w io.Writer, stateDir string, o RegisterOpts) error {
	if o.Kind == "" {
		o.Kind = "defect"
	}
	if err := o.Prose.Validate(o.Class); err != nil {
		return err
	}
	spec, err := writableCorpus(o.Corpus)
	if err != nil {
		return err
	}
	kindFilter := "accepted"
	if o.Kind == "false-positive" {
		kindFilter = "rejected"
	}
	pattern, err := compilePattern(o.Match)
	if err != nil {
		return fmt.Errorf("bad regex %s: %w", pyRepr(o.Match), err)
	}
	if err := spec.ValidateSignalKind(o.SignalKind); err != nil {
		return err
	}
	scopeMap, err := BuildScope(o.ScopeAll, o.ScopeHarness, o.ScopeRepo, o.ScopePath)
	if err != nil {
		return err
	}
	if err := spec.ValidateScope(scopeMap); err != nil {
		return err
	}
	scope, _, err := ParseScope(Record{"scope": scopeMap})
	if err != nil {
		return err
	}
	rows, err := LoadRows(stateDir, o.Ledger)
	if err != nil {
		return err
	}
	chains := BuildChains(rows, false)
	installed := o.InstalledTS
	if installed == 0 {
		installed = now()
	}
	records, err := Load(stateDir)
	if err != nil {
		return err
	}
	var bHits, bTotal int
	var bRate float64
	var instrument any
	var decisions []Decision
	if spec.Engine == EngineSession {
		signalKind := o.SignalKind
		if signalKind == "" {
			signalKind = SignalAny
		}
		beforeUnits, _, _ := SplitSessions(o.Sessions, float64(installed))
		beforeUnits = SessionBaselineWindow(beforeUnits, scope)
		bHits, bTotal, bRate = SessionIncidence(beforeUnits, pattern, signalKind)
		instrument = sessionInstrumentValue(o.Sessions)
		decisions, err = SessionOverlappingDecisions(records, o.Sessions, pattern, signalKind)
	} else {
		before, _ := SplitChains(chains, float64(installed))
		before = baselineWindow(before, scope)
		bHits, bTotal, bRate = Incidence(before, pattern, kindFilter)
		instrument = instrumentValue(chains)
		decisions, err = OverlappingDecisions(recordsForCorpus(records, spec.Corpus), chains, pattern, kindFilter)
	}
	if err != nil {
		return err
	}
	if bHits == 0 && !o.Force {
		return fmt.Errorf("regex matches no unit in the scoped baseline — an intervention " +
			"with no baseline can never be scored. Widen the scope or the regex, or pass --force")
	}
	if !o.Force {
		for _, d := range decisions {
			if StandsRejected(d.Record) && d.Ratio >= OverlapBlock && str(d.Record["id"]) != o.Supersedes {
				return fmt.Errorf("this regex shares %d/%d of its chains with %s, rejected on %s: %s\n"+
					"  reopen if: %s\n"+
					"Install it anyway with --force, or --supersedes to retire the rejection",
					d.Shared, d.Total, pyRepr(str(d.Record["id"])), getString(d.Record, "rejected_date", "?"),
					getString(d.Record, "reason", ""), getString(d.Record, "reopen", "(unrecorded)"))
			}
		}
	}
	// The ladder's vocabulary is validated here, at the shared write boundary, so
	// the CLI and the dashboard refuse the same values. A typo tier used to fall
	// through every display and render as itself.
	if !tier.Valid(o.Tier) {
		return fmt.Errorf("--tier %q names no fix type. Use one of: %s",
			o.Tier, strings.Join(tier.Accepted(), ", "))
	}
	if tier.IsGuard(o.Tier) && o.HookID == "" {
		return fmt.Errorf("a guard tier needs --hook-id (the steer-manifest key, which is " +
			"also the audit log's hook_name). Without it the audit-log join is a path search " +
			"that breaks the moment the hook is renamed or retired")
	}
	record := Record{
		"id":             UniqueID(Slugify(o.Class), records),
		"class":          o.Class,
		"match":          o.Match,
		"kind":           o.Kind,
		"corpus":         spec.Corpus,
		"unit":           spec.Unit,
		"tier":           o.Tier,
		"target":         o.Target,
		"note":           o.Note,
		"installed_ts":   int(installed),
		"installed_date": stamp(float64(installed)),
		"status":         "active",
		"scope":          scopeMap,
		"baseline":       baselineMap(bHits, bTotal, bRate),
		"instrument":     instrument,
	}
	o.Prose.writeInto(record)
	if spec.Engine == EngineSession {
		if o.SignalKind != "" {
			record["signal_kind"] = o.SignalKind
		} else {
			record["signal_kind"] = SignalAny
		}
	}
	if o.HookID != "" {
		record["hook_id"] = o.HookID
		facts, err := SteerHookFacts(o.HookID, o.HookScope)
		if err != nil {
			return err
		}
		if facts != nil {
			for k, v := range facts {
				record[k] = v
			}
		} else {
			fmt.Fprintf(os.Stderr, "steer commit-gate mine: %s is not in steer-manifest.json — "+
				"recording the id anyway, but confirm it matches the audit log's hook_name\n", pyRepr(o.HookID))
		}
	}
	if o.Supersedes != "" {
		prior := findRecord(records, o.Supersedes)
		if prior == nil {
			return fmt.Errorf("no intervention with id %s", pyRepr(o.Supersedes))
		}
		if IsRejected(prior) {
			prior["status"] = "reopened"
		} else {
			prior["status"] = "reworked"
		}
		prior["retired_ts"] = int(installed)
		prior["superseded_by"] = record["id"]
		record["supersedes"] = o.Supersedes
	}
	records = append(records, record)
	if err := Save(stateDir, records); err != nil {
		return err
	}
	fmt.Fprintln(w, pyDumps(record, 2))
	return nil
}

// RejectOpts are the flags of `mine reject`.
type RejectOpts struct {
	Ledger       string
	Class        string
	Match        string
	Kind         string
	Corpus       string
	SignalKind   string
	Sessions     []SessionUnit
	Reason       string
	Reopen       string
	Note         string
	Prose        Prose
	RejectedTS   int64
	Force        bool
	ScopeAll     bool
	ScopeHarness []string
	ScopeRepo    string
	ScopePath    string
}

// Reject records a class examined and refused, so it is not re-proposed.
func Reject(w io.Writer, stateDir string, o RejectOpts) error {
	if o.Kind == "" {
		o.Kind = "defect"
	}
	if err := o.Prose.Validate(o.Class); err != nil {
		return err
	}
	spec, err := writableCorpus(o.Corpus)
	if err != nil {
		return err
	}
	kindFilter := "accepted"
	if o.Kind == "false-positive" {
		kindFilter = "rejected"
	}
	pattern, err := compilePattern(o.Match)
	if err != nil {
		return fmt.Errorf("bad regex %s: %w", pyRepr(o.Match), err)
	}
	if err := spec.ValidateSignalKind(o.SignalKind); err != nil {
		return err
	}
	scopeMap, err := BuildScope(o.ScopeAll, o.ScopeHarness, o.ScopeRepo, o.ScopePath)
	if err != nil {
		return err
	}
	if err := spec.ValidateScope(scopeMap); err != nil {
		return err
	}
	scope, _, err := ParseScope(Record{"scope": scopeMap})
	if err != nil {
		return err
	}
	rows, err := LoadRows(stateDir, o.Ledger)
	if err != nil {
		return err
	}
	chains := BuildChains(rows, false)
	rejected := o.RejectedTS
	if rejected == 0 {
		rejected = now()
	}
	var bHits, bTotal int
	var bRate float64
	var instrument any
	if spec.Engine == EngineSession {
		signalKind := o.SignalKind
		if signalKind == "" {
			signalKind = SignalAny
		}
		beforeUnits, _, _ := SplitSessions(o.Sessions, float64(rejected))
		beforeUnits = SessionBaselineWindow(beforeUnits, scope)
		bHits, bTotal, bRate = SessionIncidence(beforeUnits, pattern, signalKind)
		instrument = sessionInstrumentValue(o.Sessions)
	} else {
		before, _ := SplitChains(chains, float64(rejected))
		before = baselineWindow(before, scope)
		bHits, bTotal, bRate = Incidence(before, pattern, kindFilter)
		instrument = instrumentValue(chains)
	}
	if bHits == 0 && !o.Force {
		return fmt.Errorf("regex matches no unit in the scoped baseline — a rejection with " +
			"no baseline can never be re-scored, and cannot be recognised when the class " +
			"returns. Widen the scope or the regex, or pass --force")
	}
	records, err := Load(stateDir)
	if err != nil {
		return err
	}
	record := Record{
		"id":            UniqueID(Slugify(o.Class), records),
		"class":         o.Class,
		"match":         o.Match,
		"kind":          o.Kind,
		"corpus":        spec.Corpus,
		"unit":          spec.Unit,
		"tier":          "none",
		"target":        "",
		"note":          o.Note,
		"reason":        o.Reason,
		"reopen":        o.Reopen,
		"rejected_ts":   int(rejected),
		"rejected_date": stamp(float64(rejected)),
		"status":        "rejected",
		"scope":         scopeMap,
		"baseline":      baselineMap(bHits, bTotal, bRate),
		"instrument":    instrument,
	}
	o.Prose.writeInto(record)
	if spec.Engine == EngineSession {
		if o.SignalKind != "" {
			record["signal_kind"] = o.SignalKind
		} else {
			record["signal_kind"] = SignalAny
		}
	}
	records = append(records, record)
	if err := Save(stateDir, records); err != nil {
		return err
	}
	fmt.Fprintln(w, pyDumps(record, 2))
	return nil
}

// ScopeSetOpts are the flags of `mine scope`.
type ScopeSetOpts struct {
	Ledger       string
	Sessions     []SessionUnit
	ScopeAll     bool
	ScopeHarness []string
	ScopeRepo    string
	ScopePath    string
	ScopeSetTS   int64
	Force        bool
}

// ScopeSet installs a scope on a legacy, unscoped record. It recomputes the scoped
// baseline and stamps scope_set_ts, which opens the after-window so a migration cannot
// select on already-observed post-install results. An existing scope is never edited;
// correcting one is a retire plus a fresh register.
func ScopeSet(w io.Writer, stateDir, id string, o ScopeSetOpts) error {
	records, err := Load(stateDir)
	if err != nil {
		return err
	}
	rec := findRecord(records, id)
	if rec == nil {
		return fmt.Errorf("no intervention with id %s", pyRepr(id))
	}
	if _, has := rec["scope"]; has {
		return fmt.Errorf("%s already carries a scope — an installed scope is frozen. "+
			"Retire it and register a fresh record to correct the eligibility rule", pyRepr(id))
	}
	spec, err := writableCorpus(CorpusOf(rec))
	if err != nil {
		return err
	}
	scopeMap, err := BuildScope(o.ScopeAll, o.ScopeHarness, o.ScopeRepo, o.ScopePath)
	if err != nil {
		return err
	}
	if err := spec.ValidateScope(scopeMap); err != nil {
		return err
	}
	scope, _, err := ParseScope(Record{"scope": scopeMap})
	if err != nil {
		return err
	}
	pattern, err := compilePattern(str(rec["match"]))
	if err != nil {
		return fmt.Errorf("bad regex %s: %w", pyRepr(str(rec["match"])), err)
	}
	var bHits, bTotal int
	var bRate float64
	if spec.Engine == EngineSession {
		beforeUnits, _, _ := SplitSessions(o.Sessions, SplitTS(rec))
		beforeUnits = SessionBaselineWindow(beforeUnits, scope)
		bHits, bTotal, bRate = SessionIncidence(beforeUnits, pattern, SessionSignalKind(rec))
	} else {
		rows, err := LoadRows(stateDir, o.Ledger)
		if err != nil {
			return err
		}
		before, _ := SplitChains(BuildChains(rows, false), SplitTS(rec))
		before = baselineWindow(before, scope)
		bHits, bTotal, bRate = Incidence(before, pattern, RecordKindFilter(rec))
	}
	if bHits == 0 && !o.Force {
		return fmt.Errorf("regex matches no unit in the scoped baseline — widen the scope, or pass --force")
	}
	setTS := o.ScopeSetTS
	if setTS == 0 {
		setTS = now()
	}
	rec["scope"] = scopeMap
	rec["scope_set_ts"] = int(setTS)
	rec["baseline"] = baselineMap(bHits, bTotal, bRate)
	if err := Save(stateDir, records); err != nil {
		return err
	}
	fmt.Fprintln(w, pyDumps(rec, 2))
	return nil
}

// baselineWindow is the eligible before-window a writer freezes its baseline over. It
// runs the same evidence pass ScoreRecord runs, so the stored baseline and the scored
// one describe one set of chains. A chain with no adjudication is not evidence of the
// class's absence, so it belongs in neither.
func baselineWindow(before []Chain, scope *Scope) []Chain {
	return CoverEvidence(before, scope, float64(now())).EligibleChains()
}

// baselineMap is the stored baseline in the one candidate-record contract. Its
// count key is the corpus-neutral "units", not "chains": the record no longer
// assumes a chain corpus. The field is write-only today — nothing reads the stored
// baseline count, so legacy rows still keyed "chains" break no reader; a reader
// built later tolerates both keys at its own call site.
func baselineMap(hits, total int, rate float64) map[string]any {
	return map[string]any{"hits": hits, "units": total, "rate": pyFloat(round4(rate))}
}

// instrumentValue is the current reviewer, or nil so the record stores null the way
// mine.py stores None on an unstamped log.
func instrumentValue(chains []Chain) any {
	if inst := CurrentInstrument(chains); inst != "" {
		return inst
	}
	return nil
}

func findRecord(records []Record, id string) Record {
	for _, r := range records {
		if str(r["id"]) == id {
			return r
		}
	}
	return nil
}

// --- score ------------------------------------------------------------------

// ScoreOpts are the flags of `mine score`.
type ScoreOpts struct {
	Ledger   string
	ID       string
	All      bool
	MinAfter int
	JSON     bool
	// Sessions is the transcript-friction corpus, and Firings the guard audit stream.
	// A caller that reads only the ledger leaves them empty; a record scored by the
	// session engine then reports missing evidence rather than a range.
	Sessions     []SessionUnit
	Firings      []Firing
	AuditPresent bool
}

// Score recomputes each intervention's before/after incidence and verdict.
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
		res, err := ScoreWith(r, ev, o.MinAfter)
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
		fmt.Fprintf(w, "%s  [%s]  %s -> %s\n", r["id"], r["verdict"], r["tier"], where)
		fmt.Fprintf(w, "    class: %s\n", r["class"])
		before, after := r["before"].(map[string]any), r["after"].(map[string]any)
		fmt.Fprintf(w, "    before %d/%d (%s)   after %d/%d (%s)   p=%s\n",
			before["hits"], before["chains"], pct(rateOf(before)),
			after["hits"], after["chains"], pct(rateOf(after)),
			r["p"].(json.Number).String())
		fmt.Fprintf(w, "    effect %s  (%d%% range %s)%s\n",
			effectPoint(r["effect"]), int(Confidence*100), effectRange(r["effect"]),
			requiredSuffix(r["required_after"]))
		if line := attributionLine(r["attribution"]); line != "" {
			fmt.Fprintf(w, "    %s\n", line)
		}
		verb := "installed"
		if refusalStatuses[str(r["status"])] {
			verb = "rejected"
		}
		fmt.Fprintf(w, "    %s %s  status %s\n", verb, r["installed"], r["status"])
		if str(r["reopen"]) != "" {
			fmt.Fprintf(w, "    reason: %s\n", r["reason"])
			fmt.Fprintf(w, "    reopen if: %s\n", r["reopen"])
		}
		if r["instrument_drift"].(bool) {
			fmt.Fprintf(w, "    INSTRUMENT DRIFT: regex authored on %s, after-window reviewed by %s — "+
				"re-read the findings before trusting this verdict\n",
				pyStr(r["instrument"]), strings.Join(r["after_instruments"].([]string), ", "))
		}
		if r["host_mix_drift"].(bool) {
			fmt.Fprintf(w, "    HOST MIX DRIFT: before-window ran on %s, after-window on %s — "+
				"the two windows describe different machines, so this rate compares more than the fix\n",
				strings.Join(r["before_hosts"].([]string), ", "), strings.Join(r["after_hosts"].([]string), ", "))
		}
		fmt.Fprintln(w)
	}
	return nil
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

// --- list / retire ----------------------------------------------------------

// List shows the recorded interventions.
func List(w io.Writer, stateDir string, asJSON bool) error {
	records, err := Load(stateDir)
	if err != nil {
		return err
	}
	if len(records) == 0 {
		fmt.Fprintln(w, "# no interventions recorded yet")
		return nil
	}
	if asJSON {
		return writeJSON(w, records)
	}
	for _, r := range records {
		date := str(r["installed_date"])
		if date == "" {
			date = getString(r, "rejected_date", "?")
		}
		fmt.Fprintf(w, "%-28s %-10s %-8s %s  %s\n",
			str(r["id"]), getString(r, "status", "active"), str(r["tier"]), date, str(r["class"]))
	}
	return nil
}

// Retire marks an intervention reworked or removed.
func Retire(w io.Writer, stateDir, id, reason, status string) error {
	records, err := Load(stateDir)
	if err != nil {
		return err
	}
	hit := findRecord(records, id)
	if hit == nil {
		return fmt.Errorf("no intervention with id %s", pyRepr(id))
	}
	hit["status"] = status
	hit["retired_ts"] = int(now())
	hit["retired_reason"] = reason
	if err := Save(stateDir, records); err != nil {
		return err
	}
	fmt.Fprintln(w, pyDumps(hit, 2))
	return nil
}

// --- shared JSON output -----------------------------------------------------

// writeJSON emits a value as indented JSON, keeping json.Number floats verbatim. Key
// order and HTML escaping are the only differences from mine.py's dump, and the reader
// of a --json stream decodes it, so neither carries meaning.
func writeJSON(w io.Writer, v any) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(raw))
	return err
}

func nonNilRecords(records []Record) []Record {
	if records == nil {
		return []Record{}
	}
	return records
}

func nonNilMaps(m []map[string]any) []map[string]any {
	if m == nil {
		return []map[string]any{}
	}
	return m
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

// writableCorpus resolves a --corpus flag against the registry. An empty value is
// review-findings. A corpus no engine can freeze a baseline over is refused at write
// time rather than recorded with a baseline no reader agrees with.
func writableCorpus(name string) (CorpusSpec, error) {
	if name == "" {
		name = CorpusReviewFindings
	}
	spec, ok := LookupCorpus(name)
	if !ok {
		return CorpusSpec{}, fmt.Errorf("unknown corpus %s — known: %s",
			pyRepr(name), strings.Join(Corpora(), ", "))
	}
	if !spec.Scorable {
		return CorpusSpec{}, fmt.Errorf("corpus %s counts by %s, which no scoring engine "+
			"reads; it cannot freeze a baseline", pyRepr(name), pyRepr(spec.Unit))
	}
	return spec, nil
}

// ValidateScope refuses a scope predicate this corpus's unit cannot decide. A predicate
// no unit carries would leave every unit Unknown, which reads as missing evidence
// forever rather than as the write-time mistake it is.
func (spec CorpusSpec) ValidateScope(m map[string]any) error {
	if !spec.PathScope && str(m["path"]) != "" {
		return fmt.Errorf("--scope-path cannot be decided over corpus %s: a %s carries no "+
			"changed-path list. Scope it by harness or repo instead",
			pyRepr(spec.Corpus), pyRepr(spec.Unit))
	}
	return nil
}

// ValidateSignalKind refuses a signal kind the corpus does not classify by.
func (spec CorpusSpec) ValidateSignalKind(kind string) error {
	if kind == "" {
		return nil
	}
	if spec.Engine != EngineSession {
		return fmt.Errorf("--signal-kind applies to corpus %s only", pyRepr(CorpusTranscript))
	}
	for _, k := range SignalKinds() {
		if k == kind {
			return nil
		}
	}
	return fmt.Errorf("unknown signal kind %s — known: %s",
		pyRepr(kind), strings.Join(SignalKinds(), ", "))
}
