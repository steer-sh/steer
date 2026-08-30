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
const QueueVersion = 1

// minCandidateSessions is the deterministic threshold on transcript clusters: a
// correction pattern must repeat across this many sessions to become a candidate.
const minCandidateSessions = 3

// Corpus and unit tags of the contract.
const (
	corpusReviewFindings = "review-findings"
	corpusTranscript     = "transcript-friction"
	corpusDenials        = "auto-denials"
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
	MinAfter     int    // optional floor on eligible after-chains; 0 = no floor
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

	// --- review-findings corpus: score what the local engine can score --------
	records, _ := mine.Load(opts.GateStateDir)
	rows, _ := mine.LoadRows(opts.GateStateDir, opts.Ledger)
	chains := mine.BuildChains(rows, false)

	for _, r := range records {
		status := str(r["status"])
		if status != "active" && status != "rejected" {
			continue // reworked/removed are history, not pending work
		}
		// The engine reads only the commit-review ledger by chain. A session-unit
		// intervention needs the audit-log join, which is unbuilt, so it is
		// reported unscored rather than scored against the wrong corpus.
		if str(r["unit"]) == "session" {
			q.Unscored = append(q.Unscored, str(r["id"]))
			continue
		}
		scored, err := mine.ScoreRecord(r, chains, opts.MinAfter)
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
			Corpus:  strOr(r["corpus"], corpusReviewFindings),
			Unit:    strOr(r["unit"], mine.UnitChain),
			Tier:    str(scored["tier"]),
			Target:  str(scored["target"]),
			HookID:  str(scored["hook_id"]),
			Verdict: verdict,
			Before:  incidenceOf(scored["before"]),
			After:   incidenceOf(scored["after"]),
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
	transcriptSize := sessionsScanned(digest, derr)
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
var AttentionVerdicts = []string{"harmful", "ineffective", "class-grew", "legacy-unscoped"}

// needsAttention is the deterministic threshold on scored interventions: a hook that
// did not reduce its class, a refusal a growing class overtook, or a record carrying no
// scope to measure against.
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
