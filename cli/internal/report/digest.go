// Package report scans Claude Code session transcripts and CLAUDE.md files,
// extracting mechanical signals of repeated friction (user corrections, tool
// failures, rejections, hook blocks) into a compact size-bounded JSON digest.
// Semantic judgment of the digest — which clusters are hookable, how to phrase
// rules — belongs to the model layer (the steer-report skill), not this package.
package report

import (
	"encoding/json"
	"sort"
	"time"

	"github.com/steer-sh/steer-cli/internal/harness"
)

// DigestVersion is the schema version of the emitted digest. The steer-report
// skill prompt reads this field; bump it whenever the shape or semantics of
// the digest change.
const DigestVersion = 2

// DefaultMaxDigestBytes caps the marshaled digest so it can be read into a
// session without crowding the context window.
const DefaultMaxDigestBytes = 40960

const (
	maxExamplesPerCluster = 3
	maxExampleLen         = 280
	clusterKeyLen         = 96
)

// Cluster aggregates one recurring signal across sessions.
type Cluster struct {
	Pattern   string   `json:"pattern"`
	Count     int      `json:"count"`
	Sessions  int      `json:"sessions"`
	FirstSeen string   `json:"first_seen,omitempty"`
	LastSeen  string   `json:"last_seen,omitempty"`
	Examples  []string `json:"examples"`

	sessionIDs map[string]struct{}
}

// Score ranks clusters for eviction: repetition spread across sessions
// outranks repetition within one.
func (c *Cluster) Score() int {
	return c.Count * c.Sessions
}

// Signals groups clusters by extraction kind.
type Signals struct {
	// UserCorrections are user turns whose text matches correction language
	// ("don't use X", "I said", "stop adding", ...).
	UserCorrections []*Cluster `json:"user_corrections"`
	// UserRejections are tool calls the user declined at the permission prompt.
	UserRejections []*Cluster `json:"user_rejections"`
	// ToolFailures are tool results matching lint/test/compile failure patterns.
	ToolFailures []*Cluster `json:"tool_failures"`
	// HookBlocks are operations blocked by an existing PreToolUse hook —
	// evidence of enforcement already working, useful for dedup.
	HookBlocks []*Cluster `json:"hook_blocks"`
}

// Directive is an instruction-file bullet outside the Steer Rules section — a standing
// instruction not yet backed by a deterministic hook.
type Directive struct {
	Section string `json:"section"`
	Text    string `json:"text"`
}

// InstructionFile summarizes one harness instruction document — CLAUDE.md or
// AGENTS.md: rules already enforced by steer, and directives that are
// candidates for enforcement.
type InstructionFile struct {
	Harness    harness.ID  `json:"harness"`
	Path       string      `json:"path"`
	SteerRules []string    `json:"steer_rules"`
	Directives []Directive `json:"directives"`
}

// HarnessCoverage is one harness's share of the scan. It answers what the
// totals cannot: whether a harness contributed no evidence because it has none
// on this machine, or because the scan never reached it.
type HarnessCoverage struct {
	Harness         harness.ID `json:"harness"`
	FilesScanned    int        `json:"files_scanned"`
	SessionsScanned int        `json:"sessions_scanned"`
	ThreadsScanned  int        `json:"threads_scanned"`
	BytesScanned    int64      `json:"bytes_scanned"`
	EntriesScanned  int        `json:"entries_scanned"`
	EntriesSkipped  int        `json:"entries_skipped"`
}

// Stats reports scan coverage and honesty about truncation.
type Stats struct {
	FilesScanned    int `json:"files_scanned"`
	SessionsScanned int `json:"sessions_scanned"`
	// ThreadsScanned counts subagent threads seen under those sessions. A
	// thread is provenance, never a unit: it is reported and never counted as
	// a session.
	ThreadsScanned  int               `json:"threads_scanned"`
	BytesScanned    int64             `json:"bytes_scanned"`
	EntriesScanned  int               `json:"entries_scanned"`
	EntriesSkipped  int               `json:"entries_skipped"`
	ClustersFound   int               `json:"clusters_found"`
	ClustersEmitted int               `json:"clusters_emitted"`
	Truncated       bool              `json:"truncated"`
	Harnesses       []HarnessCoverage `json:"harnesses"`
}

// Digest is the JSON contract between the scanner and the steer-report skill.
type Digest struct {
	DigestVersion    int               `json:"digest_version"`
	GeneratedAt      string            `json:"generated_at"`
	WindowDays       int               `json:"window_days"`
	ProjectsScanned  []string          `json:"projects_scanned"`
	Stats            Stats             `json:"stats"`
	InstructionFiles []InstructionFile `json:"instruction_files"`
	Signals          Signals           `json:"signals"`
}

// Marshal renders the digest within maxBytes, evicting the lowest-scoring
// signal clusters until it fits. InstructionFiles are exempt from eviction:
// they are small and the highest-precision source. Stats.Truncated and
// ClustersEmitted are updated to reflect what survived.
func (d *Digest) Marshal(maxBytes int) ([]byte, error) {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxDigestBytes
	}

	lists := []*[]*Cluster{
		&d.Signals.UserCorrections,
		&d.Signals.UserRejections,
		&d.Signals.ToolFailures,
		&d.Signals.HookBlocks,
	}

	for _, list := range lists {
		sort.SliceStable(*list, func(i, j int) bool {
			return (*list)[i].Score() > (*list)[j].Score()
		})
	}

	d.Stats.ClustersFound = 0
	for _, list := range lists {
		d.Stats.ClustersFound += len(*list)
	}

	for {
		d.Stats.ClustersEmitted = 0
		for _, list := range lists {
			d.Stats.ClustersEmitted += len(*list)
		}
		d.Stats.Truncated = d.Stats.ClustersEmitted < d.Stats.ClustersFound

		data, err := json.MarshalIndent(d, "", "  ")
		if err != nil {
			return nil, err
		}
		if len(data) <= maxBytes {
			return data, nil
		}

		if !evictLowest(lists) {
			// Nothing left to evict; emit oversized rather than empty.
			return data, nil
		}
	}
}

// evictLowest removes the single lowest-scoring cluster across all lists.
// Lists are already sorted descending, so the candidate is each list's tail.
func evictLowest(lists []*[]*Cluster) bool {
	victim := -1
	victimScore := 0
	for i, list := range lists {
		n := len(*list)
		if n == 0 {
			continue
		}
		score := (*list)[n-1].Score()
		if victim == -1 || score < victimScore {
			victim = i
			victimScore = score
		}
	}
	if victim == -1 {
		return false
	}
	l := lists[victim]
	*l = (*l)[:len(*l)-1]
	return true
}

// NewDigest returns an empty digest stamped with the current time.
func NewDigest(windowDays int) *Digest {
	return &Digest{
		DigestVersion: DigestVersion,
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339),
		WindowDays:    windowDays,
		Signals: Signals{
			UserCorrections: []*Cluster{},
			UserRejections:  []*Cluster{},
			ToolFailures:    []*Cluster{},
			HookBlocks:      []*Cluster{},
		},
		InstructionFiles: []InstructionFile{},
		ProjectsScanned:  []string{},
	}
}
