// Package status composes a read-only snapshot of the steer feedback loop, keyed
// on the failure class and what the corpus recorded about it rather than the hook.
// Collect reads the intervention inventory and review chains (mine), the
// attention subset (the pure cadence.Refresh), and the arming posture (claude).
// It never writes state. Terminal and TUI render this one struct; --json renders
// its projection (json.go).
package status

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/steer-sh/steer-cli/internal/adjudication"
	"github.com/steer-sh/steer-cli/internal/cadence"
	"github.com/steer-sh/steer-cli/internal/checks"
	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/codex"
	"github.com/steer-sh/steer-cli/internal/evidence"
	"github.com/steer-sh/steer-cli/internal/gate"
	"github.com/steer-sh/steer-cli/internal/gate/mine"
	"github.com/steer-sh/steer-cli/internal/gate/resolve"
	"github.com/steer-sh/steer-cli/internal/gate/reviewers"
	"github.com/steer-sh/steer-cli/internal/guard"
	"github.com/steer-sh/steer-cli/internal/harness"
	"github.com/steer-sh/steer-cli/internal/mods"
	"github.com/steer-sh/steer-cli/internal/pi"
	"github.com/steer-sh/steer-cli/internal/presets"
	"github.com/steer-sh/steer-cli/internal/report"
	"github.com/steer-sh/steer-cli/internal/sessions"
	"github.com/steer-sh/steer-cli/internal/steerbin"
)

// StatusVersion is the schema version of the emitted snapshot. It mirrors
// report.DigestVersion: --json is a stable agent contract, so bump this whenever
// the shape or semantics of SystemStatus change.
//
// EVERY COUNT IN THIS SNAPSHOT IS OBSERVATIONAL. The two windows are uncontrolled:
// an agent's standing instruction set can affect its behaviour, and no chain and
// no session records it. So the snapshot reports what the corpus
// recorded — hits, eligible units, window bounds, coverage, guard firings and the
// identities that counted each window — and the intervention's contribution to any
// change in those counts is unresolved. v11 removes the claim fields (verdict,
// p_value, effect, required_after, confounds, composition, instrument_drift,
// firings_applicable, attribution, chain_rate) and adds observation.
// v12 adds checks: the repository check tier, whose adoption, runner registration
// and observed execution are three separate readings. v13 adds observation.checks:
// a check-tier fix's executions in its window, counted on this machine. v14 adds
// posture.rules: each installed harness's rules section read against the manifest.
// v15 adds posture.masters[].trust: the harness's approval of the registered runner.
// v16 adds observation.checks.registered_host and other_host: a fix registered on
// another host is not missing coverage here. v17 adds interventions[].artifact: the
// fix a record binds and where it stands now. v18 adds rules and addenda: the
// instruction and review entries each store holds, a project addendum entry read
// both on disk and at HEAD. v19 adds posture.masters[].state `detached` and
// .store: an installed harness that runs none of a scope's guards. v20 adds
// interventions[].artifact.removed: a fix in place now that the archive records
// as removed after the record's split. v21 removes posture.denials,
// telemetry.denials_rows and the auto-denials corpus row. v22 adds coverage
// ledger_missed and ledger_overdue, and a ledger cause no longer files a record
// under repair. v23 extends that to an unknown scope and a session with no project.
// v25 adds observation.fixtures: what this machine knows about the stored cases
// behind a guard-tier fix. Its states answer attribution and freshness, never
// whether the guard blocks correctly in the field. v27 is the projection in json.go:
// one summary row per fix whose observation `steer fix score --json` carries,
// attention by id, hooks and presets from both scopes, resolver rows cut to their
// arming readings, scope.repo_root, and no chains. v28 adds posture.readiness: the
// answer `steer status` prints, so an agent reads trust and resolution with it.
// v29 adds posture.plugins: each harness's cached plugin copy, read against the
// stable executable. v33 adds hooks[].fixtures: every guard's stored cases, with or
// without a record.
const StatusVersion = 33

// recentChainLimit caps the chain list a snapshot carries.
const recentChainLimit = 20

// Options are the inputs to a collection. State dirs are passed in so a test can
// point them at fixtures; the command fills them from the gate config.
type Options struct {
	GateStateDir string // commit-review state: interventions.jsonl + ledger shards
	Ledger       string // single-ledger override, or "" for the shard glob
	Config       gate.Config
	WindowDays   int    // transcript look-back for the attention refresh
	Scope        string // active scope for the config-state read (default global)
	// InventoryScopes are the scopes whose hooks and presets the snapshot lists; empty
	// lists Scope alone. The TUI mutates the scope it lists, so it passes none.
	InventoryScopes []string
	// ScanScope selects which sessions the transcript scan reads. The zero
	// value reads every project.
	ScanScope report.Scope
	// RepoRoot is the Git root whose check tier the snapshot reports, "" outside
	// a repository. The caller resolves it; Collect runs no git command.
	RepoRoot string
	// Addendum is the review addendum the commit gate reads in RepoRoot, loaded by
	// the caller; nil leaves every review-tier artifact unread.
	Addendum *gate.Addendum
	// Evidence is every corpus a scoring pass reads. A caller leaving it zero gets
	// the ledger alone, and every session-unit record reports missing evidence.
	Evidence mine.EvidenceSet
	// ReviewerProbe is whether the configured reviewer CLI resolves, which the
	// readiness answer reads. The zero value checked nothing and raises no step.
	ReviewerProbe reviewers.Probe
}

// ScoredIntervention is one live intervention with its recomputed observation. It
// is the full inventory row, so an agent can see an already-decided class and skip
// re-proposing it. Before/After reuse cadence.Incidence, so one payload keys chain
// counts one way: units.
type ScoredIntervention struct {
	ID    string `json:"id"`
	Class string `json:"class"`
	// Problem and Intervention are the record's own explanation, authored when the
	// evidence was in hand: what goes wrong, and what this record does about it. A
	// class name is a label and cannot carry either, so every reader leads with
	// these. LegacyNote is the single free-text field that preceded them; it holds
	// implementation detail on the records that carry one and is never promoted.
	Problem      string            `json:"problem"`
	Intervention string            `json:"intervention"`
	LegacyNote   string            `json:"legacy_note,omitempty"`
	Match        string            `json:"match,omitempty"`
	Tier         string            `json:"tier"`
	Target       string            `json:"target"`
	Status       string            `json:"status"`
	HookID       string            `json:"hook_id,omitempty"`
	Before       cadence.Incidence `json:"before"`
	After        cadence.Incidence `json:"after"`
	Observation  mine.Observation  `json:"observation"`
	// Artifact is the fix the record binds and where it stands now, null on a
	// declined record.
	Artifact *cadence.Artifact `json:"artifact"`
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
	// Corpus and Unit name the evidence the class is counted over and what one
	// denominator entry is, so a reader never compares a chain rate to a session rate.
	Corpus string `json:"corpus"`
	Unit   string `json:"unit"`
	// SignalKind is the classifier kind a session-unit class is counted over.
	SignalKind string `json:"signal_kind,omitempty"`
}

// HookInfo is one registered managed hook in a scope.
type HookInfo struct {
	Name   string `json:"name"`
	Scope  string `json:"scope"`
	Source string `json:"source"`
	Rule   string `json:"rule,omitempty"`
	// Fixtures is the guard's stored cases read against the guard installed now,
	// in the shape a guard-tier record's score row carries, whether or not a record
	// names the guard. It is absent only when the guard left the store mid-read.
	Fixtures *mine.FixtureReceipt `json:"fixtures,omitempty"`
}

// PresetInfo is one catalog preset with its enable and drift state in a scope.
type PresetInfo struct {
	Name        string `json:"name"`
	Scope       string `json:"scope"`
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
	// Error is why a file that IS there could not be read. A project with no
	// lockfile is not configured for one and that is not a fault; a file that
	// exists and will not parse is a fault, and reading the two as one absence
	// told the reader nothing was there while a broken file sat at the path.
	Error string `json:"error,omitempty"`
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

// VerdictSummary counts live interventions by the field-derived action section.
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

// Resolution states for the command a registration names. A harness runs the tool
// anyway when the command does not resolve, and reports nothing, so a registered
// channel that cannot run reads as armed everywhere else.
const (
	ResolveResolvable    = steerbin.Resolvable
	ResolveMissing       = steerbin.Missing
	ResolveUnknown       = steerbin.Unknown
	ResolveNotRegistered = "not-registered"
)

// ScopeArming is one harness and scope's registration state for a hook. Three
// separate readings, because registration alone proves the least of them:
// Registered is what the configuration says, Resolvable is whether the command
// resolves from THIS process, and Observed is when the channel last wrote to its
// own corpus.
type ScopeArming struct {
	Harness    string `json:"harness"`
	Scope      string `json:"scope"`
	Registered bool   `json:"registered"`
	Effective  bool   `json:"effective"`
	Timeout    int    `json:"timeout,omitempty"`
	// Trust is the harness's posture on this registration. Registration alone does
	// not prove enforcement on a harness that reviews hook trust.
	Trust string `json:"trust"`
	// Command is the command the registration names, verbatim.
	Command string `json:"command,omitempty"`
	// Resolvable is a PROXY and never a proof of enforcement: the hook runs under
	// the harness's environment, whose PATH may differ from this process's. It can
	// prove a command absent; it can never prove one runs.
	Resolvable string `json:"resolvable"`
	// Observed is the epoch second of the newest row this channel wrote, 0 when it
	// never wrote one. Never observed is not proof of breakage — nobody may have
	// attempted the action the channel fires on.
	Observed float64 `json:"observed,omitempty"`
	// ObservedCorpus names the corpus Observed was read from, "" when the channel
	// writes no corpus of its own and nothing can be observed. A corpus records no
	// scope, so both scopes of one harness report the same evidence.
	ObservedCorpus string `json:"observed_corpus,omitempty"`
}

// Master runtime states. The stamp names the runtime spliced into a registered
// master; the comparison is against the running binary's own runtime digest.
const (
	MasterCurrent       = "current"
	MasterStale         = "stale"
	MasterUnknown       = "unknown"
	MasterNotRegistered = "not-registered"
	// MasterDetached is an installed harness that runs none of a scope's guards:
	// the store holds guards and a master, and the harness registers no master or
	// one other than the store's.
	MasterDetached = "detached"
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
	// Store is the master the scope's store holds, set on a detached row: the file
	// the harness should run.
	Store string `json:"store,omitempty"`
	// Trust is what this machine can say about the harness's approval of the
	// registered runner, "" when none is registered.
	Trust string `json:"trust,omitempty"`
	// Observed is the epoch second of the newest row in the audit log beside this
	// registered master, 0 when the master never blocked a call.
	Observed float64 `json:"observed,omitempty"`
	// Sources reads the guard source the REGISTERED master embeds against the
	// scope's store files. Registration, runtime freshness, detachment and source
	// agreement are four facts, so this never folds into State.
	Sources *claude.SourceComparison `json:"sources,omitempty"`
	// StoreSources is the same reading for the master the store holds, set on a
	// detached row. A detached harness can register matching source, and a store
	// master that already covers the store is repaired by sync rather than by a
	// rebuild, so the two readings choose different repairs.
	StoreSources *claude.SourceComparison `json:"store_sources,omitempty"`
}

// SourcesDrifted reports that the registered master runs something other than
// the guard files in the store. A comparison that reached no answer never
// drifts, and a detached row never drifts here: StoreSourcesDrifted is the
// reading that bears on one.
func (m MasterRuntime) SourcesDrifted() bool {
	return !m.Detached() && m.Sources != nil &&
		m.Sources.Complete() && len(m.Sources.Drifted()) > 0
}

// StoreSourcesDrifted reports a detached row whose own store master does not
// carry the store's guards. It decides the ORDER of the repair: sync refuses a
// store its master does not cover, so that store needs a rebuild first.
func (m MasterRuntime) StoreSourcesDrifted() bool {
	return m.StoreSources != nil && m.StoreSources.Complete() &&
		len(m.StoreSources.Drifted()) > 0
}

// Stale reports whether this master needs a rebuild. An unstamped master is
// unknown rather than current: every store built before the stamp existed is
// unstamped, so treating absence as up to date would report exactly the machines
// that need the rebuild as healthy.
func (m MasterRuntime) Stale() bool { return m.State == MasterStale || m.State == MasterUnknown }

// Detached reports whether this harness runs none of the scope's guards.
func (m MasterRuntime) Detached() bool { return m.State == MasterDetached }

// Rules section states. The manifest is what a record binds; the section is what
// the agent reads.
const (
	RulesCurrent = "current"
	RulesDrifted = "drifted"
	RulesUnknown = "unknown"
)

// RulesPresence is one installed harness and scope's rules section read against
// the manifest. A record keeps reading as live while its rule is absent from the
// document an agent loads, so this is the reading that says the rule reached it.
type RulesPresence struct {
	Harness string `json:"harness"`
	Scope   string `json:"scope"`
	Path    string `json:"path,omitempty"`
	// Expected counts the rules the manifest names in this scope.
	Expected int `json:"expected"`
	// Missing are the rule ids whose text the section does not carry.
	Missing []string `json:"missing"`
	// Extra are section lines the manifest does not name; the next sync removes them.
	Extra []string `json:"extra"`
	State string   `json:"state"`
}

// NeedsSync reports whether a rebuild would change this section, or whether the
// section could not be read at all.
func (r RulesPresence) NeedsSync() bool { return r.State != RulesCurrent }

// Posture is the arming and instrument state — what is guarding, and with what.
type Posture struct {
	Instrument string        `json:"instrument"`
	Reviewer   string        `json:"reviewer"`
	Ledger     string        `json:"ledger"`
	CommitGate []ScopeArming `json:"commit_gate"`
	// Resolver is the Stop-hook backfill arming per scope. Unresolved counts the
	// ledger rows still owing a transcript join — evidence the scorer withholds on.
	Resolver   []ScopeArming `json:"resolver"`
	Unresolved int           `json:"unresolved_rows"`
	// Masters is the runtime provenance of each registered guard master.
	Masters []MasterRuntime `json:"masters"`
	// Rules is each installed harness's rules section against the manifest.
	Rules []RulesPresence `json:"rules"`
	// Plugins is each harness's cached copy of the Steer plugin.
	Plugins []PluginCopy `json:"plugins"`
	// Mods is each enabled Claude Code mod read against its cached inspection.
	Mods []mods.Exposure `json:"mods"`
	// GuardInterpreter is whether this process finds the interpreter every guard
	// runner starts under.
	GuardInterpreter InterpreterReading `json:"guard_interpreter"`
	// HookTimeout is the harness timeout, in seconds, a commit-gate registration
	// needs under the current reviewer timeout.
	HookTimeout int `json:"-"`
}

// AttentionQueue is the subset of the loop needing action: the cadence queue
// minus its corpus sweep, which SystemStatus carries once at the top level.
type AttentionQueue struct {
	Interventions []cadence.Intervention `json:"interventions"`
	Unscored      []string               `json:"unscored"`
	Candidates    []cadence.Candidate    `json:"candidates"`
	// Checks are the open steps in the repository's check tier, the same rows the
	// queue carries.
	Checks []cadence.CheckWork `json:"checks"`
}

// HarnessTelemetry is one harness's share of the corpora, so a reader can tell a
// harness that contributed nothing from one that was never mined.
type HarnessTelemetry struct {
	Harness     string `json:"harness"`
	Sessions    int    `json:"sessions"`
	Firings     int    `json:"firings"`
	CheckBlocks int    `json:"check_blocks"`
	Chains      int    `json:"chains"`
}

// Telemetry reports corpus sizes — cheap line and row counts, no model call.
// CheckBlocks counts the check.sh Stop blocks recorded on this machine that no
// exclusion retires, the blocks a reading pass can take.
type Telemetry struct {
	ReviewChains     int                `json:"review_chains"`
	AuditFirings     int                `json:"audit_firings"`
	CheckBlocks      int                `json:"check_blocks"`
	SessionSnapshots int                `json:"session_snapshots"`
	Harnesses        []HarnessTelemetry `json:"harnesses"`
}

// SystemStatus is the one snapshot all render targets read. It is versioned so
// --json is a stable agent contract.
type SystemStatus struct {
	StatusVersion int                  `json:"status_version"`
	GeneratedAt   string               `json:"generated_at"`
	Posture       Posture              `json:"posture"`
	Checks        CheckTier            `json:"checks"`
	Interventions []ScoredIntervention `json:"interventions"`
	Summary       VerdictSummary       `json:"summary"`
	Chains        []ChainSummary       `json:"chains"`
	Attention     AttentionQueue       `json:"attention"`
	Telemetry     Telemetry            `json:"telemetry"`
	Corpora       []CorpusRow          `json:"corpora"`
	// Config state — what the dashboard panels mutate. Each reads fail-soft.
	Hooks   []HookInfo     `json:"hooks"`
	Presets []PresetInfo   `json:"presets"`
	Rules   []RuleInfo     `json:"rules"`
	Addenda []AddendumInfo `json:"addenda"`
	Team    TeamState      `json:"team"`
	// Stores a reader refused. Every config section above fails soft, so an
	// unreadable store would otherwise read as an empty inventory — the same
	// snapshot a machine with no guards produces, while the installed guards
	// still run.
	StoreProblems []StoreProblem `json:"store_problems"`
	// ReviewerProbe is the caller's reading of the reviewer CLI, kept for the
	// readiness answer.
	ReviewerProbe reviewers.Probe `json:"-"`
}

// StoreProblem names a guard store whose manifest a reader refused, with the
// refusal. It states that the sections below it are incomplete, not empty.
type StoreProblem struct {
	Scope string `json:"scope"`
	Error string `json:"error"`
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
		ReviewerProbe: opts.ReviewerProbe,
	}

	// Full scored inventory + recent chains, read-only.
	records, _ := mine.Load(opts.GateStateDir)
	ev := opts.Evidence
	if ev.Chains == nil {
		rows, _ := mine.LoadRows(opts.GateStateDir, opts.Ledger)
		ev.Chains = mine.BuildChains(rows, false)
	}
	chains := ev.Chains

	s.Interventions = scoreInventory(records, ev,
		cadence.ArtifactSources{RepoRoot: opts.RepoRoot, Addendum: opts.Addendum})
	s.Summary = summarize(s.Interventions)
	s.Chains = recentChains(chains, recentChainLimit)

	// Attention subset, from the pure orchestrator function — no side effects.
	q, err := cadence.Refresh(cadence.Options{
		GateStateDir: opts.GateStateDir,
		Ledger:       opts.Ledger,
		WindowDays:   opts.WindowDays,
		ScanScope:    opts.ScanScope,
		RepoRoot:     opts.RepoRoot,
		Addendum:     opts.Addendum,
		Evidence:     ev,
	})
	if err == nil && q != nil {
		s.Attention = AttentionQueue{
			Interventions: q.Interventions,
			Unscored:      q.Unscored,
			Candidates:    markUnread(q.Candidates, cadence.LoadReadMarks(cadence.StateDir())),
			Checks:        q.Checks,
		}
		s.Corpora = corpusRows(q.Corpora, opts)
	} else {
		s.Attention = AttentionQueue{
			Interventions: []cadence.Intervention{},
			Unscored:      []string{},
			Candidates:    []cadence.Candidate{},
			Checks:        []cadence.CheckWork{},
		}
	}

	s.Posture = collectPosture(opts.Config, ev)
	s.Checks = collectChecks(opts.RepoRoot)
	s.Telemetry = telemetry(ev, checkBlocks())
	inventory := opts.InventoryScopes
	if len(inventory) == 0 {
		inventory = []string{opts.Scope}
	}
	for _, scope := range inventory {
		hooks, problems := collectHooks(scope)
		s.Hooks = append(s.Hooks, hooks...)
		s.StoreProblems = append(s.StoreProblems, problems...)
		roster, problems := collectPresets(scope)
		s.Presets = append(s.Presets, roster...)
		s.StoreProblems = append(s.StoreProblems, problems...)
	}
	rules, problems := collectRules()
	s.Rules = rules
	s.StoreProblems = append(s.StoreProblems, problems...)
	s.StoreProblems = dedupeStoreProblems(s.StoreProblems)
	s.Addenda = AddendumInventory(opts.Addendum)
	s.Team = collectTeam()
	return s, nil
}

// scoreInventory recomputes every live intervention's observation. Reworked and
// removed records are dropped as history, and a record from a corpus no engine
// reads is dropped too — Refresh reports it by id under Unscored. Both are the
// filters Refresh applies.
func scoreInventory(records []mine.Record, ev mine.EvidenceSet, src cadence.ArtifactSources) []ScoredIntervention {
	out := []ScoredIntervention{}
	for _, r := range records {
		st := str(r["status"])
		if st != "active" && st != "rejected" {
			continue
		}
		if !mine.Scorable(r) {
			continue
		}
		scored, err := mine.ScoreWith(r, ev)
		if err != nil {
			continue
		}
		scoredTier := str(scored["tier"])
		// The prose is read off the stored record, never off the scored copy: an
		// explanation is not a measurement output.
		prose := mine.ProseOf(r)
		out = append(out, ScoredIntervention{
			ID:           str(scored["id"]),
			Class:        str(scored["class"]),
			Problem:      prose.Problem,
			Intervention: prose.Intervention,
			LegacyNote:   prose.LegacyNote,
			Match:        str(r["match"]),
			Tier:         scoredTier,
			Target:       str(scored["target"]),
			Status:       str(scored["status"]),
			HookID:       str(scored["hook_id"]),
			Before:       incidenceOf(scored["before"]),
			After:        incidenceOf(scored["after"]),
			Observation:  observationOf(scored["observation"]),
			Artifact:     cadence.ArtifactOf(r, src),
			Reason:       str(scored["reason"]),
			Reopen:       str(scored["reopen"]),
			RejectedTS:   numFloat(r["rejected_ts"]),

			Scope:          cadence.ScopeOf(scored["scope"]),
			BeforeCoverage: cadence.CoverageOf(scored["before_coverage"]),
			AfterCoverage:  cadence.CoverageOf(scored["after_coverage"]),
			Corpus:         mine.CorpusOf(r),
			Unit:           mine.UnitOf(r),
			SignalKind:     str(scored["signal_kind"]),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// summarize counts the inventory by the action section each observation files under.
func summarize(items []ScoredIntervention) VerdictSummary {
	counts := map[string]int{}
	for _, it := range items {
		counts[ActionFor(it.Observation, it.Scope, it.Tier, it.Artifact)]++
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
// and the resolver, plus the reviewer identity from the config.
func collectPosture(cfg gate.Config, ev mine.EvidenceSet) Posture {
	observed := observedChannels(ev)
	p := Posture{
		Instrument:       cfg.InstrumentID(),
		Reviewer:         cfg.ReviewerModel,
		Ledger:           cfg.LogFile(),
		CommitGate:       []ScopeArming{},
		Resolver:         []ScopeArming{},
		Masters:          []MasterRuntime{},
		Rules:            []RulesPresence{},
		Plugins:          PluginCopies(),
		Mods:             ModExposures(),
		GuardInterpreter: ReadInterpreter(exec.LookPath),
		Unresolved:       resolve.CountUnresolved(cfg),
		HookTimeout:      cfg.HookTimeout(),
	}
	cwd, _ := os.Getwd()
	claudeID := string(harness.ClaudeCode)
	codexID := string(harness.Codex)
	piID := string(harness.Pi)
	for _, scope := range []string{claude.ProjectScope, claude.GlobalScope} {
		g := claude.CommitGateHookArmed(scope)
		p.CommitGate = append(p.CommitGate, withEvidence(ScopeArming{
			Harness:    claudeID,
			Scope:      scope,
			Registered: g.Registered,
			Effective:  g.Effective(),
			Timeout:    g.Timeout,
			Trust:      TrustNotApplicable,
			Command:    g.Command,
		}, ledgerCorpus, observed.gate[claudeID]))
		cg := codex.CommitGateArming(scope, cwd)
		p.CommitGate = append(p.CommitGate, withEvidence(ScopeArming{
			Harness:    codexID,
			Scope:      scope,
			Registered: cg.Registered,
			Effective:  cg.Effective(),
			Timeout:    cg.Timeout,
			Trust:      trustState(harness.Codex, cg.TrustStateKnown, cg.TrustRecordPresent),
			Command:    cg.Command,
		}, ledgerCorpus, observed.gate[codexID]))
		// Pi is unlisted: its rows appear only where its extension registers
		// either channel. One extension carries both, so both rows go together.
		pg := pi.CommitGateArming(scope, cwd)
		pr := pi.ResolverArming(scope, cwd)
		piListed := pg.Registered || pr.Registered
		if piListed {
			p.CommitGate = append(p.CommitGate, withEvidence(ScopeArming{
				Harness:    piID,
				Scope:      scope,
				Registered: pg.Registered,
				Effective:  pg.Registered,
				Timeout:    pg.Timeout,
				// Steer reads no Pi trust record, so nothing is known: the false pair
				// says so. trustState, not this line, turns that into a state.
				Trust:   trustState(harness.Pi, false, false),
				Command: pg.Command,
			}, ledgerCorpus, observed.gate[piID]))
		}
		// The resolver rides a turn-end event, which takes no matcher: registered is
		// effective. The rows follow the commit gate's harness order, because a
		// reader pairs a resolver row with the gate row it arms with.
		b := claude.BackfillHookArming(scope)
		p.Resolver = append(p.Resolver, withEvidence(ScopeArming{
			Harness:    claudeID,
			Scope:      scope,
			Registered: b.Registered,
			Effective:  b.Registered,
			Timeout:    b.Timeout,
			Trust:      TrustNotApplicable,
			Command:    b.Command,
		}, "", 0))
		cr := codex.ResolverArmingRow(scope, cwd)
		p.Resolver = append(p.Resolver, withEvidence(ScopeArming{
			Harness:    codexID,
			Scope:      scope,
			Registered: cr.Registered,
			Effective:  cr.Registered,
			Timeout:    cr.Timeout,
			Trust:      trustState(harness.Codex, cr.TrustStateKnown, cr.TrustRecordPresent),
			Command:    cr.Command,
		}, "", 0))
		if piListed {
			p.Resolver = append(p.Resolver, withEvidence(ScopeArming{
				Harness:    piID,
				Scope:      scope,
				Registered: pr.Registered,
				Effective:  pr.Registered,
				Timeout:    pr.Timeout,
				Trust:      trustState(harness.Pi, false, false),
				Command:    pr.Command,
			}, "", 0))
		}
		p.Masters = append(p.Masters, masterRuntimes(scope)...)
		p.Rules = append(p.Rules, RulesPresences(scope, guardRegistrars())...)
	}
	return p
}

// InstalledRules reads every installed harness's rules section in the named
// scopes. It is the cheap read for a caller that needs no full Collect.
func InstalledRules(scopes ...string) []RulesPresence {
	out := []RulesPresence{}
	for _, scope := range scopes {
		out = append(out, RulesPresences(scope, guardRegistrars())...)
	}
	return out
}

// RulesPresences reads each installed target's rules section in one scope. A
// harness that is not installed gets no row: nothing reads its document, and a
// sync writes only to installed harnesses. An unlisted harness gets a row only
// where its document already carries a Steer rule. A read that fails yields an
// unknown row, never an error.
func RulesPresences(scope string, targets []harness.Registrar) []RulesPresence {
	out := []RulesPresence{}
	for _, target := range targets {
		if !target.Harness().Installed() {
			continue
		}
		row := RulesPresence{
			Harness: string(target.Harness().ID),
			Scope:   scope,
			Missing: []string{},
			Extra:   []string{},
			State:   RulesUnknown,
		}
		presence, err := claude.TargetRulesPresence(scope, target)
		if !harness.Listed(target.Harness().ID) &&
			(err != nil || (len(presence.Missing) == presence.Expected && len(presence.Extra) == 0)) {
			continue
		}
		row.Path = presence.Path
		if err == nil {
			row.Expected = presence.Expected
			row.Missing, row.Extra = presence.Missing, presence.Extra
			row.State = RulesCurrent
			if presence.Drifted() {
				row.State = RulesDrifted
			}
		}
		out = append(out, row)
	}
	return out
}

// Corpus names a channel writes its evidence to. A channel with no name here
// writes no corpus of its own, so nothing about it can be observed.
const ledgerCorpus = "commit-review"

// channelEvidence is the newest row each channel wrote, by harness.
type channelEvidence struct {
	gate map[string]float64
}

// observedChannels reads the newest row each channel wrote. A row with no
// harness stamp counts for no harness rather than for all of them.
func observedChannels(ev mine.EvidenceSet) channelEvidence {
	out := channelEvidence{gate: map[string]float64{}}
	for _, ch := range ev.Chains {
		if !ch.HarnessKnown {
			continue
		}
		if ts := numFloat(ch.EndTS); ts > out.gate[ch.Harness] {
			out.gate[ch.Harness] = ts
		}
	}
	return out
}

// withEvidence adds the two readings a registration cannot give: whether the
// command resolves from this process, and when the channel last wrote a row.
func withEvidence(a ScopeArming, corpus string, observed float64) ScopeArming {
	a.Resolvable = ResolveNotRegistered
	if a.Registered {
		a.Resolvable = steerbin.Resolve(a.Command)
	}
	a.ObservedCorpus = corpus
	a.Observed = observed
	if corpus == "" {
		a.Observed = 0
	}
	return a
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
	return []harness.Registrar{claude.Registrar{}, codex.Registrar{}, pi.Registrar{}}
}

// masterRuntimes reads each harness's registered master in one scope and reports
// the runtime it carries. Every read fails soft: an unreadable configuration
// yields a not-registered row, never an error.
func masterRuntimes(scope string) []MasterRuntime {
	expected := guard.RuntimeDigest()
	store := storeMaster(scope)
	out := make([]MasterRuntime, 0, len(guardRegistrars()))
	for _, registrar := range guardRegistrars() {
		row := MasterRuntime{
			Harness:  string(registrar.Harness().ID),
			Scope:    scope,
			Expected: expected,
			State:    MasterNotRegistered,
		}
		path, err := registrar.RegisteredMasterPath(scope)
		if !harness.Listed(registrar.Harness().ID) && (err != nil || path == "") {
			continue // an unlisted harness appears only with a registration
		}
		if err != nil {
			out = append(out, row)
			continue
		}
		row.Path = path
		row.Sources = compareStore(scope, path, "no registered guard runner")
		detached := store != "" && registrar.Harness().Installed() && !claude.SamePath(path, store)
		if detached {
			// Both readings, because they choose different repairs: a registered
			// master can carry matching source while pointing elsewhere, and a
			// store master that already covers the store needs sync, not rebuild.
			row.Store, row.State = store, MasterDetached
			row.StoreSources = compareStore(scope, store, "")
			out = append(out, row)
			continue
		}
		if path == "" {
			out = append(out, row)
			continue
		}
		row.Trust = runnerTrust(registrar.Harness().ID, scope)
		row.Observed = newestFiring(filepath.Dir(path))
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

// compareStore reads one master against the scope's guard store. absent is the
// reason to report when there is no master to compare, which is a separate
// answer from a comparison that found agreement. A store that cannot be named
// is unavailable, never empty.
func compareStore(scope, master, absent string) *claude.SourceComparison {
	if master == "" {
		return &claude.SourceComparison{State: claude.ComparisonNotApplicable, Reason: absent}
	}
	dir, err := claude.GetHooksDir(scope)
	if err != nil {
		return &claude.SourceComparison{
			MasterPath: master,
			State:      claude.ComparisonUnavailable,
			Reason:     err.Error(),
		}
	}
	c := claude.CompareMasterSources(dir, master)
	return &c
}

// storeMaster names the master a scope's store holds, "" when the store holds no
// guards, has no master file, or cannot be read. A store with guards and no
// master needs a rebuild, which the registered row's own state already names.
func storeMaster(scope string) string {
	hooks, err := claude.ListIndividualHooks(scope)
	if err != nil || len(hooks) == 0 {
		return ""
	}
	path, err := claude.GetMasterHookPath(scope)
	if err != nil {
		return ""
	}
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	return path
}

// runnerTrust reads a harness's approval of its registered guard runner. Codex
// is the one harness whose approval record Steer reads; any other harness that
// reviews hook trust reads as unknown.
func runnerTrust(id harness.ID, scope string) string {
	if id == harness.Codex {
		a := codex.MasterArming(scope, ".")
		return trustState(id, a.TrustStateKnown, a.TrustRecordPresent)
	}
	return trustState(id, false, false)
}

// newestFiring is the epoch second of the last block the audit log beside a
// registered master recorded, 0 when the log holds none. The log records blocks
// only, so a master that has allowed every call reads as never observed.
func newestFiring(dir string) float64 {
	firings, present := mine.LoadFirings(dir)
	if !present {
		return 0
	}
	newest := 0.0
	for _, f := range firings {
		if f.TS > newest {
			newest = f.TS
		}
	}
	return newest
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

// telemetry counts each corpus and splits the session, firing and check block
// counts per harness. blocks is checkBlocks' count per harness.
func telemetry(ev mine.EvidenceSet, blocks map[string]int) Telemetry {
	t := Telemetry{
		ReviewChains:     len(ev.Chains),
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
	for id, n := range blocks {
		row(id).CheckBlocks += n
		t.CheckBlocks += n
	}
	for _, ch := range ev.Chains {
		if ch.HarnessKnown {
			row(ch.Harness).Chains++
		}
	}
	for _, h := range harness.All() {
		r := *row(string(h.ID))
		if !harness.Listed(h.ID) && r == (HarnessTelemetry{Harness: r.Harness}) {
			continue // an unlisted harness appears only with evidence
		}
		t.Harnesses = append(t.Harnesses, r)
	}
	return t
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

// checkBlocks counts the check.sh Stop blocks in this machine's live check state
// per harness, less the ones an exclusion retires. Unreadable state counts zero.
func checkBlocks() map[string]int {
	live, _ := checks.ReadLiveEvidence()
	var retired map[string]adjudication.Exclusion
	if entries, err := adjudication.LoadExclusions(adjudication.Dir()); err == nil {
		retired = adjudication.ExcludedRuns(entries)
	}
	return countBlocks(live.Runs, retired)
}

func countBlocks(runs []checks.OracleRun, retired map[string]adjudication.Exclusion) map[string]int {
	out := map[string]int{}
	for _, r := range runs {
		if _, excluded := retired[r.RunID]; checks.IsStopBlock(r) && !excluded {
			out[r.Harness]++
		}
	}
	return out
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
	// the gate state dir (the ledger shards).
	paths := map[string]string{
		"review-findings":     opts.GateStateDir,
		"transcript-friction": sessions.StateDir(),
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

// dedupeStoreProblems keeps one row per scope, in scope order. Several readers
// open the same store, and one bad manifest is one problem.
func dedupeStoreProblems(in []StoreProblem) []StoreProblem {
	out := make([]StoreProblem, 0, len(in))
	seen := map[string]struct{}{}
	for _, p := range in {
		if _, ok := seen[p.Scope]; ok {
			continue
		}
		seen[p.Scope] = struct{}{}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Scope < out[j].Scope })
	return out
}

// collectHooks reads the registered managed hooks in a scope. A missing manifest
// yields an empty list; a manifest the reader refuses yields a store problem, so
// a refused store never reads as a store with no guards.
func collectHooks(scope string) ([]HookInfo, []StoreProblem) {
	manifest, err := claude.LoadManifest(scope)
	if err != nil {
		return []HookInfo{}, []StoreProblem{{Scope: scope, Error: err.Error()}}
	}
	if manifest == nil {
		return []HookInfo{}, nil
	}
	out := make([]HookInfo, 0, len(manifest.Hooks))
	for name, entry := range manifest.Hooks {
		source := entry.Source
		if source == "" {
			source = "manual"
		}
		row := HookInfo{
			Name:   name,
			Scope:  scope,
			Source: source,
			Rule:   entry.Rule,
		}
		if receipt, ok := evidence.GuardFixtureReceipt(scope, name); ok {
			row.Fixtures = &receipt
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// collectPresets reads the catalog roster with each preset's enable and drift
// state in a scope. A missing catalog or manifest yields an empty list; a
// manifest the reader refuses yields a store problem beside the roster, whose
// enable state is then unknown rather than false.
func collectPresets(scope string) ([]PresetInfo, []StoreProblem) {
	catalog, err := presets.LoadCatalog()
	if err != nil {
		return []PresetInfo{}, nil
	}
	items, err := catalog.ListVisible("")
	if err != nil {
		return []PresetInfo{}, nil
	}
	var problems []StoreProblem
	manifest, err := claude.LoadManifest(scope)
	if err != nil || manifest == nil {
		if err != nil {
			problems = append(problems, StoreProblem{Scope: scope, Error: err.Error()})
		}
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
			Scope:       scope,
			Bundle:      presets.BundleFor(item),
			Description: item.Description,
			Enabled:     enabled,
			Drifted:     drifted,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, problems
}

// teamLockfileName is the repo-committed team policy lockfile at project root.
// The read here is a minimal, decoupled decode of the file cmd/team.go writes.
const teamLockfileName = "steer-team.lock.json"

// collectTeam reads the repo-committed team lockfile summary at the project root.
// A file that is not there reads as absent. A file that IS there and cannot be
// read or parsed reads as present with its error, because the two are different
// facts: one says the project configured none, the other says the one it
// configured is broken.
func collectTeam() TeamState {
	data, err := os.ReadFile(teamLockfileName)
	if err != nil {
		if os.IsNotExist(err) {
			return TeamState{Present: false}
		}
		return TeamState{Present: true, Path: teamLockfileName, Error: err.Error()}
	}
	var lf struct {
		CatalogVersion string `json:"catalog_version"`
		Hooks          []struct {
			Name string `json:"name"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &lf); err != nil {
		return TeamState{Present: true, Path: teamLockfileName, Error: err.Error()}
	}
	return TeamState{
		Present:        true,
		Path:           teamLockfileName,
		HooksLocked:    len(lf.Hooks),
		CatalogVersion: lf.CatalogVersion,
	}
}

// markUnread sets each candidate row's unread reading against the mark the last
// /steer-report pass left.
func markUnread(rows []cadence.Candidate, marks cadence.ReadMarks) []cadence.Candidate {
	out := make([]cadence.Candidate, len(rows))
	for i, c := range rows {
		unread := cadence.Unread(c, marks)
		c.Unread = &unread
		out[i] = c
	}
	return out
}
