package tui

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/codex"
	"github.com/steer-sh/steer-cli/internal/harness"
	"github.com/steer-sh/steer-cli/internal/pi"
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

// channelRow is one armable channel: a commit gate or denials recorder in a scope.
type channelRow struct {
	label string
	kind  string // "commit-gate" | "denials"
	scope string
	armed status.ScopeArming
}

// channelRows lists the channels the panel can toggle: the commit gate and the
// denials recorder, each per scope. Both come straight from the posture read.
func (m *Model) channelRows() []channelRow {
	if m.snap == nil {
		return nil
	}
	var rows []channelRow
	for _, a := range m.snap.Posture.CommitGate {
		rows = append(rows, channelRow{label: "commit gate", kind: "commit-gate", scope: a.Scope, armed: a})
	}
	for _, a := range m.snap.Posture.Denials {
		rows = append(rows, channelRow{label: "denials recorder", kind: "denials", scope: a.Scope, armed: a})
	}
	return rows
}

// channelsKey toggles the selected channel's arming on enter.
func (m *Model) channelsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() != "enter" {
		return m, nil
	}
	rows := m.channelRows()
	if len(rows) == 0 {
		return m, nil
	}
	row := rows[m.cursor[panelChannels]]
	if err := m.toggleChannel(row); err != nil {
		m.note("error: %v", err)
		return m, nil
	}
	m.refresh()
	m.note("%s (%s) toggled", row.label, row.scope)
	return m, nil
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

// viewChannels renders the armable channels, then a read-only note for the git
// pre-commit channels, whose install still lives behind the CLI.
func (m *Model) viewChannels() string {
	rows := m.channelRows()
	out := header("Channels — arming posture")
	for i, r := range rows {
		out += fmt.Sprintf("%s %-16s %-8s %s\n", pointer(i == m.cursor[panelChannels]), r.label, r.scope, armingBadge(r.armed))
	}
	for _, a := range m.snap.Posture.Resolver {
		out += fmt.Sprintf("  %-16s %-8s %s\n", "resolver", a.Scope, armingBadge(a))
	}
	out += dim("  the resolver arms and disarms with the commit gate on the same scope.\n")
	if n := m.snap.Posture.Unresolved; n > 0 {
		out += warn(fmt.Sprintf("  %d ledger row(s) owe a transcript join — run: steer commit-gate backfill --apply\n", n))
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
