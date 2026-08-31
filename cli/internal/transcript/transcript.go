// Package transcript is the harness-neutral session contract. A consumer that
// classifies evidence reads these types; only an adapter under this package
// knows a harness's on-disk format.
package transcript

import (
	"time"

	"github.com/steer-sh/steer-cli/internal/harness"
)

// Kind names what a normalized event is. The set is deliberately small: it
// covers what every adapter can produce from its own transcript, not the union
// of what each harness records.
type Kind string

const (
	// KindHumanTurn is text a person typed. It never includes text the harness
	// wrote into a user turn on the person's behalf.
	KindHumanTurn Kind = "human_turn"
	// KindToolCall is the model asking for a tool.
	KindToolCall Kind = "tool_call"
	// KindToolResult is what came back, successful or not.
	KindToolResult Kind = "tool_result"
)

// Session is one root session's transcript. The root session is the statistical
// unit; a subagent thread is provenance carried on the events.
type Session struct {
	Harness harness.ID
	// Path is the transcript file.
	Path string
	// Project groups sessions by the repository or directory they ran in, in
	// whatever form the harness names it on disk.
	Project string
}

// Event is one normalized transcript entry.
type Event struct {
	Kind Kind
	// SessionID is the root session id, the unit signals are counted over.
	SessionID string
	// ThreadID names a subagent thread and is empty on the root thread.
	ThreadID string
	// Timestamp is the harness's own RFC3339 string, kept verbatim so ordering
	// and digest fields do not depend on this package's parsing.
	Timestamp string
	// Text is the human turn, or a tool result flattened to plain text.
	Text string
	// Meta marks an event the harness generated rather than the person, such as
	// a session-resume banner.
	Meta bool
	// Tool names the tool for a call or a result.
	Tool string
	// Target is the tool's subject: a shell command, or a file path.
	Target string
	// ToolUseID joins a result back to its call.
	ToolUseID string
	// IsError reports a failed tool result.
	IsError bool
}

// Filter narrows both discovery and streaming. Since drops sessions last
// modified before it and events recorded before it.
type Filter struct {
	Since time.Time
	// Projects lists the project paths to keep. An empty list with AllProjects
	// false selects nothing.
	Projects []string
	// AllProjects ignores Projects and keeps every session.
	AllProjects bool
}

// Counts is one session's scan coverage, reported by the adapter because only
// it knows how many raw entries a normalized event stood for.
type Counts struct {
	// Entries is the number of raw entries normalized. One entry can emit more
	// than one event, so this is a count of source rows, not of events.
	Entries int
	// Skipped is the number of raw entries the adapter could not normalize.
	Skipped int
	// Bytes is the raw size read.
	Bytes int64
}

// Add accumulates another session's counts.
func (c *Counts) Add(other Counts) {
	c.Entries += other.Entries
	c.Skipped += other.Skipped
	c.Bytes += other.Bytes
}

// Adapter reads one harness's transcripts and emits normalized events.
type Adapter interface {
	// Harness names the harness this adapter reads.
	Harness() harness.ID
	// Root is the directory holding this harness's transcripts.
	Root() (string, error)
	// Discover lists the sessions under root that pass the filter.
	Discover(root string, f Filter) ([]Session, error)
	// Stream normalizes one session, calling emit in file order. A parse error
	// on one entry increments Skipped; it never ends the stream.
	Stream(s Session, f Filter, emit func(Event) error) (Counts, error)
}
