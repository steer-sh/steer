package mine

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/steer-sh/steer-cli/internal/steerpath"
	"github.com/steer-sh/steer-cli/internal/tier"
)

// --- register / reject ------------------------------------------------------

// RegisterOpts are the flags of `mine register`.
type RegisterOpts struct {
	Ledger     string
	Class      string
	Match      string
	Kind       string
	Corpus     string
	SignalKind string
	Sessions   []SessionUnit
	// Firings is this machine's guard audit stream less excluded rows. A guard
	// record is refused while it holds a block from that guard before the cutoff.
	Firings   []Firing
	Tier      string
	Target    string
	Note      string
	Prose     Prose
	HookID    string
	HookScope string
	RuleID    string
	RuleScope string
	// AddendumID names the review addendum entry a review-tier record binds.
	AddendumID    string
	AddendumScope string
	// CheckID names the declared check a check-tier record binds, and CheckFacts is
	// that check's frozen state, read by the caller: the adapters that resolve a
	// registration live outside this package.
	CheckID    string
	CheckFacts map[string]any
	// NotMechanizable is required when Tier names the instruction rung.
	NotMechanizable string
	Supersedes      string
	InstalledTS     int64
	Force           bool
	ScopeAll        bool
	ScopeHarness    []string
	ScopeRepo       string
	ScopeRepoRoot   string
	ScopePath       string
}

// Register records an intervention with its baseline incidence.
func Register(w io.Writer, stateDir string, o RegisterOpts) error {
	if o.Kind == "" {
		o.Kind = "defect"
	}
	if err := o.Prose.Validate(o.Class); err != nil {
		return err
	}
	t, err := resolveWriterTarget(o.Corpus, o.Match, o.Kind, o.SignalKind,
		o.ScopeAll, o.ScopeHarness, o.ScopeRepo, o.ScopeRepoRoot, o.ScopePath)
	if err != nil {
		return err
	}
	spec, scopeMap, scope := t.spec, t.scopeMap, t.scope
	installed := o.InstalledTS
	if installed == 0 {
		installed = now()
	}
	base, err := writerBaseline(stateDir, o.Ledger, t, o.Sessions, float64(installed))
	if err != nil {
		return err
	}
	release, err := lockInventory(stateDir)
	if err != nil {
		return err
	}
	defer release()
	records, err := Load(stateDir)
	if err != nil {
		return err
	}
	var decisions []Decision
	if spec.Engine == EngineSession {
		decisions, err = SessionOverlappingDecisions(records, o.Sessions, t.pattern, t.signalKind)
	} else {
		decisions, err = OverlappingDecisions(recordsForCorpus(records, spec.Corpus), base.chains,
			t.pattern, t.kindFilter)
	}
	if err != nil {
		return err
	}
	if base.hits == 0 && !o.Force {
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
	// A check record binds the check whose execution evidence it is scored beside,
	// and the repository that check runs in.
	if tier.IsCheck(o.Tier) {
		if err := requireCheckBinding(o.CheckID, o.CheckFacts); err != nil {
			return err
		}
		if err := requireCheckScope(o.CheckID, o.CheckFacts, scopeMap); err != nil {
			return err
		}
	}
	// An instruction record names the rule its digest was taken over. Without it the
	// record names a document and no text inside it, and nothing later can say which
	// prose the decision was made about.
	if tier.IsInstruction(o.Tier) {
		if o.RuleID == "" {
			return fmt.Errorf("an instruction tier needs --rule-id (the steer-manifest rules " +
				"key). Install the rule first with `steer rules add <id> --rule \"<text>\"`")
		}
		// The instruction rung is the last one to reach for, so the record states why
		// the two rungs above it cannot detect this class.
		if err := ValidateArgument(NotMechanizableFlag, o.NotMechanizable,
			"why a repository check and a pre-tool guard cannot detect this class",
			o.Class, ""); err != nil {
			return err
		}
	}
	// A review record names the addendum entry it installed, and only a reviewer false
	// positive routes to the review prompt.
	if tier.IsReview(o.Tier) {
		if o.AddendumID == "" {
			return fmt.Errorf("a review tier needs --addendum-id (the steer-manifest " +
				"review_addendum key). Install the entry first with " +
				"`steer commit-gate addendum add <id> --text \"<text>\"`")
		}
		if o.Kind != "false-positive" {
			return fmt.Errorf("a review tier needs --kind false-positive: the review prompt " +
				"changes what the reviewer raises, so a defect class never routes there")
		}
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
		"baseline":       baselineMap(base.hits, base.total, base.rate),
		"instrument":     base.instrument,
	}
	o.Prose.writeInto(record)
	if spec.Engine == EngineSession {
		if o.SignalKind != "" {
			record["signal_kind"] = o.SignalKind
		} else {
			record["signal_kind"] = SignalAny
		}
	}
	for k, v := range o.CheckFacts {
		record[k] = v
	}
	if o.HookID != "" {
		record["hook_id"] = o.HookID
		facts, err := SteerHookFacts(o.HookID, o.HookScope)
		if err != nil {
			return err
		}
		if facts != nil {
			fix := GuardFix{ID: o.HookID, Scope: str(facts[HookScopeKey])}
			if err := refuseRunningGuard(o, records, fix, scope, installed); err != nil {
				return err
			}
			for k, v := range facts {
				record[k] = v
			}
		} else {
			unbound := GuardFix{ID: o.HookID, Scope: o.HookScope}
			if err := refuseRunningGuard(o, records, unbound, scope, installed); err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "steer fix: %s is not in steer-manifest.json — "+
				"recording the id anyway, but confirm it matches the audit log's hook_name\n", pyRepr(o.HookID))
		}
	}
	if v := strings.TrimSpace(o.NotMechanizable); v != "" {
		record[NotMechanizableKey] = v
	}
	if o.RuleID != "" {
		record["rule_id"] = o.RuleID
		facts, err := SteerRuleFacts(o.RuleID, o.RuleScope)
		if err != nil {
			return err
		}
		if facts == nil {
			return fmt.Errorf("no rule %s in the steer store. Install it first with "+
				"`steer rules add %s --rule \"<text>\"`", pyRepr(o.RuleID), o.RuleID)
		}
		for k, v := range facts {
			record[k] = v
		}
	}
	if o.AddendumID != "" {
		record["addendum_id"] = o.AddendumID
		facts, err := SteerAddendumFacts(o.AddendumID, o.AddendumScope)
		if err != nil {
			return err
		}
		if facts == nil {
			return fmt.Errorf("no review addendum entry %s in the steer store. Install it first "+
				"with `steer commit-gate addendum add %s --text \"<text>\"`",
				pyRepr(o.AddendumID), o.AddendumID)
		}
		if facts["addendum_scope"] == steerpath.ProjectScope {
			if err := RequireAddendumAtHead(o.AddendumID, str(facts["addendum"])); err != nil {
				return err
			}
		}
		for k, v := range facts {
			record[k] = v
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
	if err := save(stateDir, records); err != nil {
		return err
	}
	if o.Supersedes != "" {
		if err := forgetRead(stateDir, findRecord(records, o.Supersedes)); err != nil {
			return err
		}
	}
	fmt.Fprintln(w, pyDumps(record, 2))
	return nil
}

// refuseRunningGuard refuses a guard record whose before window holds blocks the
// same guard already made on this machine: those blocks lowered the incidence the
// record would take as its baseline. A fix with no scope is a guard the manifest
// does not hold, and its blocks from either store count. A block the record's scope
// excludes never reached its baseline. A replacement for a record
// bound to the same guard opens a fresh window by design, and Force records anyway.
func refuseRunningGuard(o RegisterOpts, records []Record, fix GuardFix, scope *Scope, installed int64) error {
	if o.Force || !tier.IsGuard(o.Tier) {
		return nil
	}
	if prior := findRecord(records, o.Supersedes); o.Supersedes != "" && prior != nil {
		bound, ok := GuardFixOf(prior)
		if ok && bound.ID == fix.ID && (bound.Scope == "" || bound.Scope == fix.Scope) {
			return nil
		}
	}
	var blocks int
	var first float64
	for _, f := range o.Firings {
		if f.HookID != fix.ID || (fix.Scope != "" && f.Scope != fix.Scope) || f.TS >= float64(installed) {
			continue
		}
		if scope != nil && scope.ClassifySubject(f.Subject()) == Ineligible {
			continue
		}
		if blocks == 0 || f.TS < first {
			first = f.TS
		}
		blocks++
	}
	if blocks == 0 {
		return nil
	}
	return fmt.Errorf("guard %s already blocked work on this machine: %d recorded block(s) before "+
		"the cutoff %s, the first at %s. Its before window would count time the guard already "+
		"ran. Pass --installed-ts %d to split at its first block, previewing it with "+
		"`steer fix match` under the same flag, or record anyway with --force",
		pyRepr(fix.ID), blocks, stamp(float64(installed)), stamp(first), int64(first))
}

// RejectOpts are the flags of `mine reject`.
type RejectOpts struct {
	Ledger        string
	Class         string
	Match         string
	Kind          string
	Corpus        string
	SignalKind    string
	Sessions      []SessionUnit
	Reason        string
	Reopen        string
	ReopenMatch   string
	ReopenAt      int
	Note          string
	Prose         Prose
	RejectedTS    int64
	Force         bool
	ScopeAll      bool
	ScopeHarness  []string
	ScopeRepo     string
	ScopeRepoRoot string
	ScopePath     string
}

// validateReopenSubset refuses half a reopen trigger. A regex without a count
// names no threshold, and a count without a regex names no subset to count.
func validateReopenSubset(match string, at int) error {
	switch {
	case match == "" && at == 0:
		return nil
	case match == "":
		return fmt.Errorf("--reopen-at needs --reopen-match: a threshold with no regex " +
			"cannot be evaluated by a later pass")
	case at == 0:
		return fmt.Errorf("--reopen-match needs --reopen-at: a subset regex with no " +
			"threshold never reports itself met")
	}
	if _, err := compilePattern(match); err != nil {
		return fmt.Errorf("--reopen-match does not compile: %w", err)
	}
	return nil
}

// Reject records a class examined and refused, so it is not re-proposed.
func Reject(w io.Writer, stateDir string, o RejectOpts) error {
	if o.Kind == "" {
		o.Kind = "defect"
	}
	if err := o.Prose.Validate(o.Class); err != nil {
		return err
	}
	if err := validateReopenSubset(o.ReopenMatch, o.ReopenAt); err != nil {
		return err
	}
	t, err := resolveWriterTarget(o.Corpus, o.Match, o.Kind, o.SignalKind,
		o.ScopeAll, o.ScopeHarness, o.ScopeRepo, o.ScopeRepoRoot, o.ScopePath)
	if err != nil {
		return err
	}
	spec := t.spec
	rejected := o.RejectedTS
	if rejected == 0 {
		rejected = now()
	}
	base, err := writerBaseline(stateDir, o.Ledger, t, o.Sessions, float64(rejected))
	if err != nil {
		return err
	}
	if base.hits == 0 && !o.Force {
		return fmt.Errorf("regex matches no unit in the scoped baseline — a rejection with " +
			"no baseline can never be re-scored, and cannot be recognised when the class " +
			"returns. Widen the scope or the regex, or pass --force")
	}
	release, err := lockInventory(stateDir)
	if err != nil {
		return err
	}
	defer release()
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
		"reopen_match":  o.ReopenMatch,
		"reopen_at":     o.ReopenAt,
		"rejected_ts":   int(rejected),
		"rejected_date": stamp(float64(rejected)),
		"status":        "rejected",
		"scope":         t.scopeMap,
		"baseline":      baselineMap(base.hits, base.total, base.rate),
		"instrument":    base.instrument,
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
	if err := save(stateDir, records); err != nil {
		return err
	}
	fmt.Fprintln(w, pyDumps(record, 2))
	return nil
}

// ScopeSetOpts are the flags of `mine scope`.
type ScopeSetOpts struct {
	Ledger        string
	Sessions      []SessionUnit
	ScopeAll      bool
	ScopeHarness  []string
	ScopeRepo     string
	ScopeRepoRoot string
	ScopePath     string
	ScopeSetTS    int64
	Force         bool
}

// ScopeSet installs a scope on a legacy, unscoped record. It recomputes the scoped
// baseline and stamps scope_set_ts, which opens the after-window so a migration cannot
// select on already-observed post-install results. An existing scope is never edited;
// correcting one is a retire plus a fresh register.
func ScopeSet(w io.Writer, stateDir, id string, o ScopeSetOpts) error {
	release, err := lockInventory(stateDir)
	if err != nil {
		return err
	}
	defer release()
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
	scopeMap, err := BuildScope(o.ScopeAll, o.ScopeHarness, o.ScopeRepo, o.ScopeRepoRoot, o.ScopePath)
	if err != nil {
		return err
	}
	if err := spec.ValidateScope(scopeMap); err != nil {
		return err
	}
	if fix, bound := CheckFixOf(rec); bound {
		facts := map[string]any{CheckRepoKey: fix.Repo}
		if err := requireCheckScope(fix.ID, facts, scopeMap); err != nil {
			return err
		}
	}
	scope, _, err := ParseScope(Record{"scope": scopeMap})
	if err != nil {
		return err
	}
	pattern, err := compilePattern(str(rec["match"]))
	if err != nil {
		return fmt.Errorf("bad regex %s: %w", pyRepr(str(rec["match"])), err)
	}
	t := writerTarget{spec: spec, pattern: pattern, kindFilter: RecordKindFilter(rec),
		signalKind: SessionSignalKind(rec), scopeMap: scopeMap, scope: scope}
	base, err := writerBaseline(stateDir, o.Ledger, t, o.Sessions, SplitTS(rec))
	if err != nil {
		return err
	}
	if base.hits == 0 && !o.Force {
		return fmt.Errorf("regex matches no unit in the scoped baseline — widen the scope, or pass --force")
	}
	setTS := o.ScopeSetTS
	if setTS == 0 {
		setTS = now()
	}
	rec["scope"] = scopeMap
	rec["scope_set_ts"] = int(setTS)
	rec["baseline"] = baselineMap(base.hits, base.total, base.rate)
	if err := save(stateDir, records); err != nil {
		return err
	}
	fmt.Fprintln(w, pyDumps(rec, 2))
	return nil
}

// baselineChains is the eligible before-window a writer freezes its baseline over: the
// chains starting before cutoff that the scope admits. It runs the same evidence pass
// ScoreRecord runs, read against the clock at, so the stored baseline and the scored
// one describe one set of chains. A chain with no adjudication is not evidence of the
// class's absence, so it belongs in neither.
func baselineChains(chains []Chain, scope *Scope, cutoff, at float64) []Chain {
	before, _ := SplitChains(chains, cutoff)
	return CoverEvidence(before, scope, at).EligibleChains()
}

// baselineSessions is the session engine's baseline window: the sessions wholly before
// cutoff that the scope admits. A session spanning cutoff belongs to neither window.
func baselineSessions(units []SessionUnit, scope *Scope, cutoff float64) []SessionUnit {
	before, _, _ := SplitSessions(units, cutoff)
	return SessionBaselineWindow(before, scope)
}

// writerTarget is what a writer counts its baseline with: the corpus, the class regex,
// the verdict the chain engine counts, the signal kind the session engine counts, and
// the frozen scope.
type writerTarget struct {
	spec       CorpusSpec
	pattern    *regexp.Regexp
	kindFilter string
	signalKind string
	scopeMap   map[string]any
	scope      *Scope
}

// resolveWriterTarget validates the corpus, regex, signal kind and scope flags that
// Register and Reject share, in the order both refuse them.
func resolveWriterTarget(corpus, match, kind, signalKind string, scopeAll bool,
	scopeHarness []string, scopeRepo, scopeRepoRoot, scopePath string) (writerTarget, error) {
	spec, err := writableCorpus(corpus)
	if err != nil {
		return writerTarget{}, err
	}
	kindFilter := "accepted"
	if kind == "false-positive" {
		kindFilter = "rejected"
	}
	pattern, err := compilePattern(match)
	if err != nil {
		return writerTarget{}, fmt.Errorf("bad regex %s: %w", pyRepr(match), err)
	}
	if err := spec.ValidateSignalKind(signalKind); err != nil {
		return writerTarget{}, err
	}
	scopeMap, scope, err := resolveScope(spec, scopeAll, scopeHarness, scopeRepo, scopeRepoRoot, scopePath)
	if err != nil {
		return writerTarget{}, err
	}
	if signalKind == "" {
		signalKind = SignalAny
	}
	return writerTarget{spec: spec, pattern: pattern, kindFilter: kindFilter,
		signalKind: signalKind, scopeMap: scopeMap, scope: scope}, nil
}

// resolveScope builds the scope the flags name, refuses a predicate the corpus's unit
// cannot decide, and parses it.
func resolveScope(spec CorpusSpec, all bool, harness []string, repo, repoRoot, path string) (map[string]any, *Scope, error) {
	scopeMap, err := BuildScope(all, harness, repo, repoRoot, path)
	if err != nil {
		return nil, nil, err
	}
	if err := spec.ValidateScope(scopeMap); err != nil {
		return nil, nil, err
	}
	scope, _, err := ParseScope(Record{"scope": scopeMap})
	if err != nil {
		return nil, nil, err
	}
	return scopeMap, scope, nil
}

// frozenBaseline is the incidence a writer stores and the instrument that counted it.
// chains is the ledger's chains for the chain engine and nil for the session engine.
type frozenBaseline struct {
	hits, total int
	rate        float64
	instrument  any
	chains      []Chain
}

// writerBaseline counts the target's regex over the window before cutoff that its
// scope admits, through baselineChains or baselineSessions. Only the chain engine
// reads the ledger.
func writerBaseline(stateDir, ledger string, t writerTarget, sessions []SessionUnit, cutoff float64) (frozenBaseline, error) {
	if t.spec.Engine == EngineSession {
		hits, total, rate := SessionIncidence(baselineSessions(sessions, t.scope, cutoff), t.pattern, t.signalKind)
		return frozenBaseline{hits: hits, total: total, rate: rate,
			instrument: sessionInstrumentValue(sessions)}, nil
	}
	rows, err := LoadRows(stateDir, ledger)
	if err != nil {
		return frozenBaseline{}, err
	}
	chains := BuildChains(rows, false)
	hits, total, rate := Incidence(baselineChains(chains, t.scope, cutoff, float64(now())), t.pattern, t.kindFilter)
	return frozenBaseline{hits: hits, total: total, rate: rate,
		instrument: instrumentValue(chains), chains: chains}, nil
}

// baselineMap is the stored baseline in the one candidate-record contract. Its
// count key is the corpus-neutral "units", not "chains": the record no longer
// assumes a chain corpus. The field is write-only today — nothing reads the stored
// baseline count, so legacy rows still keyed "chains" break no reader; a reader
// built later tolerates both keys at its own call site.
func baselineMap(hits, total int, rate float64) map[string]any {
	return map[string]any{"hits": hits, "units": total, "rate": pyFloat(round4(rate))}
}

// instrumentValue is the current reviewer, or nil so the record stores null on an
// unstamped log.
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

// Retire marks an intervention reworked or removed.
func Retire(w io.Writer, stateDir, id, reason, status string) error {
	release, err := lockInventory(stateDir)
	if err != nil {
		return err
	}
	defer release()
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
	if err := save(stateDir, records); err != nil {
		return err
	}
	if err := forgetRead(stateDir, hit); err != nil {
		return err
	}
	fmt.Fprintln(w, pyDumps(hit, 2))
	return nil
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
