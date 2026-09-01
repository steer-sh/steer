// Package status composes a read-only snapshot of the steer feedback loop, keyed
// on the failure class and its intervention's verdict rather than the hook.
// Collect reads the scored intervention inventory and review chains (mine), the
// attention subset (the pure cadence.Refresh), and the arming posture (claude).
// It never writes state. Terminal, JSON, HTML and TUI all render this one struct.
package status

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/steer-sh/steer-cli/internal/cadence"
	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/codex"
	"github.com/steer-sh/steer-cli/internal/denials"
	"github.com/steer-sh/steer-cli/internal/gate"
	"github.com/steer-sh/steer-cli/internal/gate/mine"
	"github.com/steer-sh/steer-cli/internal/gate/resolve"
	"github.com/steer-sh/steer-cli/internal/guard"
	"github.com/steer-sh/steer-cli/internal/harness"
	"github.com/steer-sh/steer-cli/internal/presets"
	"github.com/steer-sh/steer-cli/internal/sessions"
)

// StatusVersion is the schema version of the emitted snapshot. It mirrors
// report.DigestVersion: --json is a stable agent contract, so bump this whenever
// the shape or semantics of SystemStatus change.
const StatusVersion = 5

// recentChainLimit caps the chain list a snapshot carries.
const recentChainLimit = 20

// Options are the inputs to a collection. State dirs are passed in so a test can
// point them at fixtures; the command fills them from the gate config.
type Options struct {
	GateStateDir string // commit-review state: interventions.jsonl + ledger shards
	Ledger       string // single-ledger override, or "" for the shard glob
	Config       gate.Config
	WindowDays   int    // transcript look-back for the attention refresh
	AllProjects  bool   // scan every project, not just the cwd
	MinAfter     int    // optional floor on eligible after-units; 0 = no floor
	Scope        string // active scope for the config-state read (default global)
	// Evidence is every corpus a scoring pass reads. A caller leaving it zero gets
	// the ledger alone, and every session-unit record reports missing evidence.
	Evidence mine.EvidenceSet
}

// ScoredIntervention is one live intervention with its recomputed verdict. It is
// the full inventory row — effective classes included — so an agent can see an
// already-decided class and skip re-proposing it. Before/After reuse
// cadence.Incidence, so one payload keys chain counts one way: units.
type ScoredIntervention struct {
	ID      string            `json:"id"`
	Class   string            `json:"class"`
	Match   string            `json:"match,omitempty"`
	Tier    string            `json:"tier"`
	Target  string            `json:"target"`
	Status  string            `json:"status"`
	HookID  string            `json:"hook_id,omitempty"`
	Verdict string            `json:"verdict"`
	Before  cadence.Incidence `json:"before"`
	After   cadence.Incidence `json:"after"`
	// PValue is the Fisher one-sided p from the score, 0 when unscored.
	PValue float64 `json:"p_value,omitempty"`
	// InstrumentDrift flags a measurement window spanning two reviewer prompts.
	// The verdict stands; this flag rides beside it, never rewrites it.
	InstrumentDrift bool `json:"instrument_drift,omitempty"`
	// FiringsApplicable is false for check and instruction tiers, which produce
	// no audit-log firings by construction, so a reader shows n/a, not zero.
	FiringsApplicable bool `json:"firings_applicable"`
	// Reason, Reopen and RejectedTS explain a refused class: why it was refused
	// and the condition that reopens it.
	Reason     string  `json:"reason,omitempty"`
	Reopen     string  `json:"reopen,omitempty"`
	RejectedTS float64 `json:"rejected_ts,omitempty"`
	// Scope is the frozen eligibility rule, null on a legacy record.
	Scope *cadence.Scope `json:"scope"`
	// BeforeCoverage and AfterCoverage account for each window under that scope.
	BeforeCoverage cadence.Coverage `json:"before_coverage"`
	AfterCoverage  cadence.Coverage `json:"after_coverage"`
	// Effect is the relative-risk range the verdict reads; RequiredAfter is the
	// clean eligible after-window that could still reach the effect target.
	Effect        cadence.Effect `json:"effect"`
	RequiredAfter *int           `json:"required_after"`
	// Confounds withhold the verdict; Composition only warns beside it.
	Confounds   []mine.Confound `json:"confounds"`
	Composition []mine.Confound `json:"composition"`
	// Corpus and Unit name the evidence the class is counted over and what one
	// denominator entry is, so a reader never compares a chain rate to a session rate.
	Corpus string `json:"corpus"`
	Unit   string `json:"unit"`
	// SignalKind is the classifier kind a session-unit class is counted over.
	SignalKind string `json:"signal_kind,omitempty"`
	// Attribution is the guard's audit join over the after-window.
	Attribution cadence.Attribution `json:"attribution"`
}

// HookInfo is one registered managed hook in a scope.
type HookInfo struct {
	Name   string `json:"name"`
	Scope  string `json:"scope"`
	Source string `json:"source"`
	Rule   string `json:"rule,omitempty"`
}

// PresetInfo is one catalog preset with its enable and drift state in a scope.
type PresetInfo struct {
	Name        string `json:"name"`
	Bundle      string `json:"bundle"`
	Description string `json:"description"`
	Enabled     bool   `json:"enabled"`
	Drifted     bool   `json:"drifted"`
}

// TeamState summarizes the repo-committed team lockfile, when one is present.
type TeamState struct {
	Present        bool   `json:"present"`
	Path           string `json:"path,omitempty"`
	HooksLocked    int    `json:"hooks_locked"`
	CatalogVersion string `json:"catalog_version,omitempty"`
}

// CorpusRow is one corpus's presence, size and path on this machine. It carries
// the path cadence.CorpusStatus omits, so a reader knows where to look.
type CorpusRow struct {
	Corpus  string `json:"corpus"`
	Present bool   `json:"present"`
	Size    int    `json:"size"`
	Path    string `json:"path,omitempty"`
}

// ChainSummary is one commit attempt's review outcome, without the finding text.
type ChainSummary struct {
	Host     string  `json:"host"`
	StartTS  float64 `json:"start_ts"`
	Rounds   int     `json:"rounds"`
	Files    int     `json:"files"`
	Findings int     `json:"findings"`
	Accepted int     `json:"accepted"`
	Rejected int     `json:"rejected"`
	Sessions int     `json:"sessions"`
	Terminal string  `json:"terminal,omitempty"`
}

// VerdictSummary counts live interventions by verdict. The keys are the verdict
// vocabulary VerdictFor and TrendFor emit, plus "unscored" for session-unit
// records the chain engine cannot score.
type VerdictSummary struct {
	Total  int            `json:"total"`
	Counts map[string]int `json:"counts"`
}

// Trust states a harness reports for a registered hook. Steer never claims to have
// verified a harness's internal hash: a record's presence is all it can read.
const (
	TrustNotApplicable = "not-applicable"
	TrustUnknown       = "unknown"
	TrustRecordPresent = "record-present-unverified"
	TrustMissing       = "missing"
)

// ScopeArming is one harness and scope's registration state for a hook.
type ScopeArming struct {
	Harness    string `json:"harness"`
	Scope      string `json:"scope"`
	Registered bool   `json:"registered"`
	Effective  bool   `json:"effective"`
	Timeout    int    `json:"timeout,omitempty"`
	// Trust is the harness's posture on this registration. Registration alone does
	// not prove enforcement on a harness that reviews hook trust.
	Trust string `json:"trust"`
}

// Master runtime states. The stamp names the runtime spliced into a registered
// master; the comparison is against the running binary's own runtime digest.
const (
	MasterCurrent       = "current"
	MasterStale         = "stale"
	MasterUnknown       = "unknown"
	MasterNotRegistered = "not-registered"
)

// MasterRuntime is one harness and scope's registered master and the runtime it
// carries. It reads the REGISTERED file, never the one the store resolves to: a
// migrated machine holds a master at both paths, and reporting the healthy new
// file while the stale registered one runs is the exact failure the stamp exists
// to end.
type MasterRuntime struct {
	Harness string `json:"harness"`
	Scope   string `json:"scope"`
	Path    string `json:"path,omitempty"`
	// Runtime is the master's runtime digest, "" when the file carries no stamp.
	Runtime string `json:"runtime,omitempty"`
	// Version is the CLI that wrote it, "" when unstamped.
	Version string `json:"version,omitempty"`
	// Expected is the running binary's runtime digest.
	Expected string `json:"expected"`
	State    string `json:"state"`
}

// Stale reports whether this master needs a rebuild. An unstamped master is
// unknown rather than current: every store built before the stamp existed is
// unstamped, so treating absence as up to date would report exactly the machines
// that need the rebuild as healthy.
func (m MasterRuntime) Stale() bool { return m.State == MasterStale || m.State == MasterUnknown }

// Posture is the arming and instrument state — what is guarding, and with what.
type Posture struct {
	Instrument string        `json:"instrument"`
	Reviewer   string        `json:"reviewer"`
	Ledger     string        `json:"ledger"`
	CommitGate []ScopeArming `json:"commit_gate"`
	Denials    []ScopeArming `json:"denials"`
	// Resolver is the Stop-hook backfill arming per scope. Unresolved counts the
	// ledger rows still owing a transcript join — evidence the scorer withholds on.
	Resolver   []ScopeArming `json:"resolver"`
	Unresolved int           `json:"unresolved_rows"`
	// Masters is the runtime provenance of each registered guard master.
	Masters []MasterRuntime `json:"masters"`
}

// AttentionQueue is the subset of the loop needing action: the cadence queue
// minus its corpus sweep, which SystemStatus carries once at the top level.
type AttentionQueue struct {
	Interventions []cadence.Intervention `json:"interventions"`
	Unscored      []string               `json:"unscored"`
	Candidates    []cadence.Candidate    `json:"candidates"`
}

// HarnessTelemetry is one harness's share of the corpora, so a reader can tell a
// harness that contributed nothing from one that was never mined.
type HarnessTelemetry struct {
	Harness  string `json:"harness"`
	Sessions int    `json:"sessions"`
	Firings  int    `json:"firings"`
	Chains   int    `json:"chains"`
}

// Telemetry reports corpus sizes — cheap line and row counts, no model call.
type Telemetry struct {
	ReviewChains     int                `json:"review_chains"`
	DenialsRows      int                `json:"denials_rows"`
	AuditFirings     int                `json:"audit_firings"`
	SessionSnapshots int                `json:"session_snapshots"`
	Harnesses        []HarnessTelemetry `json:"harnesses"`
}

// SystemStatus is the one snapshot all render targets read. It is versioned so
// --json is a stable agent contract.
type SystemStatus struct {
	StatusVersion int                  `json:"status_version"`
	GeneratedAt   string               `json:"generated_at"`
	Posture       Posture              `json:"posture"`
	Interventions []ScoredIntervention `json:"interventions"`
	Summary       VerdictSummary       `json:"summary"`
	Chains        []ChainSummary       `json:"chains"`
	Attention     AttentionQueue       `json:"attention"`
	Telemetry     Telemetry            `json:"telemetry"`
	Corpora       []CorpusRow          `json:"corpora"`
	// Config state — what the dashboard panels mutate. Each reads fail-soft.
	Hooks   []HookInfo   `json:"hooks"`
	Presets []PresetInfo `json:"presets"`
	Team    TeamState    `json:"team"`
}

// Collect composes the snapshot. It never writes and every source fails soft: a
// missing ledger yields an empty section, never an error. GeneratedAt is the one
// non-deterministic field; a --json caller wanting byte stability clears it.
func Collect(opts Options) (*SystemStatus, error) {
	if opts.Scope == "" {
		opts.Scope = claude.GlobalScope
	}
	s := &SystemStatus{
		StatusVersion: StatusVersion,
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339),
		Interventions: []ScoredIntervention{},
		Chains:        []ChainSummary{},
		Corpora:       []CorpusRow{},
		Summary:       VerdictSummary{Counts: map[string]int{}},
		Hooks:         []HookInfo{},
		Presets:       []PresetInfo{},
	}

	// Full scored inventory + recent chains, read-only.
	records, _ := mine.Load(opts.GateStateDir)
	ev := opts.Evidence
	if ev.Chains == nil {
		rows, _ := mine.LoadRows(opts.GateStateDir, opts.Ledger)
		ev.Chains = mine.BuildChains(rows, false)
	}
	chains := ev.Chains

	s.Interventions = scoreInventory(records, ev, opts.MinAfter)
	s.Summary = summarize(s.Interventions)
	s.Chains = recentChains(chains, recentChainLimit)

	// Attention subset, from the pure orchestrator function — no side effects.
	q, err := cadence.Refresh(cadence.Options{
		GateStateDir: opts.GateStateDir,
		Ledger:       opts.Ledger,
		WindowDays:   opts.WindowDays,
		AllProjects:  opts.AllProjects,
		MinAfter:     opts.MinAfter,
		Evidence:     ev,
	})
	if err == nil && q != nil {
		s.Attention = AttentionQueue{
			Interventions: q.Interventions,
			Unscored:      q.Unscored,
			Candidates:    q.Candidates,
		}
		s.Corpora = corpusRows(q.Corpora, opts)
	} else {
		s.Attention = AttentionQueue{
			Interventions: []cadence.Intervention{},
			Unscored:      []string{},
			Candidates:    []cadence.Candidate{},
		}
	}

	s.Posture = collectPosture(opts.Config)
	s.Telemetry = telemetry(ev)
	s.Hooks = collectHooks(opts.Scope)
	s.Presets = collectPresets(opts.Scope)
	s.Team = collectTeam()
	return s, nil
}

// scoreInventory recomputes every live intervention's verdict. Reworked and
// removed records are dropped as history — the filter Refresh applies. A record
// from a corpus the chain engine cannot score is carried with verdict "unscored"
// rather than scored against the wrong corpus; the registry decides which.
func scoreInventory(records []mine.Record, ev mine.EvidenceSet, minAfter int) []ScoredIntervention {
	out := []ScoredIntervention{}
	for _, r := range records {
		st := str(r["status"])
		if st != "active" && st != "rejected" {
			continue
		}
		tier := str(r["tier"])
		if !mine.Scorable(r) {
			out = append(out, ScoredIntervention{
				ID:                str(r["id"]),
				Class:             str(r["class"]),
				Match:             str(r["match"]),
				Tier:              tier,
				Target:            str(r["target"]),
				Status:            st,
				HookID:            str(r["hook_id"]),
				Verdict:           "unscored",
				Corpus:            mine.CorpusOf(r),
				Unit:              mine.UnitOf(r),
				Confounds:         []mine.Confound{},
				Composition:       []mine.Confound{},
				FiringsApplicable: firingsApplicable(tier),
				Reason:            str(r["reason"]),
				Reopen:            str(r["reopen"]),
				RejectedTS:        numFloat(r["rejected_ts"]),
			})
			continue
		}
		scored, err := mine.ScoreWith(r, ev, minAfter)
		if err != nil {
			continue
		}
		scoredTier := str(scored["tier"])
		out = append(out, ScoredIntervention{
			ID:                str(scored["id"]),
			Class:             str(scored["class"]),
			Match:             str(r["match"]),
			Tier:              scoredTier,
			Target:            str(scored["target"]),
			Status:            str(scored["status"]),
			HookID:            str(scored["hook_id"]),
			Verdict:           str(scored["verdict"]),
			Before:            incidenceOf(scored["before"]),
			After:             incidenceOf(scored["after"]),
			PValue:            numFloat(scored["p"]),
			InstrumentDrift:   boolOf(scored["instrument_drift"]),
			FiringsApplicable: firingsApplicable(scoredTier),
			Reason:            str(scored["reason"]),
			Reopen:            str(scored["reopen"]),
			RejectedTS:        numFloat(r["rejected_ts"]),

			Scope:          cadence.ScopeOf(scored["scope"]),
			BeforeCoverage: cadence.CoverageOf(scored["before_coverage"]),
			AfterCoverage:  cadence.CoverageOf(scored["after_coverage"]),
			Effect:         cadence.EffectOf(scored["effect"]),
			RequiredAfter:  cadence.RequiredAfterOf(scored["required_after"]),
			Confounds:      cadence.ConfoundsOf(scored["confounds"]),
			Composition:    cadence.ConfoundsOf(scored["composition"]),
			Corpus:         mine.CorpusOf(r),
			Unit:           mine.UnitOf(r),
			SignalKind:     str(scored["signal_kind"]),
			Attribution:    cadence.AttributionOf(scored["attribution"]),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// summarize counts the inventory by verdict.
func summarize(items []ScoredIntervention) VerdictSummary {
	counts := map[string]int{}
	for _, it := range items {
		counts[it.Verdict]++
	}
	return VerdictSummary{Total: len(items), Counts: counts}
}

// recentChains returns the last limit chains by start time, newest last, with
// finding counts split by verdict.
func recentChains(chains []mine.Chain, limit int) []ChainSummary {
	start := 0
	if len(chains) > limit {
		start = len(chains) - limit
	}
	out := []ChainSummary{}
	for _, ch := range chains[start:] {
		accepted, rejected := 0, 0
		for _, f := range ch.Findings {
			switch f.Verdict {
			case "ACCEPTED":
				accepted++
			case "REJECTED":
				rejected++
			}
		}
		cs := ChainSummary{
			Host:     ch.Host,
			StartTS:  numFloat(ch.StartTS),
			Rounds:   ch.Rounds,
			Files:    ch.Files,
			Findings: len(ch.Findings),
			Accepted: accepted,
			Rejected: rejected,
			Sessions: len(ch.Sessions),
		}
		if ch.Terminal != nil {
			cs.Terminal = *ch.Terminal
		}
		out = append(out, cs)
	}
	return out
}

// collectPosture reads the arming state both scopes report for the commit gate
// and the denials recorder, plus the reviewer identity from the config.
func collectPosture(cfg gate.Config) Posture {
	p := Posture{
		Instrument: cfg.InstrumentID(),
		Reviewer:   cfg.ReviewerModel,
		Ledger:     cfg.LogFile(),
		CommitGate: []ScopeArming{},
		Denials:    []ScopeArming{},
		Resolver:   []ScopeArming{},
		Masters:    []MasterRuntime{},
		Unresolved: resolve.CountUnresolved(cfg),
	}
	cwd, _ := os.Getwd()
	claudeID := string(harness.ClaudeCode)
	codexID := string(harness.Codex)
	for _, scope := range []string{claude.ProjectScope, claude.GlobalScope} {
		g := claude.CommitGateHookArmed(scope)
		p.CommitGate = append(p.CommitGate, ScopeArming{
			Harness:    claudeID,
			Scope:      scope,
			Registered: g.Registered,
			Effective:  g.Effective(),
			Timeout:    g.Timeout,
			Trust:      TrustNotApplicable,
		})
		cg := codex.CommitGateArming(scope, cwd)
		p.CommitGate = append(p.CommitGate, ScopeArming{
			Harness:    codexID,
			Scope:      scope,
			Registered: cg.Registered,
			Effective:  cg.Effective(),
			Timeout:    cg.Timeout,
			Trust:      trustState(harness.Codex, cg.TrustStateKnown, cg.TrustRecordPresent),
		})
		// The denials recorder rides Claude Code's PermissionDenied event. Codex
		// declares no equivalent, so it gets no row rather than a false zero.
		d, err := claude.DenialsHookArming(scope)
		if err != nil {
			d = claude.DenialsArming{}
		}
		p.Denials = append(p.Denials, ScopeArming{
			Harness:    claudeID,
			Scope:      scope,
			Registered: d.Registered,
			Effective:  d.Registered && d.Matcher == claude.DenialsMatcher,
			Timeout:    d.Timeout,
			Trust:      TrustNotApplicable,
		})
		// The resolver rides a turn-end event, which takes no matcher: registered is
		// effective.
		armed, timeout := claude.BackfillHookArmed(scope)
		p.Resolver = append(p.Resolver, ScopeArming{
			Harness:    claudeID,
			Scope:      scope,
			Registered: armed,
			Effective:  armed,
			Timeout:    timeout,
			Trust:      TrustNotApplicable,
		})
		p.Masters = append(p.Masters, masterRuntimes(scope)...)
		codexArmed, codexTimeout := codex.ResolverArming(scope, cwd)
		p.Resolver = append(p.Resolver, ScopeArming{
			Harness:    codexID,
			Scope:      scope,
			Registered: codexArmed,
			Effective:  codexArmed,
			Timeout:    codexTimeout,
			Trust:      trustState(harness.Codex, cg.TrustStateKnown, cg.TrustRecordPresent),
		})
	}
	return p
}

// MasterRuntimes reports each harness's registered master in the named scopes.
// It is the cheap read — two configuration files and a header line each — for a
// caller that needs the runtime posture without a full Collect.
func MasterRuntimes(scopes ...string) []MasterRuntime {
	out := []MasterRuntime{}
	for _, scope := range scopes {
		out = append(out, masterRuntimes(scope)...)
	}
	return out
}

// guardRegistrars are the harnesses a guard master can be registered in. The
// store is shared, so the registration is per harness and so is its stamp.
func guardRegistrars() []harness.Registrar {
	return []harness.Registrar{claude.Registrar{}, codex.Registrar{}}
}

// masterRuntimes reads each harness's registered master in one scope and reports
// the runtime it carries. Every read fails soft: an unreadable configuration
// yields a not-registered row, never an error.
func masterRuntimes(scope string) []MasterRuntime {
	expected := guard.RuntimeDigest()
	out := make([]MasterRuntime, 0, len(guardRegistrars()))
	for _, registrar := range guardRegistrars() {
		row := MasterRuntime{
			Harness:  string(registrar.Harness().ID),
			Scope:    scope,
			Expected: expected,
			State:    MasterNotRegistered,
		}
		path, err := registrar.RegisteredMasterPath(scope)
		if err != nil || path == "" {
			out = append(out, row)
			continue
		}
		row.Path = path
		stamp, ok := guard.ReadStamp(path)
		if !ok {
			row.State = MasterUnknown
			out = append(out, row)
			continue
		}
		row.Runtime, row.Version = stamp.Runtime, stamp.Version
		row.State = MasterStale
		if stamp.Runtime == expected {
			row.State = MasterCurrent
		}
		out = append(out, row)
	}
	return out
}

// trustState reports what this machine can honestly say about a registration's trust.
// A harness that never reviews hook trust is not-applicable; one that does reports
// record presence, never a verified hash.
func trustState(id harness.ID, known, present bool) string {
	h, ok := harness.Get(id)
	if !ok || !h.Capabilities.TrustReview {
		return TrustNotApplicable
	}
	switch {
	case !known:
		return TrustUnknown
	case present:
		return TrustRecordPresent
	default:
		return TrustMissing
	}
}

// telemetry counts each corpus and splits the session and firing counts per harness.
func telemetry(ev mine.EvidenceSet) Telemetry {
	t := Telemetry{
		ReviewChains:     len(ev.Chains),
		DenialsRows:      denialsCount(),
		AuditFirings:     len(ev.Firings),
		SessionSnapshots: len(ev.Sessions),
		Harnesses:        []HarnessTelemetry{},
	}
	if t.AuditFirings == 0 {
		t.AuditFirings = auditFirings()
	}
	rows := map[string]*HarnessTelemetry{}
	row := func(id string) *HarnessTelemetry {
		r, ok := rows[id]
		if !ok {
			r = &HarnessTelemetry{Harness: id}
			rows[id] = r
		}
		return r
	}
	for _, h := range harness.All() {
		row(string(h.ID))
	}
	for _, u := range ev.Sessions {
		row(u.Harness).Sessions++
	}
	for _, f := range ev.Firings {
		row(f.Harness).Firings++
	}
	for _, ch := range ev.Chains {
		if ch.HarnessKnown {
			row(ch.Harness).Chains++
		}
	}
	for _, h := range harness.All() {
		t.Harnesses = append(t.Harnesses, *row(string(h.ID)))
	}
	return t
}

// denialsCount is the size of the auto-mode denials corpus on this machine.
func denialsCount() int {
	rows, err := denials.Read()
	if err != nil {
		return 0
	}
	return len(rows)
}

// auditFirings counts the block decisions the global master hook logged. The log
// sits beside the master that wrote it, so this reads beside the REGISTERED
// master: a migrated machine holds a log at both paths, and the one being
// appended to is the one the harness invokes. It falls back to the resolved
// store when no harness registers a master. A missing log reads as zero.
func auditFirings() int {
	for _, registrar := range guardRegistrars() {
		path, err := registrar.RegisteredMasterPath(claude.GlobalScope)
		if err != nil || path == "" {
			continue
		}
		return countLines(filepath.Join(filepath.Dir(path), claude.AuditLogName))
	}
	dir, err := claude.GetHooksDir(claude.GlobalScope)
	if err != nil {
		return 0
	}
	return countLines(filepath.Join(dir, claude.AuditLogName))
}

// firingsApplicable reports whether a tier contributes to the audit-log firing
// count. Only the guard tier's master PreToolUse hook writes that log; check,
// instruction and review write no audit-log firings, so a reader shows n/a
// rather than a misleading zero. The review tier's firings live in the
// adjudication ledger, surfaced already as chains and incidence, not here.
func firingsApplicable(tier string) bool {
	return tier == "guard"
}

// boolOf reads a bool value from a decoded map, false when absent or not a bool.
func boolOf(v any) bool {
	b, _ := v.(bool)
	return b
}

// corpusRows carries the cadence corpus sweep into the snapshot, adding the path
// each corpus lives at so a reader knows where to look.
func corpusRows(rows []cadence.CorpusStatus, opts Options) []CorpusRow {
	// Keys match the corpus names cadence.Refresh emits. review-findings lives at
	// the gate state dir (the ledger shards); auto-denials at the denials dir.
	paths := map[string]string{
		"review-findings":     opts.GateStateDir,
		"transcript-friction": sessions.StateDir(),
		"auto-denials":        denials.StateDir(),
	}
	out := make([]CorpusRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, CorpusRow{
			Corpus:  r.Corpus,
			Present: r.Present,
			Size:    r.Size,
			Path:    paths[r.Corpus],
		})
	}
	return out
}

// collectHooks reads the registered managed hooks in a scope. A missing manifest
// yields an empty list, never an error.
func collectHooks(scope string) []HookInfo {
	manifest, err := claude.LoadManifest(scope)
	if err != nil || manifest == nil {
		return []HookInfo{}
	}
	out := make([]HookInfo, 0, len(manifest.Hooks))
	for name, entry := range manifest.Hooks {
		source := entry.Source
		if source == "" {
			source = "manual"
		}
		out = append(out, HookInfo{
			Name:   name,
			Scope:  scope,
			Source: source,
			Rule:   entry.Rule,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// collectPresets reads the catalog roster with each preset's enable and drift
// state in a scope. A missing catalog or manifest yields an empty list.
func collectPresets(scope string) []PresetInfo {
	catalog, err := presets.LoadCatalog()
	if err != nil {
		return []PresetInfo{}
	}
	items, err := catalog.ListVisible("")
	if err != nil {
		return []PresetInfo{}
	}
	manifest, err := claude.LoadManifest(scope)
	if err != nil || manifest == nil {
		manifest = &claude.Manifest{Hooks: map[string]claude.ManifestEntry{}}
	}
	out := make([]PresetInfo, 0, len(items))
	for _, item := range items {
		hookName := strings.TrimSuffix(item.HookFile, ".py")
		entry, ok := manifest.Hooks[hookName]
		enabled := ok && strings.EqualFold(strings.TrimSpace(entry.Source), claude.SourceFactory) && strings.TrimSpace(entry.SourceID) == item.Name
		drifted := false
		if enabled {
			if needs, _, derr := presets.FactoryPresetNeedsReinstall(scope, catalog, item, hookName, entry); derr == nil {
				drifted = needs
			}
		}
		out = append(out, PresetInfo{
			Name:        item.Name,
			Bundle:      presets.BundleFor(item),
			Description: item.Description,
			Enabled:     enabled,
			Drifted:     drifted,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// teamLockfileName is the repo-committed team policy lockfile at project root.
// The read here is a minimal, decoupled decode of the file cmd/team.go writes.
const teamLockfileName = "steer-team.lock.json"

// collectTeam reads the repo-committed team lockfile summary, when one is present
// at the project root. A missing or unreadable lockfile reads as absent.
func collectTeam() TeamState {
	data, err := os.ReadFile(teamLockfileName)
	if err != nil {
		return TeamState{Present: false}
	}
	var lf struct {
		CatalogVersion string `json:"catalog_version"`
		Hooks          []struct {
			Name string `json:"name"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &lf); err != nil {
		return TeamState{Present: false}
	}
	return TeamState{
		Present:        true,
		Path:           teamLockfileName,
		HooksLocked:    len(lf.Hooks),
		CatalogVersion: lf.CatalogVersion,
	}
}

// MarshalIndent renders the snapshot as indented JSON.
func (s *SystemStatus) MarshalIndent() ([]byte, error) {
	return json.MarshalIndent(s, "", "  ")
}
