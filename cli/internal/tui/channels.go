package tui

import (
	"fmt"
	"io"
	"os"
	"sort"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/steer-sh/steer-cli/internal/checks"
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

// backfillTimeout matches cmd's Stop-hook resolver wall clock.
const backfillTimeout = 30

// channelRow is one armable channel: a commit gate or check runner in one
// harness and scope. The harness is part of the row's identity, not decoration:
// the posture carries one row per harness per scope, so a row naming the scope
// alone renders identically for Claude Code, Codex and Pi while enter toggles
// exactly one of them.
type channelRow struct {
	label   string
	kind    string // "commit-gate" | "check-runner" | "read-outcomes"
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

// channelRows lists the rows enter acts on: the commit gate and the check runner
// per scope, then the one action row. The action row is last so a toggle
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
	for _, a := range m.snap.Checks.Runner {
		rows = append(rows, channelRow{label: "check runner", kind: "check-runner",
			harness: a.Harness, scope: a.Scope, armed: a})
	}
	// The kind stays the primary key: the commit gate is one block a reader
	// compares across harnesses, which is the question readiness answers. The
	// harness sorts above the scope because one harness's two scopes are the pair
	// a person toggles.
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if ka, kb := kindRank(a.kind), kindRank(b.kind); ka != kb {
			return ka < kb
		}
		if ha, hb := harnessRank(a.harness), harnessRank(b.harness); ha != hb {
			return ha < hb
		}
		return scopeRank(a.scope) < scopeRank(b.scope)
	})
	if n := m.snap.Posture.Unresolved; n > 0 {
		rows = append(rows, channelRow{label: readOutcomesLabel(n),
			kind: "read-outcomes", waiting: n})
	}
	return rows
}

// kindRank orders the commit gate before the check runner.
func kindRank(kind string) int {
	if kind == "commit-gate" {
		return 0
	}
	return 1
}

// harnessRank orders rows by the registry's own harness order, so the panel and
// harness.All can never disagree.
func harnessRank(id string) int {
	all := harness.All()
	for i, h := range all {
		if string(h.ID) == id {
			return i
		}
	}
	return len(all)
}

// scopeRank puts project above global.
func scopeRank(scope string) int {
	if scope == claude.ProjectScope {
		return 0
	}
	return 1
}

// readOutcomesLabel says what the action row does, in the count the reader sees.
func readOutcomesLabel(n int) string {
	if n == 1 {
		return "read 1 review outcome from your session history"
	}
	return fmt.Sprintf("read %d review outcomes from your session history", n)
}

// channelsKey acts on the selected row: a channel toggles, the action row starts
// the read. The read walks every session tree on the machine, so it runs as a
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
		m.note("reading your session history…")
		return m, m.readOutcomes(row.waiting)
	}
	if err := m.toggleChannel(row); err != nil {
		m.note("error: %v", err)
		return m, nil
	}
	m.refresh()
	m.note("%s changed", row.name())
	return m, nil
}

// outcomesReadMsg carries the count that was waiting when the read started, plus
// the run's own outcome in fields. The panel has no stream to narrate into.
type outcomesReadMsg struct {
	before int
	result resolve.Result
	err    error
}

// readOutcomes joins the reviews owing an outcome to each agent's session history
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
	// The read may finish while the reader is on another panel, so the note names
	// the panel that started it rather than the one now active.
	m.noteFor(panelChannels, "%s", outcomeNote(msg, left))
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
	case "check-runner":
		return m.toggleCheckRunner(row, on)
	}
	return nil
}

// toggleCheckRunner registers or removes the Stop runner for one harness and
// scope, through the functions `steer check install` and `uninstall` call. Codex
// reads its project file at the directory the snapshot read the runner from.
func (m *Model) toggleCheckRunner(row channelRow, on bool) error {
	command, _ := steerbin.HookCommand("check hook --harness " + row.harness)
	timeout := checks.RegisteredHookTimeout()
	if harness.ID(row.harness) == harness.Codex {
		cwd := m.snap.Checks.Repo
		if cwd == "" {
			cwd, _ = os.Getwd()
		}
		if on {
			_, _, err := codex.RemoveCheckHook(row.scope, cwd)
			return err
		}
		_, err := codex.RegisterCheckHook(row.scope, cwd, command, timeout)
		return err
	}
	if on {
		_, err := claude.RemoveCheckHook(row.scope)
		return err
	}
	return claude.RegisterCheckHook(row.scope, command, timeout)
}

// viewChannels renders the armable channels, then the action row, then a
// read-only note for the git pre-commit channels, whose install still lives
// behind the CLI. The action row is not a channel and does not take the channel
// columns: it names the work and the panel does it, so it never asks the reader
// to leave for a terminal.
func (m *Model) viewChannels() string {
	rows := m.channelRows()
	out := m.title("Arming — what runs, and where")
	for i, r := range rows {
		selected := i == m.cursor[panelChannels]
		if i > 0 && r.kind != rows[i-1].kind {
			out += "\n"
		}
		if r.kind == "read-outcomes" {
			// The row carries the in-flight state, not just the note: the reader may
			// be on another panel when the read finishes, and comes back to a row
			// that says whether it is still working.
			hint := "  press enter"
			if m.reading {
				hint = "  reading…"
			}
			out += selectedRow(r.label, secondary(hint), selected, m.bodyWidth())
			continue
		}
		name := fmt.Sprintf("%-17s %-12s %-8s", r.label, status.HarnessLabel(r.harness), r.scope)
		out += selectedRow(name, " "+armingBadge(r.armed)+m.pairedDrift(r), selected, m.bodyWidth())
	}
	out += "\n" + secondary("Install through the CLI:") + "\n"
	out += wrapTo("steer commit-gate install --precommit", "  ", m.bodyWidth())
	out += wrapTo("steer git-hooks install", "  ", m.bodyWidth())
	return out
}

// armingBadge names a scope's arming state, colored. A registration whose command
// does not resolve reads as broken rather than armed: the harness runs the tool
// anyway and reports nothing, so "armed" would be the lie this panel exists to
// prevent.
func armingBadge(a status.ScopeArming) string {
	switch {
	case a.Registered && a.Resolvable == status.ResolveMissing:
		return warn("broken") + " (command does not resolve — steer bin install)"
	case a.Effective:
		return ok("armed") + dim(observedSuffix(a))
	case a.Registered:
		return warn("inert") + " (registered, not effective)"
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
