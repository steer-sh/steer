package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/status"
)

// commitGateTimeoutMargin matches cmd's margin: the hook timeout is the reviewer
// timeout plus this, so the pair cannot drift.
const commitGateTimeoutMargin = 60

// denialsTimeout matches cmd's denials recorder settings.json wall clock.
const denialsTimeout = 10

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
		if on {
			return claude.RemoveCommitGateHook(row.scope)
		}
		timeout := int(m.cfg.ReviewTimeout.Seconds()) + commitGateTimeoutMargin
		return claude.RegisterCommitGateHook(row.scope, "steer commit-gate review", timeout)
	case "denials":
		if on {
			return claude.RemoveDenialsHook(row.scope)
		}
		return claude.RegisterDenialsHook(row.scope, "steer denials record", denialsTimeout)
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
	out += "\n" + dim("git pre-commit and steer git-hooks install through the CLI: "+
		"steer commit-gate install --precommit · steer git-hooks install")
	return out
}

// armingBadge names a scope's arming state, colored.
func armingBadge(a status.ScopeArming) string {
	switch {
	case a.Effective:
		return ok("armed")
	case a.Registered:
		return warn("inert (registered, not effective)")
	default:
		return dim("off")
	}
}
