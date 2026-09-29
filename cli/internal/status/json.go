package status

import (
	"encoding/json"
	"sort"

	"github.com/steer-sh/steer-cli/internal/cadence"
)

// statusJSON is the --json contract: the snapshot an agent reads for Steer's
// state. It projects SystemStatus rather than serializing it, so the terminal and
// the TUI keep every field while an agent reads one summary row per fix. The
// per-fix observation lives in `steer fix score --json`, keyed by the same id.
type statusJSON struct {
	StatusVersion int              `json:"status_version"`
	GeneratedAt   string           `json:"generated_at"`
	Posture       postureJSON      `json:"posture"`
	Checks        CheckTier        `json:"checks"`
	Interventions []fixSummaryJSON `json:"interventions"`
	Summary       VerdictSummary   `json:"summary"`
	Attention     attentionJSON    `json:"attention"`
	Telemetry     Telemetry        `json:"telemetry"`
	Corpora       []CorpusRow      `json:"corpora"`
	Hooks         []HookInfo       `json:"hooks"`
	Presets       []presetJSON     `json:"presets"`
	Rules         []RuleInfo       `json:"rules"`
	Addenda       []AddendumInfo   `json:"addenda"`
	Team          TeamState        `json:"team"`
	StoreProblems []StoreProblem   `json:"store_problems"`
}

// postureJSON is Posture with each resolver row cut to its arming readings.
type postureJSON struct {
	Instrument string          `json:"instrument"`
	Reviewer   string          `json:"reviewer"`
	Ledger     string          `json:"ledger"`
	CommitGate []ScopeArming   `json:"commit_gate"`
	Resolver   []resolverJSON  `json:"resolver"`
	Unresolved int             `json:"unresolved_rows"`
	Masters    []MasterRuntime `json:"masters"`
	Rules      []RulesPresence `json:"rules"`
	Plugins    []PluginCopy    `json:"plugins"`
	Readiness  readinessJSON   `json:"readiness"`
}

// readinessJSON is ReadinessOf, the answer `steer status` prints: whether a commit
// here is reviewed, each setup step with the command that clears it, and the trust
// Steer cannot verify.
type readinessJSON struct {
	Reviewing  bool       `json:"reviewing"`
	Headline   string     `json:"headline"`
	Steps      []stepJSON `json:"steps"`
	Unverified []string   `json:"unverified"`
	Note       string     `json:"note,omitempty"`
}

type stepJSON struct {
	Title string `json:"title"`
	Fix   string `json:"fix,omitempty"`
}

func readinessRow(r Readiness) readinessJSON {
	out := readinessJSON{Reviewing: r.Reviewing, Headline: r.Headline, Steps: []stepJSON{},
		Unverified: nonNil(r.Unverified), Note: r.Note}
	for _, step := range r.Steps {
		out.Steps = append(out.Steps, stepJSON{Title: step.Title, Fix: step.Fix})
	}
	return out
}

// resolverJSON is one harness and scope's review-outcome reader: whether it is
// registered and whether the command it names resolves.
type resolverJSON struct {
	Harness    string `json:"harness"`
	Scope      string `json:"scope"`
	Registered bool   `json:"registered"`
	Effective  bool   `json:"effective"`
	Trust      string `json:"trust"`
	Resolvable string `json:"resolvable"`
}

// fixSummaryJSON is one live record: what it is, where its fix stands, and the
// before/after counts over its own unit. Recurred is observation.recurrence.occurred.
type fixSummaryJSON struct {
	ID           string            `json:"id"`
	Class        string            `json:"class"`
	Problem      string            `json:"problem"`
	Intervention string            `json:"intervention"`
	Tier         string            `json:"tier"`
	Target       string            `json:"target"`
	Status       string            `json:"status"`
	HookID       string            `json:"hook_id,omitempty"`
	Scope        *cadence.Scope    `json:"scope"`
	Artifact     *cadence.Artifact `json:"artifact"`
	Corpus       string            `json:"corpus"`
	Unit         string            `json:"unit"`
	SignalKind   string            `json:"signal_kind,omitempty"`
	Recurred     bool              `json:"recurred"`
	Before       cadence.Incidence `json:"before"`
	After        cadence.Incidence `json:"after"`
}

// attentionJSON names the records asking for a reader by id; their rows are in
// interventions.
type attentionJSON struct {
	Interventions []string            `json:"interventions"`
	Unscored      []string            `json:"unscored"`
	Candidates    []cadence.Candidate `json:"candidates"`
	Checks        []cadence.CheckWork `json:"checks"`
}

// presetJSON is one catalog preset and the scopes it is enabled or drifted in.
type presetJSON struct {
	Name          string   `json:"name"`
	Bundle        string   `json:"bundle"`
	Description   string   `json:"description"`
	EnabledScopes []string `json:"enabled_scopes"`
	DriftedScopes []string `json:"drifted_scopes"`
}

// MarshalIndent renders the --json contract as indented JSON.
func (s *SystemStatus) MarshalIndent() ([]byte, error) {
	return json.MarshalIndent(s.contract(), "", "  ")
}

func (s *SystemStatus) contract() statusJSON {
	out := statusJSON{
		StatusVersion: s.StatusVersion,
		GeneratedAt:   s.GeneratedAt,
		Posture: postureJSON{
			Instrument: s.Posture.Instrument,
			Reviewer:   s.Posture.Reviewer,
			Ledger:     s.Posture.Ledger,
			CommitGate: nonNil(s.Posture.CommitGate),
			Resolver:   []resolverJSON{},
			Unresolved: s.Posture.Unresolved,
			Masters:    nonNil(s.Posture.Masters),
			Rules:      nonNil(s.Posture.Rules),
			Plugins:    nonNil(s.Posture.Plugins),
			Readiness:  readinessRow(ReadinessOf(s, s.ReviewerProbe)),
		},
		Checks:        s.Checks,
		Interventions: []fixSummaryJSON{},
		Summary:       s.Summary,
		Attention: attentionJSON{
			Interventions: []string{},
			Unscored:      nonNil(s.Attention.Unscored),
			Candidates:    nonNil(s.Attention.Candidates),
			Checks:        nonNil(s.Attention.Checks),
		},
		Telemetry:     s.Telemetry,
		Corpora:       nonNil(s.Corpora),
		Hooks:         nonNil(s.Hooks),
		Presets:       presetRows(s.Presets),
		Rules:         nonNil(s.Rules),
		Addenda:       nonNil(s.Addenda),
		Team:          s.Team,
		StoreProblems: nonNil(s.StoreProblems),
	}
	for _, r := range s.Posture.Resolver {
		out.Posture.Resolver = append(out.Posture.Resolver, resolverJSON{
			Harness: r.Harness, Scope: r.Scope, Registered: r.Registered,
			Effective: r.Effective, Trust: r.Trust, Resolvable: r.Resolvable,
		})
	}
	for _, iv := range s.Interventions {
		out.Interventions = append(out.Interventions, fixSummaryJSON{
			ID: iv.ID, Class: iv.Class, Problem: iv.Problem, Intervention: iv.Intervention,
			Tier: iv.Tier, Target: iv.Target, Status: iv.Status, HookID: iv.HookID,
			Scope: iv.Scope, Artifact: iv.Artifact, Corpus: iv.Corpus, Unit: iv.Unit,
			SignalKind: iv.SignalKind, Recurred: iv.Observation.Recurrence.Occurred,
			Before: iv.Before, After: iv.After,
		})
	}
	for _, iv := range s.Attention.Interventions {
		out.Attention.Interventions = append(out.Attention.Interventions, iv.ID)
	}
	return out
}

// presetRows folds the per-scope preset rows into one row per catalog preset.
func presetRows(in []PresetInfo) []presetJSON {
	byName := map[string]*presetJSON{}
	out := []presetJSON{}
	var names []string
	for _, p := range in {
		row, ok := byName[p.Name]
		if !ok {
			row = &presetJSON{Name: p.Name, Bundle: p.Bundle, Description: p.Description,
				EnabledScopes: []string{}, DriftedScopes: []string{}}
			byName[p.Name] = row
			names = append(names, p.Name)
		}
		if p.Enabled {
			row.EnabledScopes = append(row.EnabledScopes, p.Scope)
		}
		if p.Drifted {
			row.DriftedScopes = append(row.DriftedScopes, p.Scope)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		out = append(out, *byName[name])
	}
	return out
}

// nonNil returns an empty slice for nil, so the contract prints [] and never null.
func nonNil[T any](in []T) []T {
	if in == nil {
		return []T{}
	}
	return in
}
