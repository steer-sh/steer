package tui

import (
	"fmt"
	"io"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/codex"
	"github.com/steer-sh/steer-cli/internal/gate/harvests"
	"github.com/steer-sh/steer-cli/internal/gate/resolve"
	"github.com/steer-sh/steer-cli/internal/harness"
	"github.com/steer-sh/steer-cli/internal/pi"
	"github.com/steer-sh/steer-cli/internal/statestore"
	"github.com/steer-sh/steer-cli/internal/status"
	"github.com/steer-sh/steer-cli/internal/steerbin"
)

// commitGateTimeoutMargin matches cmd's margin: the hook timeout is the reviewer
// timeout plus this, so the pair cannot drift.
const commitGateTimeoutMargin = 60

// denialsTimeout matches cmd's denials recorder settings.json wall clock.
const denialsTimeout = 10

// backfillTimeout matches cmd's Stop-hook resolver wall clock.
const backfillTimeout = 30

// channelRow is one armable channel: a commit gate or denials recorder in one
// harness and scope. The harness is part of the row's identity, not decoration:
// the posture carries one row per harness per scope, so a row naming the scope
// alone renders identically for Claude Code, Codex and Pi while enter toggles
// exactly one of them.
type channelRow struct {
	label   string
	kind    string // "commit-gate" | "denials" | "read-outcomes"
	harness string
	scope   string
	armed   status.ScopeArming
	// waiting is the read-outcomes row's count of reviews with no outcome yet.
	waiting int
}

// name is the row's identity in one phrase, used by the row and by the note the
// toggle writes, so the confirmation names what the row named.
func (r channelRow) name() string {
	if r.harness == "" {
		return r.label
	}
	return fmt.Sprintf("%s · %s %s", r.label, status.HarnessLabel(r.harness), r.scope)
}

// channelRows lists the rows enter acts on: the commit gate and the denials
// recorder per scope, then the one action row. The action row is last so a toggle
// keeps the cursor position it had before a review's outcome arrived, and it is
// present only while some review is waiting: a row that asks for nothing is a row
// the reader has to read to learn it can be skipped.
func (m *Model) channelRows() []channelRow {
	if m.snap == nil {
		return nil
	}
	var rows []channelRow
	for _, a := range m.snap.Posture.CommitGate {
		rows = append(rows, channelRow{label: "commit gate", kind: "commit-gate",
			harness: a.Harness, scope: a.Scope, armed: a})
	}
	for _, a := range m.snap.Posture.Denials {
		rows = append(rows, channelRow{label: "denials recorder", kind: "denials",
			harness: a.Harness, scope: a.Scope, armed: a})
	}
	if n := m.snap.Posture.Unresolved; n > 0 {
		rows = append(rows, channelRow{label: readOutcomesLabel(n),
			kind: "read-outcomes", waiting: n})
	}
	return rows
}

// readOutcomesLabel says what the action row does, in the count the reader sees.
func readOutcomesLabel(n int) string {
	if n == 1 {
		return "read 1 review outcome from your transcripts"
	}
	return fmt.Sprintf("read %d review outcomes from your transcripts", n)
}

// channelsKey acts on the selected row: a channel toggles, the action row starts
// the read. The read walks every transcript tree on the machine, so it runs as a
// command and the panel keeps drawing while it works.
func (m *Model) channelsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() != "enter" {
		return m, nil
	}
	rows := m.channelRows()
	if len(rows) == 0 {
		return m, nil
	}
	row := rows[m.cursor[panelChannels]]
	if row.kind == "read-outcomes" {
		// Only the read is blocked while a read is in flight. A channel toggle
		// writes a registration, which the read neither reads nor writes.
		if m.reading {
			return m, nil
		}
		m.reading = true
		m.note("reading your transcripts…")
		return m, m.readOutcomes(row.waiting)
	}
	if err := m.toggleChannel(row); err != nil {
		m.note("error: %v", err)
		return m, nil
	}
	m.refresh()
	m.note("%s toggled", row.name())
	return m, nil
}

// outcomesReadMsg carries the count that was waiting when the read started, plus
// the run's own outcome in fields. The panel has no stream to narrate into.
type outcomesReadMsg struct {
	before int
	result resolve.Result
	err    error
}

// readOutcomes joins the reviews owing an outcome to the harnesses' transcripts
// and writes the result, through the same engine `steer commit-gate backfill
// --apply` runs. A write migrates the evidence root, so it prepares it first,
// exactly as the CLI does before the same call.
func (m *Model) readOutcomes(before int) tea.Cmd {
	cfg := m.cfg
	return func() tea.Msg {
		if err := statestore.PrepareAll(); err != nil {
			return outcomesReadMsg{before: before, err: err}
		}
		return outcomesReadMsg{before: before, result: resolve.Backfill(cfg, harvests.All(),
			resolve.BackfillOptions{Apply: true, Quiet: true}, io.Discard, nil)}
	}
}

// outcomesRead recomposes over the rewritten evidence and says what moved. A run
// that read nothing has three causes, so the two the reader can act on are named
// ahead of the benign one. The count is the drop in the waiting reviews, which is
// the number the action row was labelled from.
func (m *Model) outcomesRead(msg outcomesReadMsg) {
	m.reading = false
	if msg.err == nil && msg.result.Err == nil && !msg.result.Locked {
		m.refresh()
	}
	left := 0
	if m.snap != nil {
		left = m.snap.Posture.Unresolved
	}
	m.note("%s", outcomeNote(msg, left))
}

// outcomeNote is the note itself, over the run's outcome and the reviews still
// waiting after it. It is pure so the wording is tested without a corpus on disk:
// the count is a difference across a recompose, and a test that recomposes reads
// whatever the machine running it happens to hold.
func outcomeNote(msg outcomesReadMsg, left int) string {
	switch {
	case msg.err != nil:
		return fmt.Sprintf("could not read: %v", msg.err)
	case msg.result.Err != nil:
		return fmt.Sprintf("nothing was written: %v", msg.result.Err)
	case msg.result.Locked:
		return "your coding agent is reading them right now — try again in a moment"
	}
	switch read := msg.before - left; {
	case read <= 0:
		return "no outcome could be read yet — these reviews are too recent"
	case left > 0:
		return fmt.Sprintf("read %d · %d still waiting", read, left)
	default:
		return fmt.Sprintf("read %d · every review has its outcome", read)
	}
}

// toggleChannel arms an off channel and removes an on one, through the same
// claude functions the CLI commands call.
func (m *Model) toggleChannel(row channelRow) error {
	on := row.armed.Effective || row.armed.Registered
	switch row.kind {
	case "commit-gate":
		timeout := int(m.cfg.ReviewTimeout.Seconds()) + commitGateTimeoutMargin
		// A failed copy of the stable executable returns the PATH form beside its
		// error. Register that rather than nothing, exactly as the CLI does: the
		// posture then reports whether the command resolves.
		resolver, _ := steerbin.HookCommand("commit-gate backfill --if-due --apply --quiet")
		cwd, _ := os.Getwd()
		switch harness.ID(row.armed.Harness) {
		case harness.Codex:
			if on {
				_, err := codex.RemoveCommitGateAndResolver(row.scope, cwd)
				return err
			}
			review, _ := steerbin.HookCommand("commit-gate review-codex")
			_, err := codex.RegisterCommitGateAndResolver(row.scope, cwd,
				review, timeout, resolver, backfillTimeout)
			return err
		case harness.Pi:
			if on {
				_, err := pi.RemoveCommitGateAndResolver(row.scope, cwd)
				return err
			}
			review, _ := steerbin.HookCommand("commit-gate review-pi")
			_, err := pi.RegisterCommitGateAndResolver(row.scope, cwd,
				review, timeout, resolver, backfillTimeout)
			return err
		default:
			if on {
				return claude.RemoveCommitGateAndResolver(row.scope)
			}
			review, _ := steerbin.HookCommand("commit-gate review")
			return claude.RegisterCommitGateAndResolver(row.scope, review, timeout, resolver, backfillTimeout)
		}
	case "denials":
		if on {
			return claude.RemoveDenialsHook(row.scope)
		}
		record, _ := steerbin.HookCommand("denials record")
		return claude.RegisterDenialsHook(row.scope, record, denialsTimeout)
	}
	return nil
}

// viewChannels renders the armable channels, then the action row, then a
// read-only note for the git pre-commit channels, whose install still lives
// behind the CLI. The action row is not a channel and does not take the channel
// columns: it names the work and the panel does it, so it never asks the reader
// to leave for a terminal.
func (m *Model) viewChannels() string {
	rows := m.channelRows()
	out := header("Channels — arming posture")
	for i, r := range rows {
		selected := pointer(i == m.cursor[panelChannels])
		if r.kind == "read-outcomes" {
			// The row carries the in-flight state, not just the note: the reader may
			// be on another panel when the read finishes, and comes back to a row
			// that says whether it is still working.
			hint := "  press enter\n"
			if m.reading {
				hint = "  reading…\n"
			}
			out += "\n" + selected + " " + warn(r.label) + dim(hint)
			continue
		}
		out += fmt.Sprintf("%s %-17s %-12s %-8s %s%s\n", selected,
			r.label, status.HarnessLabel(r.harness), r.scope, armingBadge(r.armed),
			m.pairedDrift(r))
	}
	out += "\n" + dim("git pre-commit and steer git-hooks install through the CLI: "+
		"steer commit-gate install --precommit · steer git-hooks install")
	return out
}

// armingBadge names a scope's arming state, colored. A registration whose command
// does not resolve reads as broken rather than armed: the harness runs the tool
// anyway and reports nothing, so "armed" would be the lie this panel exists to
// prevent.
func armingBadge(a status.ScopeArming) string {
	switch {
	case a.Registered && a.Resolvable == status.ResolveMissing:
		return warn("broken (command does not resolve — steer bin install)")
	case a.Effective:
		return ok("armed" + dim(observedSuffix(a)))
	case a.Registered:
		return warn("inert (registered, not effective)")
	default:
		return dim("off")
	}
}

// observedSuffix names when the channel last wrote a row, or nothing when the
// channel writes no corpus of its own.
func observedSuffix(a status.ScopeArming) string {
	if o := status.ObservedPhrase(a); o != "" {
		return " · " + o
	}
	return ""
}

// pairedDrift names a commit-gate row's turn-end hook, and only when the two
// disagree. One operation writes the pair, so agreement is the normal state and a
// row for it repeats the gate row beside it. A disagreement is an install an
// older steer wrote or a hand edit, and it is the one thing a reader needs the
// second hook for: without it a review is never closed, so the measurement reads
// as missing evidence rather than as a result.
func (m *Model) pairedDrift(row channelRow) string {
	if row.kind != "commit-gate" || m.snap == nil {
		return ""
	}
	paired, found := status.ScopeArming{}, false
	for _, a := range m.snap.Posture.Resolver {
		if a.Harness == row.harness && a.Scope == row.scope {
			paired, found = a, true
			break
		}
	}
	if !found {
		return ""
	}
	switch on := row.armed.Effective || row.armed.Registered; {
	case on && !paired.Registered:
		return warn(" · reviews stay open — toggle to repair")
	case !on && paired.Registered:
		return warn(" · a turn-end hook is left over — arm, then disarm")
	}
	return ""
}
