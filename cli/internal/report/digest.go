// Package report scans Claude Code session transcripts and CLAUDE.md files,
// extracting mechanical signals of repeated friction (user corrections, tool
// failures, rejections, hook blocks) into a stored report that a paged index
// and item details read. Semantic judgment of the report — which clusters are
// hookable, how to phrase rules — belongs to the model layer (the steer-report
// skill), not this package.
package report

import (
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/steer-sh/steer-cli/internal/harness"
)

// DigestVersion is the schema version of the stored report and of every page
// read from it. The steer-report skill prompt reads this field; bump it whenever
// the shape or semantics change.
const DigestVersion = 7

// DefaultMaxDigestBytes bounds one page of index, listing or detail output.
const DefaultMaxDigestBytes = 40960

const (
	maxOccurrenceLen = 1000
	clusterKeyLen    = 96
)

// Occurrence is one classified event of a cluster.
type Occurrence struct {
	Session   string `json:"session"`
	Timestamp string `json:"timestamp,omitempty"`
	Text      string `json:"text"`
	// PreviousHumanText is the human text observed last before a correction in
	// the same session and thread. It is context, never evidence: no key, count
	// or match reads it. Absent means none was observed within this scan.
	PreviousHumanText *ObservedText `json:"previous_human_text,omitempty"`
	// FirstObservedHumanTextInScan is the first human text observed in the same
	// session and thread within this scan. It is absent when it is the previous
	// human text, and it is never a filter or a rank.
	FirstObservedHumanTextInScan *ObservedText `json:"first_observed_human_text_in_scan,omitempty"`
	// Group numbers the send group of a repeated wording occurrence.
	Group int `json:"group,omitempty"`
	// AfterInterrupt marks a correction typed right after the person stopped a
	// running turn.
	AfterInterrupt bool `json:"after_interrupt,omitempty"`
	// RecentToolCalls is the tail of the tool calls the same session and thread
	// requested since the previous real human turn. It is context, never
	// evidence, and absent when none was observed.
	RecentToolCalls *RecentToolCalls `json:"recent_tool_calls,omitempty"`
}

// RecentToolCalls is a bounded tail of requested tool calls, oldest first.
// Omitted counts the earlier calls of the same interval the bound dropped.
type RecentToolCalls struct {
	Calls   []RecentToolCall `json:"calls"`
	Omitted int              `json:"omitted,omitempty"`
}

// RecentToolCall is one requested tool call and what its result showed before
// the human turn. Target is the normalized subject, a path or a shell command
// head, cut to a bound; it is empty when the harness recorded none. Result is
// one of the toolResult* states.
type RecentToolCall struct {
	Tool   string `json:"tool"`
	Target string `json:"target,omitempty"`
	Cut    bool   `json:"cut,omitempty"`
	Result string `json:"result"`
}

// Result states of a RecentToolCall. toolResultNonError is an observed result
// the harness did not mark as an error, not a verified success.
const (
	toolResultUnobserved = "unobserved"
	toolResultError      = "error"
	toolResultNonError   = "non_error"
	toolResultDeclined   = "declined"
)

// ObservedText is human text shown as context, cut to a bound.
type ObservedText struct {
	Text string `json:"text"`
	Cut  bool   `json:"cut,omitempty"`
}

// Cluster aggregates one recurring signal across sessions.
type Cluster struct {
	// ID names the cluster within one stored report.
	ID          string       `json:"id"`
	Pattern     string       `json:"pattern"`
	Count       int          `json:"count"`
	Sessions    int          `json:"sessions"`
	FirstSeen   string       `json:"first_seen,omitempty"`
	LastSeen    string       `json:"last_seen,omitempty"`
	Occurrences []Occurrence `json:"occurrences"`
	// SendGroups counts the send groups of a repeated wording: identical text
	// sent to several sessions within sendGroupWindow is one group. A group is a
	// heuristic, not a proven duplicate.
	SendGroups int `json:"send_groups,omitempty"`

	sessionIDs map[string]struct{}
}

// Score ranks clusters: repetition spread across sessions outranks repetition
// within one.
func (c *Cluster) Score() int {
	return c.Count * c.Sessions
}

// Signals groups clusters by extraction kind.
type Signals struct {
	// UserCorrections are heuristic correction candidates: user turns whose text
	// matches correction language ("don't use X", "I said use Y", "stop adding", ...).
	// Precision is checked by hand; recall is unmeasured.
	UserCorrections []*Cluster `json:"user_corrections"`
	// UserRejections are tool calls the user declined at the permission prompt.
	// Only Claude Code's decline result is recognized.
	UserRejections []*Cluster `json:"user_rejections"`
	// AfterInterrupt is the message typed right after the person stopped a
	// running turn, when no correction pattern matched it. Most are redirections
	// or continuations, not corrections. It is report-only: no session snapshot
	// or fix score reads it.
	AfterInterrupt []*Cluster `json:"after_interrupt"`
	// RepeatedWording is typed human text that recurs verbatim, after whitespace
	// normalization, in two or more sessions. It is report-only: no session
	// snapshot or fix score reads it.
	RepeatedWording []*Cluster `json:"repeated_wording"`
	// ToolFailures are tool results matching lint/test/compile failure patterns.
	ToolFailures []*Cluster `json:"tool_failures"`
	// HookBlocks are operations blocked by an existing PreToolUse hook —
	// evidence of enforcement already working, useful for dedup.
	HookBlocks []*Cluster `json:"hook_blocks"`
}

// signalList is one signal kind with the id prefix its clusters carry. The
// index lists the rows of a kind with inIndex set; `steer report show <signal>`
// reads the others.
type signalList struct {
	kind    string
	prefix  string
	items   []*Cluster
	inIndex bool
}

// lists returns the signal kinds in index order.
func (s Signals) lists() []signalList {
	return []signalList{
		{kindCorrection, "c", s.UserCorrections, true},
		{kindRejection, "r", s.UserRejections, true},
		{kindAfterInterrupt, "a", s.AfterInterrupt, true},
		{kindRepeatedWording, "w", s.RepeatedWording, true},
		{kindFailure, "f", s.ToolFailures, false},
		{kindHookBlock, "h", s.HookBlocks, false},
	}
}

// Directive is an instruction-file bullet outside the Steer Rules section — a standing
// instruction not yet backed by a deterministic hook.
type Directive struct {
	Section string `json:"section"`
	Text    string `json:"text"`
}

// InstructionFile summarizes one harness instruction document — CLAUDE.md or
// AGENTS.md: the lines Steer wrote, the subset a guard enforces, and
// directives that are candidates for enforcement.
type InstructionFile struct {
	// ID names the document within one stored report.
	ID      string     `json:"id"`
	Harness harness.ID `json:"harness"`
	Path    string     `json:"path"`
	// SteerRules is every line of the Steer Rules section. A guard writes its
	// rule there and so does an instruction rule, so a line here is not proof of
	// enforcement.
	SteerRules []string `json:"steer_rules"`
	// GuardRules are the SteerRules lines whose text is a guard's rule in the
	// manifest of the document's scope.
	GuardRules []string    `json:"guard_rules"`
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

// Stats reports scan coverage.
type Stats struct {
	FilesScanned    int `json:"files_scanned"`
	SessionsScanned int `json:"sessions_scanned"`
	// ThreadsScanned counts subagent threads seen under those sessions. A
	// thread is provenance, never a unit: it is reported and never counted as
	// a session.
	ThreadsScanned int               `json:"threads_scanned"`
	BytesScanned   int64             `json:"bytes_scanned"`
	EntriesScanned int               `json:"entries_scanned"`
	EntriesSkipped int               `json:"entries_skipped"`
	ClustersFound  int               `json:"clusters_found"`
	Harnesses      []HarnessCoverage `json:"harnesses"`
}

// Digest is the stored report: every cluster with every occurrence. The index
// and `steer report show` read it; nothing reads it whole into a session.
type Digest struct {
	DigestVersion int `json:"digest_version"`
	// ReportID names one stored scan, so a page or a detail request can refuse
	// to mix evidence from two scans.
	ReportID         string            `json:"report_id"`
	GeneratedAt      string            `json:"generated_at"`
	WindowDays       int               `json:"window_days"`
	ProjectsScanned  []string          `json:"projects_scanned"`
	Stats            Stats             `json:"stats"`
	InstructionFiles []InstructionFile `json:"instruction_files"`
	Signals          Signals           `json:"signals"`
}

// assignIDs numbers every cluster by its rank within its signal, and every
// instruction file by its position.
func (d *Digest) assignIDs() {
	for _, list := range d.Signals.lists() {
		for i, c := range list.items {
			c.ID = list.prefix + strconv.Itoa(i+1)
		}
	}
	for i := range d.InstructionFiles {
		d.InstructionFiles[i].ID = "i" + strconv.Itoa(i+1)
	}
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
			AfterInterrupt:  []*Cluster{},
			RepeatedWording: []*Cluster{},
			ToolFailures:    []*Cluster{},
			HookBlocks:      []*Cluster{},
		},
		InstructionFiles: []InstructionFile{},
		ProjectsScanned:  []string{},
	}
}

// clip cuts s to at most n bytes without splitting a UTF-8 sequence.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[:n]
	for i := len(s) - 1; i >= 0 && i >= len(s)-utf8.UTFMax; i-- {
		if utf8.RuneStart(s[i]) {
			if !utf8.FullRuneInString(s[i:]) {
				s = s[:i]
			}
			break
		}
	}
	return s
}

// distinctSessions counts the sessions a set of clusters occurred in.
func distinctSessions(clusters []*Cluster) int {
	seen := map[string]struct{}{}
	for _, c := range clusters {
		for _, o := range c.Occurrences {
			seen[o.Session] = struct{}{}
		}
	}
	return len(seen)
}
