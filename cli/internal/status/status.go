// Package status composes a read-only snapshot of the steer feedback loop, keyed
// on the failure class and its intervention's verdict rather than the hook.
// Collect reads the scored intervention inventory and review chains (mine), the
// attention subset (the pure cadence.Refresh), and the arming posture (claude).
// It never writes state. Terminal, JSON, HTML and TUI all render this one struct.
package status

import (
	"encoding/json"
	"path/filepath"
	"sort"
	"time"

	"github.com/steer-sh/steer-cli/internal/cadence"
	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/denials"
	"github.com/steer-sh/steer-cli/internal/gate"
	"github.com/steer-sh/steer-cli/internal/gate/mine"
)

// StatusVersion is the schema version of the emitted snapshot. It mirrors
// report.DigestVersion: --json is a stable agent contract, so bump this whenever
// the shape or semantics of SystemStatus change.
const StatusVersion = 1

// recentChainLimit caps the chain list a snapshot carries.
const recentChainLimit = 20

// Options are the inputs to a collection. State dirs are passed in so a test can
// point them at fixtures; the command fills them from the gate config.
type Options struct {
	GateStateDir string // commit-review state: interventions.jsonl + ledger shards
	Ledger       string // single-ledger override, or "" for the shard glob
	Config       gate.Config
	WindowDays   int  // transcript look-back for the attention refresh
	AllProjects  bool // scan every project, not just the cwd
	MinAfter     int  // post-install chains below which a verdict is withheld
}

// ScoredIntervention is one live intervention with its recomputed verdict. It is
// the full inventory row — effective classes included — so an agent can see an
// already-decided class and skip re-proposing it. Before/After reuse
// cadence.Incidence, so one payload keys chain counts one way: units.
type ScoredIntervention struct {
	ID      string            `json:"id"`
	Class   string            `json:"class"`
	Tier    string            `json:"tier"`
	Target  string            `json:"target"`
	Status  string            `json:"status"`
	HookID  string            `json:"hook_id,omitempty"`
	Verdict string            `json:"verdict"`
	Before  cadence.Incidence `json:"before"`
	After   cadence.Incidence `json:"after"`
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

// ScopeArming is one scope's registration state for a hook.
type ScopeArming struct {
	Scope      string `json:"scope"`
	Registered bool   `json:"registered"`
	Effective  bool   `json:"effective"`
	Timeout    int    `json:"timeout,omitempty"`
}

// Posture is the arming and instrument state — what is guarding, and with what.
type Posture struct {
	Instrument string        `json:"instrument"`
	Reviewer   string        `json:"reviewer"`
	Ledger     string        `json:"ledger"`
	CommitGate []ScopeArming `json:"commit_gate"`
	Denials    []ScopeArming `json:"denials"`
}

// AttentionQueue is the subset of the loop needing action: the cadence queue
// minus its corpus sweep, which SystemStatus carries once at the top level.
type AttentionQueue struct {
	Interventions []cadence.Intervention `json:"interventions"`
	Unscored      []string               `json:"unscored"`
	Candidates    []cadence.Candidate    `json:"candidates"`
}

// Telemetry reports corpus sizes — cheap line and row counts, no model call.
type Telemetry struct {
	ReviewChains int `json:"review_chains"`
	DenialsRows  int `json:"denials_rows"`
	AuditFirings int `json:"audit_firings"`
}

// SystemStatus is the one snapshot all render targets read. It is versioned so
// --json is a stable agent contract.
type SystemStatus struct {
	StatusVersion int                    `json:"status_version"`
	GeneratedAt   string                 `json:"generated_at"`
	Posture       Posture                `json:"posture"`
	Interventions []ScoredIntervention   `json:"interventions"`
	Summary       VerdictSummary         `json:"summary"`
	Chains        []ChainSummary         `json:"chains"`
	Attention     AttentionQueue         `json:"attention"`
	Telemetry     Telemetry              `json:"telemetry"`
	Corpora       []cadence.CorpusStatus `json:"corpora"`
}

// Collect composes the snapshot. It never writes and every source fails soft: a
// missing ledger yields an empty section, never an error. GeneratedAt is the one
// non-deterministic field; a --json caller wanting byte stability clears it.
func Collect(opts Options) (*SystemStatus, error) {
	if opts.MinAfter == 0 {
		opts.MinAfter = mine.MinAfterChains
	}
	s := &SystemStatus{
		StatusVersion: StatusVersion,
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339),
		Interventions: []ScoredIntervention{},
		Chains:        []ChainSummary{},
		Corpora:       []cadence.CorpusStatus{},
		Summary:       VerdictSummary{Counts: map[string]int{}},
	}

	// Full scored inventory + recent chains, from one miner, read-only.
	records, _ := mine.Load(opts.GateStateDir)
	rows, _ := mine.LoadRows(opts.GateStateDir, opts.Ledger)
	chains := mine.BuildChains(rows, false)

	s.Interventions = scoreInventory(records, chains, opts.MinAfter)
	s.Summary = summarize(s.Interventions)
	s.Chains = recentChains(chains, recentChainLimit)

	// Attention subset, from the pure orchestrator function — no side effects.
	q, err := cadence.Refresh(cadence.Options{
		GateStateDir: opts.GateStateDir,
		Ledger:       opts.Ledger,
		WindowDays:   opts.WindowDays,
		AllProjects:  opts.AllProjects,
		MinAfter:     opts.MinAfter,
	})
	if err == nil && q != nil {
		s.Attention = AttentionQueue{
			Interventions: q.Interventions,
			Unscored:      q.Unscored,
			Candidates:    q.Candidates,
		}
		s.Corpora = q.Corpora
	} else {
		s.Attention = AttentionQueue{
			Interventions: []cadence.Intervention{},
			Unscored:      []string{},
			Candidates:    []cadence.Candidate{},
		}
	}

	s.Posture = collectPosture(opts.Config)
	s.Telemetry = Telemetry{
		ReviewChains: len(chains),
		DenialsRows:  denialsCount(),
		AuditFirings: auditFirings(),
	}
	return s, nil
}

// scoreInventory recomputes every live intervention's verdict. Reworked and
// removed records are dropped as history — the filter Refresh applies. A
// session-unit record the chain engine cannot score is carried with verdict
// "unscored" rather than scored against the wrong corpus.
func scoreInventory(records []mine.Record, chains []mine.Chain, minAfter int) []ScoredIntervention {
	out := []ScoredIntervention{}
	for _, r := range records {
		st := str(r["status"])
		if st != "active" && st != "rejected" {
			continue
		}
		if str(r["unit"]) == "session" {
			out = append(out, ScoredIntervention{
				ID:      str(r["id"]),
				Class:   str(r["class"]),
				Tier:    str(r["tier"]),
				Target:  str(r["target"]),
				Status:  st,
				HookID:  str(r["hook_id"]),
				Verdict: "unscored",
			})
			continue
		}
		scored, err := mine.ScoreRecord(r, chains, minAfter)
		if err != nil {
			continue
		}
		out = append(out, ScoredIntervention{
			ID:      str(scored["id"]),
			Class:   str(scored["class"]),
			Tier:    str(scored["tier"]),
			Target:  str(scored["target"]),
			Status:  str(scored["status"]),
			HookID:  str(scored["hook_id"]),
			Verdict: str(scored["verdict"]),
			Before:  incidenceOf(scored["before"]),
			After:   incidenceOf(scored["after"]),
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
	}
	for _, scope := range []string{claude.ProjectScope, claude.GlobalScope} {
		g := claude.CommitGateHookArmed(scope)
		p.CommitGate = append(p.CommitGate, ScopeArming{
			Scope:      scope,
			Registered: g.Registered,
			Effective:  g.Effective(),
			Timeout:    g.Timeout,
		})
		d, err := claude.DenialsHookArming(scope)
		if err != nil {
			d = claude.DenialsArming{}
		}
		p.Denials = append(p.Denials, ScopeArming{
			Scope:      scope,
			Registered: d.Registered,
			Effective:  d.Registered && d.Matcher == claude.DenialsMatcher,
			Timeout:    d.Timeout,
		})
	}
	return p
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
// sits beside the master hook; a missing or unreadable log reads as zero.
func auditFirings() int {
	dir, err := claude.GetHooksDir(claude.GlobalScope)
	if err != nil {
		return 0
	}
	return countLines(filepath.Join(dir, claude.AuditLogName))
}

// MarshalIndent renders the snapshot as indented JSON.
func (s *SystemStatus) MarshalIndent() ([]byte, error) {
	return json.MarshalIndent(s, "", "  ")
}
