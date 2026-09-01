// Package cadence is the deterministic tier of the steer feedback loop: it runs
// on a schedule, spends no model tokens, and writes a queue of work for the
// /steer-report skill to finish. It reads both miners — the commit-review ledger
// (via internal/gate/mine) and the transcript digest (via internal/report) — and
// emits the one candidate-record contract. The miners stay separate code; this
// orchestrator is the one place allowed to read both.
package cadence

import (
	"regexp"
	"sort"

	"github.com/steer-sh/steer-cli/internal/denials"
	"github.com/steer-sh/steer-cli/internal/gate/mine"
	"github.com/steer-sh/steer-cli/internal/report"
)

// QueueVersion is the schema version of the queue file the skill reads.
const QueueVersion = 3

// minCandidateSessions is the deterministic threshold on transcript clusters: a
// correction pattern must repeat across this many sessions to become a candidate.
const minCandidateSessions = 3

// Corpus and unit tags of the contract.
const (
	corpusReviewFindings = "review-findings"
	corpusTranscript     = "transcript-friction"
	corpusDenials        = "auto-denials"
)

// Attribution is the guard audit join carried beside a session-scored verdict.
type Attribution struct {
	Applicable bool   `json:"applicable"`
	LogPresent bool   `json:"log_present"`
	HookID     string `json:"hook_id,omitempty"`
	Firings    int    `json:"firings"`
	Sessions   int    `json:"sessions"`
}

// Incidence is a before/after chain count in the scored contract.
type Incidence struct {
	Hits  int     `json:"hits"`
	Units int     `json:"units"`
	Rate  float64 `json:"rate"`
}

// Intervention is a full contract record the deterministic tier already scored and
// flagged as needing attention. It carries a match, produced by register.
type Intervention struct {
	ID      string    `json:"id"`
	Class   string    `json:"class"`
	Match   string    `json:"match"`
	Corpus  string    `json:"corpus"`
	Unit    string    `json:"unit"`
	Tier    string    `json:"tier"`
	Target  string    `json:"target"`
	HookID  string    `json:"hook_id,omitempty"`
	Verdict string    `json:"verdict"`
	Before  Incidence `json:"before"`
	After   Incidence `json:"after"`
	// Scope is the frozen eligibility rule, null on a legacy record.
	Scope *Scope `json:"scope"`
	// BeforeCoverage and AfterCoverage account for each window under that scope.
	BeforeCoverage Coverage `json:"before_coverage"`
	AfterCoverage  Coverage `json:"after_coverage"`
	// Effect is the relative-risk range; RequiredAfter is the clean eligible
	// after-window that could still reach the effect target, null when none can.
	Effect        Effect `json:"effect"`
	RequiredAfter *int   `json:"required_after"`
	// Confounds withhold the verdict; Composition only warns beside it.
	Confounds   []mine.Confound `json:"confounds"`
	Composition []mine.Confound `json:"composition"`
	// SignalKind is the classifier kind a session-unit class is counted over, empty
	// on a chain-unit record.
	SignalKind string `json:"signal_kind,omitempty"`
	// Attribution is the guard's audit join over the after-window. Applicable is
	// false wherever the tier writes no audit rows, and on every chain-unit record.
	Attribution Attribution `json:"attribution"`
}

// Candidate is a pointer to a new cluster the model must cluster, phrase, and author
// a regex for. It carries no match — that is model judgment reserved for the skill.
type Candidate struct {
	Corpus   string   `json:"corpus"`
	Pattern  string   `json:"pattern,omitempty"`
	Count    int      `json:"count,omitempty"`
	Sessions int      `json:"sessions,omitempty"`
	Examples []string `json:"examples,omitempty"`
	// Review-findings pointer variant: no per-class regex exists yet, so the row
	// points at the findings dump and counts the chains no class has decided.
	UndecidedChains int    `json:"undecided_chains,omitempty"`
	Dump            string `json:"dump,omitempty"`
}

// CorpusStatus reports one corpus's presence and size on this machine. A corpus
// absent here (an adopter with no ledger) still emits a row, never an error.
type CorpusStatus struct {
	Corpus  string `json:"corpus"`
	Present bool   `json:"present"`
	Size    int    `json:"size"`
}

// Queue is the deterministic tier's output: the work the skill finishes. Its
// content is deterministic — two refreshes over the same evidence produce byte-
// identical queues, so the timestamp lives in a sibling file, not here.
type Queue struct {
	Version       int            `json:"version"`
	Interventions []Intervention `json:"interventions"`
	Unscored      []string       `json:"unscored"`
	Candidates    []Candidate    `json:"candidates"`
	Corpora       []CorpusStatus `json:"corpora"`
}

// Options are the inputs to a refresh. State dirs are passed in so a test can
// point them at fixtures; the command fills them from the gate config.
type Options struct {
	GateStateDir string // commit-review state: interventions.jsonl + ledger shards
	Ledger       string // single-ledger override, or "" for the shard glob
	WindowDays   int    // transcript look-back
	AllProjects  bool   // scan every project, not just the cwd
	MinAfter     int    // optional floor on eligible after-units; 0 = no floor
	// Evidence is every corpus a scoring pass reads, gathered by the caller. A caller
	// that leaves it zero scores the ledger alone; the session engine then reports
	// missing evidence rather than a range it cannot support.
	Evidence mine.EvidenceSet
}

// Refresh runs the deterministic tier and returns the queue. It never calls a
// model. Errors from one corpus do not abort the others: a machine missing a
// ledger still gets its transcript half.
func Refresh(opts Options) (*Queue, error) {
	q := &Queue{
		Version:       QueueVersion,
		Interventions: []Intervention{},
		Unscored:      []string{},
		Candidates:    []Candidate{},
		Corpora:       []CorpusStatus{},
	}

	// --- score every record an engine reads ----------------------------------
	records, _ := mine.Load(opts.GateStateDir)
	ev := opts.Evidence
	if ev.Chains == nil {
		rows, _ := mine.LoadRows(opts.GateStateDir, opts.Ledger)
		ev.Chains = mine.BuildChains(rows, false)
	}
	chains := ev.Chains

	for _, r := range records {
		status := str(r["status"])
		if status != "active" && status != "rejected" {
			continue // reworked/removed are history, not pending work
		}
		// A record from a corpus no engine reads is routed to unscored rather than
		// scored against evidence it was never authored over.
		if !mine.Scorable(r) {
			q.Unscored = append(q.Unscored, str(r["id"]))
			continue
		}
		scored, err := mine.ScoreWith(r, ev, opts.MinAfter)
		if err != nil {
			continue
		}
		verdict := str(scored["verdict"])
		if !needsAttention(verdict) {
			continue
		}
		q.Interventions = append(q.Interventions, Intervention{
			ID:      str(scored["id"]),
			Class:   str(scored["class"]),
			Match:   str(r["match"]),
			Corpus:  mine.CorpusOf(r),
			Unit:    mine.UnitOf(r),
			Tier:    str(scored["tier"]),
			Target:  str(scored["target"]),
			HookID:  str(scored["hook_id"]),
			Verdict: verdict,
			Before:  incidenceOf(scored["before"]),
			After:   incidenceOf(scored["after"]),

			Scope:          ScopeOf(scored["scope"]),
			BeforeCoverage: CoverageOf(scored["before_coverage"]),
			AfterCoverage:  CoverageOf(scored["after_coverage"]),
			Effect:         EffectOf(scored["effect"]),
			RequiredAfter:  RequiredAfterOf(scored["required_after"]),
			Confounds:      ConfoundsOf(scored["confounds"]),
			Composition:    ConfoundsOf(scored["composition"]),
			SignalKind:     str(scored["signal_kind"]),
			Attribution:    AttributionOf(scored["attribution"]),
		})
	}
	sort.Slice(q.Interventions, func(i, j int) bool { return q.Interventions[i].ID < q.Interventions[j].ID })
	sort.Strings(q.Unscored)

	// --- review-findings candidate pointer: undecided chains + dump -----------
	undecided := undecidedChains(chains, records)
	if undecided > 0 {
		q.Candidates = append(q.Candidates, Candidate{
			Corpus:          corpusReviewFindings,
			UndecidedChains: undecided,
			Dump:            "steer commit-gate mine findings",
		})
	}

	// --- transcript-friction candidates: mechanical clusters over threshold ---
	digest, derr := report.Generate(opts.WindowDays, opts.AllProjects)
	if derr == nil {
		for _, c := range digest.Signals.UserCorrections {
			if c.Sessions < minCandidateSessions {
				continue
			}
			q.Candidates = append(q.Candidates, Candidate{
				Corpus:   corpusTranscript,
				Pattern:  c.Pattern,
				Count:    c.Count,
				Sessions: c.Sessions,
				Examples: c.Examples,
			})
		}
	}
	sortCandidates(q.Candidates)

	// --- corpus sweep: one row per corpus, present when it holds data ---------
	// The transcript corpus's size is the snapshot store, falling back to the scan.
	transcriptSize := len(ev.Sessions)
	if transcriptSize == 0 {
		transcriptSize = sessionsScanned(digest, derr)
	}
	denialCount := denialsSize()
	q.Corpora = []CorpusStatus{
		{Corpus: corpusReviewFindings, Present: len(chains) > 0, Size: len(chains)},
		{Corpus: corpusTranscript, Present: transcriptSize > 0, Size: transcriptSize},
		{Corpus: corpusDenials, Present: denialCount > 0, Size: denialCount},
	}

	return q, nil
}

// AttentionVerdicts are the scored verdicts the cadence queues for action. Every
// consumer that groups the queue by verdict must cover this set.
var AttentionVerdicts = []string{
	"harmful", "ineffective", "not-triggered", "unattributed", "class-grew",
	"legacy-unscoped", "evidence-incomplete", "confounded",
}

// needsAttention is the deterministic threshold on scored interventions: a hook that
// did not reduce its class, a refusal a growing class overtook, or a record whose
// measurement conditions are invalid — no scope, missing evidence, or a confound.
func needsAttention(verdict string) bool {
	for _, v := range AttentionVerdicts {
		if v == verdict {
			return true
		}
	}
	return false
}

// undecidedChains counts chains carrying an accepted finding that no live class
// decision covers. Only a defect class (kind accepted) can decide an accepted
// finding — a false-positive class decides rejected findings and leaves the
// accepted defect undecided. Only a live decision counts: active or rejected, not
// a removed or reworked record whose class no longer stands. It is an upper bound
// the skill refines by clustering the dump.
func undecidedChains(chains []mine.Chain, records []mine.Record) int {
	matchAll := regexp.MustCompile("")
	var deciding []*regexp.Regexp
	for _, r := range records {
		status := str(r["status"])
		if status != "active" && status != "rejected" {
			continue
		}
		if mine.RecordKindFilter(r) != "accepted" {
			continue
		}
		re, err := regexp.Compile("(?i)" + str(r["match"]))
		if err != nil {
			continue
		}
		deciding = append(deciding, re)
	}
	count := 0
	for _, ch := range chains {
		accepted := mine.ChainHits(ch, matchAll, "accepted")
		if len(accepted) == 0 {
			continue
		}
		// The chain is undecided if any one of its accepted findings is covered by
		// no deciding class — a residual unclassified defect the skill must cluster.
		// One classified finding does not decide the whole chain.
		if hasUnclassified(accepted, deciding) {
			count++
		}
	}
	return count
}

// hasUnclassified reports whether any finding is matched by none of the regexes.
func hasUnclassified(findings []mine.Finding, deciding []*regexp.Regexp) bool {
	for _, f := range findings {
		matched := false
		for _, re := range deciding {
			if re.MatchString(f.Text) {
				matched = true
				break
			}
		}
		if !matched {
			return true
		}
	}
	return false
}

func incidenceOf(v any) Incidence {
	m, _ := v.(map[string]any)
	if m == nil {
		return Incidence{}
	}
	return Incidence{Hits: numInt(m["hits"]), Units: numInt(m["chains"]), Rate: numFloat(m["rate"])}
}

func sortCandidates(cs []Candidate) {
	sort.SliceStable(cs, func(i, j int) bool {
		if cs[i].Corpus != cs[j].Corpus {
			return cs[i].Corpus < cs[j].Corpus
		}
		return cs[i].Pattern < cs[j].Pattern
	})
}

func sessionsScanned(d *report.Digest, err error) int {
	if err != nil || d == nil {
		return 0
	}
	return d.Stats.SessionsScanned
}

func denialsSize() int {
	rows, err := denials.Read()
	if err != nil {
		return 0
	}
	return len(rows)
}
