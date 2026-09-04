package mine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/steer-sh/steer-cli/internal/hookstore"
	"github.com/steer-sh/steer-cli/internal/steerpath"
)

// Record is one intervention-log entry. It is a map so unmodeled keys survive a
// round-trip and numbers keep their original text, the same reasoning gate.Row records.
type Record = map[string]any

// refusalStatuses are the record statuses that installed no gate.
var refusalStatuses = map[string]bool{"rejected": true, "reopened": true}

// InterventionsPath is the log's location: inside the state directory, tracked while the
// adjudication shards beside it are gitignored.
func InterventionsPath(stateDir string) string {
	return filepath.Join(stateDir, "interventions.jsonl")
}

// Load reads the intervention log, skipping malformed lines.
func Load(stateDir string) ([]Record, error) {
	data, err := readFileIfExists(InterventionsPath(stateDir))
	if err != nil || data == nil {
		return nil, err
	}
	var out []Record
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		dec := json.NewDecoder(strings.NewReader(line))
		dec.UseNumber()
		var m map[string]any
		if err := dec.Decode(&m); err != nil {
			continue
		}
		out = append(out, m)
	}
	return out, nil
}

// Save writes the log atomically: tmp file, then rename.
func Save(stateDir string, records []Record) error {
	path := InterventionsPath(stateDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var b strings.Builder
	for _, r := range records {
		b.WriteString(pyDumps(r, 0))
		b.WriteByte('\n')
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// RecordKindFilter is the verdict filter a record's regex was authored against.
func RecordKindFilter(record Record) string {
	kind := "accepted"
	if k := str(record["kind"]); k != "" {
		kind = k
	}
	switch kind {
	case "defect":
		return "accepted"
	case "false-positive":
		return "rejected"
	default:
		return kind
	}
}

// SplitTS is the timestamp a record's before/after windows split on.
func SplitTS(record Record) float64 {
	if truthy(record["installed_ts"]) {
		return numFloat(record["installed_ts"])
	}
	if truthy(record["rejected_ts"]) {
		return numFloat(record["rejected_ts"])
	}
	return 0
}

// StandsRejected reports a refusal still in force — one no later install overturned.
func StandsRejected(record Record) bool { return str(record["status"]) == "rejected" }

// IsRejected reports a record that installed nothing — a refusal, live or overturned.
func IsRejected(record Record) bool { return refusalStatuses[str(record["status"])] }

// Overlap counts the chains a candidate regex and a recorded class's regex both match.
// The prior regex is read under the verdict filter it was authored against.
func Overlap(record Record, chains []Chain, pattern *regexp.Regexp, kind string) (shared, total int, err error) {
	prior, err := compilePattern(str(record["match"]))
	if err != nil {
		return 0, 0, err
	}
	priorKind := RecordKindFilter(record)
	var candidate []Chain
	for _, ch := range chains {
		if len(ChainHits(ch, pattern, kind)) > 0 {
			candidate = append(candidate, ch)
		}
	}
	for _, ch := range candidate {
		if len(ChainHits(ch, prior, priorKind)) > 0 {
			shared++
		}
	}
	return shared, len(candidate), nil
}

// Decision pairs a record with its overlap against a candidate.
type Decision struct {
	Record Record
	Shared int
	Total  int
	Ratio  float64
}

// OverlappingDecisions lists recorded classes sharing chains with a candidate, most
// overlapping first. A record authored against the other verdict filter is skipped.
func OverlappingDecisions(records []Record, chains []Chain, pattern *regexp.Regexp, kind string) ([]Decision, error) {
	var out []Decision
	for _, record := range records {
		if kind != "both" && RecordKindFilter(record) != kind {
			continue
		}
		shared, total, err := Overlap(record, chains, pattern, kind)
		if err != nil {
			return nil, err
		}
		if shared > 0 {
			ratio := 0.0
			if total > 0 {
				ratio = float64(shared) / float64(total)
			}
			out = append(out, Decision{record, shared, total, ratio})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Ratio > out[j].Ratio })
	return out, nil
}

var (
	slugNonAlnum = regexp.MustCompile(`[^a-z0-9]+`)
	slugDashes   = regexp.MustCompile(`-+`)
)

// Slugify mirrors mine.py's slugify: lowercase, non-alphanumerics to dashes, collapse.
func Slugify(name string) string {
	s := slugNonAlnum.ReplaceAllString(strings.ToLower(name), "-")
	s = slugDashes.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

// UniqueID returns base, or base-2, base-3… when base is already taken.
func UniqueID(base string, existing []Record) string {
	ids := map[string]bool{}
	for _, r := range existing {
		ids[str(r["id"])] = true
	}
	if !ids[base] {
		return base
	}
	for n := 2; ; n++ {
		cand := base + "-" + strconv.Itoa(n)
		if !ids[cand] {
			return cand
		}
	}
}

// hookScopes are the guard stores a lookup searches, in the order it searches them.
var hookScopes = []string{steerpath.ProjectScope, steerpath.GlobalScope}

// SteerHookFacts reads a steer hook's manifest entry: the rule text, sha256(rule),
// the store that owns it, and the identity of the implementation that ran. The rule
// hash names the policy text; hook_version and hook_digest name the code, and a guard
// repaired without a rule change moves only those.
//
// The store is scoped. A named scope reads that store alone; an empty scope reads
// both and refuses an id that exists in each, because the two entries can carry
// different implementations and picking one would freeze the wrong treatment.
// A scope holding no store, or a store whose manifest does not name the id, is
// (nil, nil): the caller records the id and warns. An unreadable or unparseable
// manifest is an error instead. Reading it as absence would report a corrupt store
// as an unregistered hook, and would let one unreadable store resolve an ambiguity
// the other half of which is real.
func SteerHookFacts(hookID, scope string) (map[string]any, error) {
	scopes := hookScopes
	if scope != "" {
		if scope != steerpath.ProjectScope && scope != steerpath.GlobalScope {
			return nil, fmt.Errorf("invalid hook scope %s: expected %q or %q",
				pyRepr(scope), steerpath.ProjectScope, steerpath.GlobalScope)
		}
		scopes = []string{scope}
	}
	var found []string
	facts := map[string]any{}
	for _, s := range scopes {
		f, err := hookFactsInScope(hookID, s)
		if err != nil {
			return nil, err
		}
		if f == nil {
			continue
		}
		found = append(found, s)
		facts = f
	}
	if len(found) > 1 {
		return nil, fmt.Errorf("hook %s is in the %s and %s stores; name one with --hook-scope",
			pyRepr(hookID), found[0], found[1])
	}
	if len(found) == 0 {
		return nil, nil
	}
	return facts, nil
}

// hookFactsInScope reads one store's manifest entry, through hookstore.Resolve so a
// migrated machine is read at the directory every other reader resolves.
func hookFactsInScope(hookID, scope string) (map[string]any, error) {
	dir, err := hookstore.Resolve(scope)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(dir, hookstore.ManifestName))
	if os.IsNotExist(err) {
		return nil, nil // a scope with no store holds no hook; that is not a failure
	}
	if err != nil {
		return nil, fmt.Errorf("reading the %s guard manifest: %w", scope, err)
	}
	var manifest struct {
		Hooks map[string]struct {
			Rule        string `json:"rule"`
			RuleHash    string `json:"rule_hash"`
			File        string `json:"file"`
			HookVersion string `json:"hook_version"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("parsing the %s guard manifest: %w", scope, err)
	}
	entry, ok := manifest.Hooks[hookID]
	if !ok {
		return nil, nil
	}
	ruleHash := entry.RuleHash
	if ruleHash == "" {
		sum := sha256.Sum256([]byte(entry.Rule))
		ruleHash = hex.EncodeToString(sum[:])
	}
	facts := map[string]any{
		"rule":       entry.Rule,
		"rule_hash":  ruleHash,
		"hook_file":  entry.File,
		"hook_scope": scope,
	}
	if entry.HookVersion != "" {
		facts["hook_version"] = entry.HookVersion
	}
	if digest := fileDigest(filepath.Join(dir, entry.File)); digest != "" {
		facts["hook_digest"] = digest
	}
	return facts, nil
}

// fileDigest is sha256 over the installed guard, truncated to 16 hex characters. An
// unreadable file yields no digest rather than a digest of nothing.
func fileDigest(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])[:16]
}

// ScoreRecord recomputes an intervention's before/after incidence and its verdict,
// producing the JSON shape mine.py emits (rate and p as Python floats; empty lists as
// [], not null; instrument null when the record predates the field).
func ScoreRecord(record Record, chains []Chain, minAfter int) (map[string]any, error) {
	if err := requireChainCorpus(record); err != nil {
		return nil, err
	}
	pattern, err := compilePattern(str(record["match"]))
	if err != nil {
		return nil, err
	}
	kf := RecordKindFilter(record)
	scope, hasScope, err := ParseScope(record)
	if err != nil {
		return nil, err
	}
	// The after-window opens at scope_set_ts when a legacy record was migrated later, so
	// inspecting its historical post-install results cannot influence the new denominator.
	splitTS := SplitTS(record)
	afterStart := splitTS
	if v := numFloat(record["scope_set_ts"]); v != 0 {
		afterStart = v
	}
	beforeWindow, _ := SplitChains(chains, splitTS)
	_, afterWindow := SplitChains(chains, afterStart)
	clock := float64(now())
	beforeCov := CoverEvidence(beforeWindow, scope, clock)
	afterCov := CoverEvidence(afterWindow, scope, clock)
	before, after := beforeCov.EligibleChains(), afterCov.EligibleChains()

	bHits, bTotal, bRate := Incidence(before, pattern, kf)
	aHits, aTotal, aRate := Incidence(after, pattern, kf)
	m := Measure(bHits, bTotal, bRate, HitSequence(after, pattern, kf), aRate)
	p := FisherOneSided(bHits, bTotal-bHits, aHits, aTotal-aHits)

	authored := str(record["instrument"])
	afterInstruments := InstrumentsIn(after)
	// The reported flag and the confound read one computation, so a drift the
	// confound grades by dimension can never be a drift the flag denies.
	driftConfound, _ := instrumentDrift(before, after, authored)
	drift := authored != "" && driftConfound != nil
	hard, soft := Confounds(before, after, authored)

	// A record predating scopes has no frozen denominator, so its windows describe
	// different opportunity sets and no verdict is available from them. Withholding
	// outranks the range: legacy-unscoped, then missing evidence, then a confound.
	var label string
	switch {
	case !hasScope:
		label = "legacy-unscoped"
	case evidenceVerdict(beforeCov, afterCov) != "":
		label = evidenceVerdict(beforeCov, afterCov)
	case len(hard) > 0:
		label = "confounded"
	case IsRejected(record):
		label = TrendFor(m, aTotal, minAfter)
	default:
		label = VerdictFor(m, aTotal, minAfter)
	}

	beforeHosts, afterHosts := HostsIn(before), HostsIn(after)
	hostDrift := len(beforeHosts) > 0 && len(afterHosts) > 0 && !sameStringSet(beforeHosts, afterHosts)

	return map[string]any{
		"id":                str(record["id"]),
		"class":             str(record["class"]),
		"tier":              str(record["tier"]),
		"target":            getString(record, "target", ""),
		"hook_id":           getString(record, "hook_id", ""),
		"status":            getString(record, "status", "active"),
		"installed":         installedLabel(record),
		"reason":            getString(record, "reason", ""),
		"reopen":            getString(record, "reopen", ""),
		"before":            incidenceMap(bHits, bTotal, bRate),
		"after":             incidenceMap(aHits, aTotal, aRate),
		"before_coverage":   coverageMap(beforeCov),
		"after_coverage":    coverageMap(afterCov),
		"scope":             record["scope"],
		"effect":            effectMap(m.Effect),
		"required_after":    requiredAfterValue(m),
		"confounds":         confoundList(hard),
		"composition":       confoundList(soft),
		"p":                 pyFloat(round4(p)),
		"verdict":           label,
		"instrument":        record["instrument"],
		"instrument_drift":  drift,
		"after_instruments": emptyIfNil(afterInstruments),
		"before_hosts":      emptyIfNil(beforeHosts),
		"after_hosts":       emptyIfNil(afterHosts),
		"host_mix_drift":    hostDrift,
	}, nil
}

// EvidenceSet is every corpus a scoring pass may read, gathered once by the caller. Each
// engine takes the whole struct and reads the part its corpus is counted over.
type EvidenceSet struct {
	// Chains is the commit-review corpus, segmented.
	Chains []Chain
	// Sessions is the transcript-friction corpus: normalized root-session snapshots.
	Sessions []SessionUnit
	// Firings is the guard audit stream; AuditPresent reports that a store was
	// readable, so a silent guard is told apart from absent evidence.
	Firings      []Firing
	AuditPresent bool
}

// ScoreWith scores one record through the engine its corpus names.
func ScoreWith(record Record, ev EvidenceSet, minAfter int) (map[string]any, error) {
	if err := requireScorable(record); err != nil {
		return nil, err
	}
	switch EngineOf(record) {
	case EngineSession:
		return ScoreSessionRecord(record, ev, minAfter)
	default:
		return ScoreRecord(record, ev.Chains, minAfter)
	}
}

// requiredAfterValue is null when no clean window of any size could reach the effect
// target, which an unmeasurably loose baseline causes.
func requiredAfterValue(m Measurement) any {
	if !m.HasBaseline || m.Required == 0 {
		return nil
	}
	return m.Required
}

func coverageMap(c Coverage) map[string]any {
	return map[string]any{
		"total":            c.Total,
		"eligible":         c.Eligible,
		"ineligible":       c.Ineligible,
		"unknown":          c.Unknown,
		"unknown_scope":    c.UnknownScope,
		"unknown_evidence": c.UnknownEvidence,
		"pending":          c.Pending,
	}
}

// evidenceVerdict withholds when a window has no eligible chain left and unknown chains
// are why. It reads the causes in order of permanence: a closed miss and an unknown
// scope are both permanent and both incomplete, while a window emptied only by ledgers
// still inside the grace window becomes measurable on its own and is collecting.
// It returns "" to fall through to the range ladder.
func evidenceVerdict(before, after Coverage) string {
	for _, c := range []Coverage{before, after} {
		if c.Eligible > 0 || c.Unknown == 0 {
			continue
		}
		switch {
		case c.UnknownEvidence > c.Pending: // a ledger the resolver closed as gone
			return "evidence-incomplete"
		case c.UnknownScope > 0: // metadata a scope needs and no row carried
			return "evidence-incomplete"
		default:
			return "collecting"
		}
	}
	return ""
}

func incidenceMap(hits, total int, rate float64) map[string]any {
	return map[string]any{"hits": hits, "chains": total, "rate": pyFloat(round4(rate))}
}

func installedLabel(record Record) string {
	if d := str(record["installed_date"]); d != "" {
		return d
	}
	return getString(record, "rejected_date", "?")
}

func getString(record Record, key, fallback string) string {
	if v, ok := record[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return fallback
}

func sameStringSet(a, b []string) bool {
	set := map[string]bool{}
	for _, x := range a {
		set[x] = true
	}
	other := map[string]bool{}
	for _, x := range b {
		other[x] = true
	}
	if len(set) != len(other) {
		return false
	}
	for x := range set {
		if !other[x] {
			return false
		}
	}
	return true
}

func emptyIfNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
