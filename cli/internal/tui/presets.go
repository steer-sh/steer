package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/steer-sh/steer-cli/internal/presets"
)

// presetsKey toggles a preset on enter and reconciles the catalog on s.
func (m *Model) presetsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "s":
		m.syncPresets()
		return m, nil
	case "enter":
		m.togglePreset()
		return m, nil
	}
	return m, nil
}

// togglePreset enables a disabled preset and disables an enabled one, through
// the same presets functions the CLI commands call.
func (m *Model) togglePreset() {
	roster := m.snap.Presets
	if len(roster) == 0 {
		return
	}
	sel := roster[m.cursor[panelPresets]]
	catalog, err := presets.LoadCatalog()
	if err != nil {
		m.note("error: %v", err)
		return
	}
	item, ok := presets.FindVisiblePreset(catalog, sel.Name)
	if !ok {
		m.note("preset not found: %s", sel.Name)
		return
	}
	if sel.Enabled {
		_, err = presets.DisablePreset(m.scope, item, false)
	} else {
		_, err = presets.EnablePreset(m.scope, catalog, item, false)
		if err == nil {
			_, err = presets.EnsureAutoManagedFactoryPresets(m.scope, catalog, false)
		}
	}
	if err != nil {
		m.note("error: %v", err)
		return
	}
	m.refresh()
	m.note("preset %s toggled", sel.Name)
}

// syncPresets reconciles enabled factory presets with the embedded catalog.
func (m *Model) syncPresets() {
	catalog, err := presets.LoadCatalog()
	if err != nil {
		m.note("error: %v", err)
		return
	}
	res, err := presets.SyncFactoryPresets(m.scope, catalog, presets.SyncOptions{})
	if err != nil {
		m.note("sync error: %v", err)
		return
	}
	m.refresh()
	m.note("sync: %d checked, %d reinstalled, %d up-to-date", res.CheckedFactory, res.Reinstalled, res.UpToDate)
}

// viewPresets renders the catalog roster and the registered hooks below it.
func (m *Model) viewPresets() string {
	out := header("Hooks & presets — catalog roster")
	for i, p := range m.snap.Presets {
		sel := i == m.cursor[panelPresets]
		state := dim("disabled")
		if p.Enabled {
			state = ok("enabled")
			if p.Drifted {
				state = warn("enabled (drifted)")
			}
		}
		out += fmt.Sprintf("%s %-30s %-10s %s\n", pointer(sel), truncate(p.Name, 30), p.Bundle, state)
	}
	out += "\n" + header("Registered managed hooks ("+m.scope+")")
	if len(m.snap.Hooks) == 0 {
		out += dim("none registered in this scope.")
		return out
	}
	for _, h := range m.snap.Hooks {
		out += fmt.Sprintf("   %-30s %s\n", truncate(h.Name, 30), dim(h.Source))
	}
	return out
}
