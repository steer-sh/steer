// Package cadence is the deterministic tier of the steer feedback loop: it runs
// on a schedule, spends no model tokens, and writes a queue of work for the
// /steer-report skill to finish. It reads both miners — the commit-review ledger
// (via internal/gate/mine) and the transcript digest (via internal/report) — and
// emits the one candidate-record contract. The miners stay separate code; this
// orchestrator is the one place allowed to read both.
package cadence

import (
	"encoding/json"
	"regexp"
	"sort"

	"github.com/steer-sh/steer-cli/internal/gate"
	"github.com/steer-sh/steer-cli/internal/gate/mine"
	"github.com/steer-sh/steer-cli/internal/report"
)

// QueueVersion is the schema version of the queue file the skill reads. v6 adds
// checks: the open steps in the repository's check tier. v7 adds observation.checks
// to each intervention. v8 adds observation.checks.registered_host and other_host,
// and leaves a fix registered on another host out of the coverage queue. v9 adds
// artifact to each intervention, and queues a record whose fix is absent or unreadable.
// v10 adds artifact.removed, and queues a record whose fix the archive records as
// removed after its split. v11 removes the auto-denials corpus row.
const QueueVersion = 11

// maxTranscriptCandidates bounds the transcript half of the queue. A cluster key
// is the user's own wording, so it groups verbatim repeats and nothing else;
// grouping corrections into classes is the skill's job, and the queue carries the
// strongest rows rather than filtering on a count the key cannot earn.
const maxTranscriptCandidates = 25

// Corpus and unit tags of the contract.
const (
	corpusReviewFindings = "review-findings"
	corpusTranscript     = "transcript-friction"
)

// Incidence is a before/after chain count in the scored contract.
type Incidence struct {
	Hits  int     `json:"hits"`
	Units int     `json:"units"`
	Rate  float64 `json:"rate"`
}

// Intervention is a full contract record the deterministic tier already scored and
// flagged as needing attention. It carries a match, produced by register.
type Intervention struct {
	ID    string `json:"id"`
	Class string `json:"class"`
	// Problem and Intervention are the record's own explanation: what goes wrong,
	// and what this record does about it. The queue is read by a skill that was
	// not in the session that authored the class, so the explanation travels with
	// the work rather than being re-derived from the regex.
	Problem      string           `json:"problem"`
	Intervention string           `json:"intervention"`
	Match        string           `json:"match"`
	Corpus       string           `json:"corpus"`
	Unit         string           `json:"unit"`
	Tier         string           `json:"tier"`
	Target       string           `json:"target"`
	HookID       string           `json:"hook_id,omitempty"`
	Before       Incidence        `json:"before"`
	After        Incidence        `json:"after"`
	Observation  mine.Observation `json:"observation"`
	// Artifact is the fix the record binds and where it stands now, null on a
	// declined record.
	Artifact *Artifact `json:"artifact"`
	// Scope is the frozen eligibility rule, null on a legacy record.
	Scope *Scope `json:"scope"`
	// BeforeCoverage and AfterCoverage account for each window under that scope.
	BeforeCoverage Coverage `json:"before_coverage"`
	AfterCoverage  Coverage `json:"after_coverage"`
	// SignalKind is the classifier kind a session-unit class is counted over, empty
	// on a chain-unit record.
	SignalKind string `json:"signal_kind,omitempty"`
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
	// Checks are the open steps in the repository the refresh ran in, empty when
	// the caller named none.
	Checks []CheckWork `json:"checks"`
}

// Options are the inputs to a refresh. State dirs are passed in so a test can
// point them at fixtures; the command fills them from the gate config.
type Options struct {
	GateStateDir string // commit-review state: interventions.jsonl + ledger shards
	Ledger       string // single-ledger override, or "" for the shard glob
	WindowDays   int    // transcript look-back
	AllProjects  bool   // scan every project, not just the cwd
	// RepoRoot is the Git root whose check tier the queue reports, "" outside a
	// repository. The caller resolves it; Refresh runs no git command.
	RepoRoot string
	// Addendum is the review addendum the commit gate reads in RepoRoot, loaded by
	// the caller; nil leaves every review-tier artifact unread.
	Addendum *gate.Addendum
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
		Checks:        checkWork(opts.RepoRoot),
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
		scored, err := mine.ScoreWith(r, ev)
		if err != nil {
			continue
		}
		observation := observationOf(scored["observation"])
		artifact := ArtifactOf(r, ArtifactSources{RepoRoot: opts.RepoRoot, Addendum: opts.Addendum})
		if !needsAttention(observation, scored["scope"], artifact) {
			continue
		}
		prose := mine.ProseOf(r)
		q.Interventions = append(q.Interventions, Intervention{
			ID:           str(scored["id"]),
			Class:        str(scored["class"]),
			Problem:      prose.Problem,
			Intervention: prose.Intervention,
			Match:        str(r["match"]),
			Corpus:       mine.CorpusOf(r),
			Unit:         mine.UnitOf(r),
			Tier:         str(scored["tier"]),
			Target:       str(scored["target"]),
			HookID:       str(scored["hook_id"]),
			Before:       incidenceOf(scored["before"]),
			After:        incidenceOf(scored["after"]),
			Observation:  observation,
			Artifact:     artifact,

			Scope:          ScopeOf(scored["scope"]),
			BeforeCoverage: CoverageOf(scored["before_coverage"]),
			AfterCoverage:  CoverageOf(scored["after_coverage"]),
			SignalKind:     str(scored["signal_kind"]),
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
		transcript := make([]Candidate, 0, len(digest.Signals.UserCorrections))
		for _, c := range digest.Signals.UserCorrections {
			transcript = append(transcript, Candidate{
				Corpus:   corpusTranscript,
				Pattern:  c.Pattern,
				Count:    c.Count,
				Sessions: c.Sessions,
				Examples: c.Examples,
			})
		}
		sortByStrength(transcript)
		if len(transcript) > maxTranscriptCandidates {
			transcript = transcript[:maxTranscriptCandidates]
		}
		q.Candidates = append(q.Candidates, transcript...)
	}
	sortCandidates(q.Candidates)

	// --- corpus sweep: one row per corpus, present when it holds data ---------
	// The transcript corpus's size is the snapshot store, falling back to the scan.
	transcriptSize := len(ev.Sessions)
	if transcriptSize == 0 {
		transcriptSize = sessionsScanned(digest, derr)
	}
	q.Corpora = []CorpusStatus{
		{Corpus: corpusReviewFindings, Present: len(chains) > 0, Size: len(chains)},
		{Corpus: corpusTranscript, Present: transcriptSize > 0, Size: transcriptSize},
	}

	return q, nil
}

// needsAttention is field-derived. A recurrence, an unreadable guard audit log, a
// coverage cause, an unfrozen scope, a fix that is absent or unreadable, or a fix
// the archive records as removed after the split gives the reader something to
// inspect;
// quiet eligible work stays in the inventory without manufacturing a repair. A
// guard whose audit log is present and records no block is NOT attention: the
// zero is a reading, and the tier that writes no audit rows keeps Applicable
// false.
func needsAttention(obs mine.Observation, scope any, artifact *Artifact) bool {
	if obs.Recurrence.Occurred || artifact.Missing() || artifact.RemovalRecorded() {
		return true
	}
	if mine.MissingCoverage(obs) {
		return true
	}
	return scope == nil
}

func observationOf(v any) mine.Observation {
	if obs, ok := v.(mine.Observation); ok {
		return obs
	}
	data, err := json.Marshal(v)
	if err != nil {
		return mine.Observation{}
	}
	var obs mine.Observation
	if json.Unmarshal(data, &obs) != nil {
		return mine.Observation{}
	}
	return obs
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
		return strongerThan(cs[i], cs[j])
	})
}

// sortByStrength orders one corpus's candidates so a truncation drops the
// weakest rows rather than the alphabetically last ones.
func sortByStrength(cs []Candidate) {
	sort.SliceStable(cs, func(i, j int) bool { return strongerThan(cs[i], cs[j]) })
}

// strongerThan ranks by spread across sessions, then by raw count, and falls
// back to the pattern so a refresh over unchanged evidence stays byte-identical.
func strongerThan(a, b Candidate) bool {
	if a.Sessions != b.Sessions {
		return a.Sessions > b.Sessions
	}
	if a.Count != b.Count {
		return a.Count > b.Count
	}
	return a.Pattern < b.Pattern
}

func sessionsScanned(d *report.Digest, err error) int {
	if err != nil || d == nil {
		return 0
	}
	return d.Stats.SessionsScanned
}
