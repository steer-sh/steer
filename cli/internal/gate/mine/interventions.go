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

	"github.com/steer-sh/steer-cli/internal/checks"
	"github.com/steer-sh/steer-cli/internal/gate"
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

// OverlapBlock: a candidate sharing this fraction of its chains with a rejected class
// is that class again under a new name, so register stops rather than warns.
const OverlapBlock = 0.5

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
	stampRepo(facts, "hook", scope)
	return facts, nil
}

// stampRepo records which repository a project-scope entry was read from, as
// <prefix>_repo. A project store is resolved from the working directory, so the
// scope alone cannot say which checkout's store a record names.
func stampRepo(facts map[string]any, prefix, scope string) {
	if scope != steerpath.ProjectScope {
		return
	}
	root, err := checks.RepoRootOf(".")
	if err != nil {
		return
	}
	if identity, err := checks.RepoIdentity(root); err == nil {
		facts[prefix+"_repo"] = identity
	}
}

// SteerRuleFacts freezes what an instruction-tier record's artifact says. It is the
// instruction mirror of SteerHookFacts and resolves the scope the same way: a named
// scope reads that store alone, an empty scope reads both and refuses an id in each.
//
// The digest is recomputed over the stored text rather than read from the entry's
// own rule_hash, so a hand-edited manifest cannot make a record name text the store
// does not hold.
func SteerRuleFacts(ruleID, scope string) (map[string]any, error) {
	return textEntryFacts(ruleTextEntry, ruleID, scope)
}

// SteerAddendumFacts freezes what a review-tier record's addendum entry says, under
// SteerRuleFacts' scope resolution and recomputed digest.
func SteerAddendumFacts(id, scope string) (map[string]any, error) {
	return textEntryFacts(addendumTextEntry, id, scope)
}

// RequireAddendumAtHead refuses a project addendum entry whose text the commit gate
// does not read yet. The gate reads project entries from the repository's HEAD, so
// an entry only on disk would put reviews that never saw it in the after-window.
func RequireAddendumAtHead(id, text string) error {
	want := normalizeRuleText(text)
	for _, e := range gate.LoadAddendum(gate.Toplevel(".")).Entries {
		if e.Scope != steerpath.ProjectScope || e.ID != id {
			continue
		}
		if e.Text == want {
			return nil
		}
		return fmt.Errorf("project addendum entry %s differs from HEAD, and the commit gate "+
			"reads the committed text. Commit the project steer-manifest.json, then register", pyRepr(id))
	}
	return fmt.Errorf("project addendum entry %s is not at HEAD, so the commit gate does not "+
		"read it yet. Commit the project steer-manifest.json, then register", pyRepr(id))
}

// textEntry names one manifest map of text entries and the facts a record stamps
// from it: <prefix>, <prefix>_hash and <prefix>_scope.
type textEntry struct {
	key, prefix, label, scopeFlag string
}

var (
	ruleTextEntry     = textEntry{key: "rules", prefix: "rule", label: "rule", scopeFlag: "--rule-scope"}
	addendumTextEntry = textEntry{key: "review_addendum", prefix: "addendum",
		label: "addendum entry", scopeFlag: "--addendum-scope"}
)

func textEntryFacts(kind textEntry, id, scope string) (map[string]any, error) {
	scopes := hookScopes
	if scope != "" {
		if scope != steerpath.ProjectScope && scope != steerpath.GlobalScope {
			return nil, fmt.Errorf("invalid %s scope %s: expected %q or %q",
				kind.label, pyRepr(scope), steerpath.ProjectScope, steerpath.GlobalScope)
		}
		scopes = []string{scope}
	}
	var found []string
	facts := map[string]any{}
	for _, s := range scopes {
		f, err := textEntryFactsInScope(kind, id, s)
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
		return nil, fmt.Errorf("%s %s is in the %s and %s stores; name one with %s",
			kind.label, pyRepr(id), found[0], found[1], kind.scopeFlag)
	}
	if len(found) == 0 {
		return nil, nil
	}
	return facts, nil
}

// textEntryFactsInScope reads one store's entry, through hookstore.Resolve so a
// migrated machine is read at the directory every other reader resolves.
func textEntryFactsInScope(kind textEntry, id, scope string) (map[string]any, error) {
	dir, err := hookstore.Resolve(scope)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(dir, hookstore.ManifestName))
	if os.IsNotExist(err) {
		return nil, nil // a scope with no store holds no entry; that is not a failure
	}
	if err != nil {
		return nil, fmt.Errorf("reading the %s guard manifest: %w", scope, err)
	}
	var manifest map[string]json.RawMessage
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("parsing the %s guard manifest: %w", scope, err)
	}
	var entries map[string]struct {
		Text string `json:"text"`
	}
	if raw, ok := manifest[kind.key]; ok {
		if err := json.Unmarshal(raw, &entries); err != nil {
			return nil, fmt.Errorf("parsing the %s guard manifest: %w", scope, err)
		}
	}
	entry, ok := entries[id]
	if !ok {
		return nil, nil
	}
	sum := sha256.Sum256([]byte(normalizeRuleText(entry.Text)))
	facts := map[string]any{
		kind.prefix:            entry.Text,
		kind.prefix + "_hash":  hex.EncodeToString(sum[:]),
		kind.prefix + "_scope": scope,
	}
	stampRepo(facts, kind.prefix, scope)
	return facts, nil
}

// NormalizeEntryText is the form a rule or addendum digest is taken over.
func NormalizeEntryText(text string) string { return normalizeRuleText(text) }

// normalizeRuleText is claude.NormalizeRule's pre-image, repeated here because
// nothing under gate may import an adapter. hookfacts_test.go pins the two together.
func normalizeRuleText(text string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
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

// ScoreRecord recomputes the field counts and observation contract for a review-chain
// intervention. The scorer reports what the corpus recorded; it does not classify an
// efficacy result.
//
// It takes the whole EvidenceSet, as ScoreSessionRecord does, and reads the chain
// corpus plus the guard audit stream: a guard mined from review findings still
// writes audit rows, and its observation reports them.
func ScoreRecord(record Record, ev EvidenceSet) (map[string]any, error) {
	if err := requireChainCorpus(record); err != nil {
		return nil, err
	}
	chains := ev.Chains
	pattern, err := compilePattern(str(record["match"]))
	if err != nil {
		return nil, err
	}
	kf := RecordKindFilter(record)
	scope, _, err := ParseScope(record)
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
	afterInstruments := InstrumentsIn(after)

	beforeHosts, afterHosts := HostsIn(before), HostsIn(after)

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
		"instrument":        record["instrument"],
		"after_instruments": emptyIfNil(afterInstruments),
		"before_hosts":      emptyIfNil(beforeHosts),
		"after_hosts":       emptyIfNil(afterHosts),
		"observation": chainObservation(record, before, after, beforeCov, afterCov,
			pattern, kf, ev, afterStart, clock),
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
	// CheckRuns is this machine's live check execution stream; CheckRepos holds
	// each repository with any check evidence here.
	CheckRuns  []CheckRun
	CheckRepos map[string]bool
}

// ScoreWith scores one record through the engine its corpus names.
func ScoreWith(record Record, ev EvidenceSet) (map[string]any, error) {
	if err := requireScorable(record); err != nil {
		return nil, err
	}
	switch EngineOf(record) {
	case EngineSession:
		return ScoreSessionRecord(record, ev)
	default:
		return ScoreRecord(record, ev)
	}
}

// requiredAfterValue is null when no clean window of any size could reach the effect
// target, which an unmeasurably loose baseline causes.
func coverageMap(c Coverage) map[string]any {
	return map[string]any{
		"total":            c.Total,
		"eligible":         c.Eligible,
		"ineligible":       c.Ineligible,
		"unknown":          c.Unknown,
		"unknown_scope":    c.UnknownScope,
		"unknown_evidence": c.UnknownEvidence,
		"pending":          c.Pending,
		"ledger_missed":    c.LedgerMissed,
		"ledger_overdue":   c.LedgerOverdue,
	}
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

// getInt reads a numeric record field. Records round-trip through JSON with
// UseNumber, so a stored int reads back as json.Number rather than int.
func getInt(record Record, key string, fallback int) int {
	switch v := record[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case json.Number:
		if n, err := v.Int64(); err == nil {
			return int(n)
		}
	}
	return fallback
}

func emptyIfNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
