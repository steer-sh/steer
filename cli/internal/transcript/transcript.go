// Package transcript is the harness-neutral session contract. A consumer that
// classifies evidence reads these types; only an adapter under this package
// knows a harness's on-disk format.
package transcript

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
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
	// Cwd is the working directory the session was launched from, absolute and
	// in the harness's own form. An adapter reads it at discovery, before any
	// time window applies, so it names where the session started rather than
	// where it stood when the window opened. A harness that records none, or a
	// transcript whose header cannot be read, leaves it empty.
	Cwd string
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
	// Shell reports that a tool call ran a shell command. A classifier reads
	// this instead of the tool's name, which differs per harness.
	Shell bool
	// Target is the tool's subject: a shell command, or a file path.
	Target string
	// ToolUseID joins a result back to its call.
	ToolUseID string
	// IsError reports a failed tool result.
	IsError bool
	// Cwd is the working directory the harness recorded for this entry, absolute
	// and in the harness's own form. It is session metadata rather than a
	// classifier input, so ReplayDigest does not hash it and stamping it moves no
	// adapter's instrument. A harness that records none leaves it empty.
	Cwd string
}

// NormalizeProject collapses a path or a harness's munged form of one to a
// canonical key: every non-alphanumeric byte becomes '-'. Harnesses name a
// project on disk in undocumented ways, so both sides of a comparison are
// normalized rather than one side being trusted.
func NormalizeProject(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	return b.String()
}

// Filter narrows both discovery and streaming. Since drops sessions last
// modified before it and events recorded before it.
//
// Three scopes are mutually exclusive, in the order Selector tests them:
// RepoRoot selects one checkout, AllProjects selects everything, and Projects
// selects the listed directories.
type Filter struct {
	Since time.Time
	// Projects lists the project paths to keep. An empty list with AllProjects
	// false selects nothing.
	Projects []string
	// AllProjects ignores Projects and keeps every session.
	AllProjects bool
	// RepoRoot is one canonical repository root. When it is set, a session is
	// kept only when ResolveRoot maps its launch directory to exactly this
	// root, which excludes a sibling worktree, a nested repository and a
	// submodule even when their paths sit under it.
	RepoRoot string
	// ResolveRoot maps a launch directory to its canonical repository root, and
	// returns "" when the directory is in no repository. RepoRoot needs it; the
	// caller owns it so this package stays free of git.
	ResolveRoot func(cwd string) string
}

// Selector returns this filter's scope as one predicate, with the project key
// set built once. Every adapter calls it rather than testing the scope itself,
// so discovery for a report and for the session corpus select the same sessions.
func (f Filter) Selector() func(Session) bool {
	if f.RepoRoot != "" {
		resolve := f.ResolveRoot
		return func(s Session) bool {
			if s.Cwd == "" || resolve == nil {
				return false
			}
			return resolve(s.Cwd) == f.RepoRoot
		}
	}
	if f.AllProjects {
		return func(Session) bool { return true }
	}
	want := map[string]struct{}{}
	for _, p := range f.Projects {
		want[NormalizeProject(p)] = struct{}{}
	}
	return func(s Session) bool {
		_, ok := want[NormalizeProject(s.Project)]
		return ok
	}
}

// ScopesRepo reports that this filter selects one checkout. An adapter reads it
// to decide whether a session's launch directory is worth the read it costs.
func (f Filter) ScopesRepo() bool { return f.RepoRoot != "" }

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
	// Instrument identifies this adapter's normalization, as "<harness>:<digest>".
	// The adapter decides Kind, Shell, Target and IsError, which are the fields a
	// classifier reads, so a change here moves signals the same way a classifier
	// change does. The value is a declared constant that ReplayDigest pins.
	Instrument() string
	// Root is the directory holding this harness's transcripts.
	Root() (string, error)
	// Discover lists the sessions under root that pass the filter.
	Discover(root string, f Filter) ([]Session, error)
	// Stream normalizes one session, calling emit in file order. A parse error
	// on one entry increments Skipped; it never ends the stream.
	Stream(s Session, f Filter, emit func(Event) error) (Counts, error)
}

// ReplayDigest is the digest of what an adapter emits over a fixed set of
// transcript files. An adapter's Instrument constant carries this value over its
// committed fixtures. It replays Stream only, never Discover.
func ReplayDigest(a Adapter, paths []string) (string, error) {
	h := sha256.New()
	for _, path := range paths {
		s := Session{Harness: a.Harness(), Path: path}
		counts, err := a.Stream(s, Filter{AllProjects: true}, func(e Event) error {
			fmt.Fprintf(h, "%s\x1f%s\x1f%s\x1f%s\x1f%t\x1f%s\x1f%t\x1f%s\x1f%s\x1f%t\x1f%s\x1e",
				e.Kind, e.SessionID, e.ThreadID, e.Timestamp, e.Meta,
				e.Tool, e.Shell, e.Target, e.ToolUseID, e.IsError, e.Text)
			return nil
		})
		if err != nil {
			return "", err
		}
		// Entries and Skipped are the coverage a reader reports; Bytes is the
		// fixture's size, which no behaviour of the adapter decides.
		fmt.Fprintf(h, "counts:%d/%d\x1e", counts.Entries, counts.Skipped)
	}
	return hex.EncodeToString(h.Sum(nil))[:8], nil
}
