package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/steer-sh/steer-cli/internal/status"
)

// classesKey opens the retire wizard for the selected class on x.
func (m *Model) classesKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() != "x" {
		return m, nil
	}
	items := m.snap.Interventions
	if len(items) == 0 {
		return m, nil
	}
	it := items[m.cursor[panelClasses]]
	m.form = &formState{
		kind:  formRetire,
		id:    it.ID,
		steps: []formStep{{label: "reason", value: ""}},
	}
	m.editing = true
	m.matchCount = ""
	m.loadStep()
	return m, nil
}

// viewClasses renders the full scored inventory, verdict-first.
func (m *Model) viewClasses() string {
	if m.editing && m.form != nil {
		return m.viewForm()
	}
	items := m.snap.Interventions
	out := header("Classes — full scored inventory")
	if len(items) == 0 {
		return out + dim("no interventions recorded on this machine.")
	}
	for i, it := range items {
		sel := i == m.cursor[panelClasses]
		line := fmt.Sprintf("%s %-22s %s %s", pointer(sel), truncate(it.Class, 22), verdictBadge(it.Verdict),
			dim(fmt.Sprintf("%s → %s", it.Tier, it.Target)))
		out += line + "\n"
		out += "     " + dim(detailLine(it)) + "\n"
	}
	return out
}

// detailLine is the second row for a class: its regex and rate columns.
func detailLine(it status.ScoredIntervention) string {
	s := "/" + it.Match + "/"
	if it.Verdict != "unscored" && it.Verdict != "" {
		s += fmt.Sprintf("   %d/%d → %d/%d", it.Before.Hits, it.Before.Units, it.After.Hits, it.After.Units)
		if it.PValue > 0 {
			s += fmt.Sprintf("   p=%.3f", it.PValue)
		}
	}
	if it.InstrumentDrift {
		s += "   ⚠ instrument drift"
	}
	if it.Status == "rejected" && it.Reason != "" {
		s += "   refused: " + it.Reason
	}
	return s
}

// verdictBadge colors a verdict by whether it demands action.
func verdictBadge(v string) string {
	switch v {
	case "effective", "promising", "class-faded", "class-steady":
		return ok(v)
	case "ineffective", "class-grew":
		return errText(v)
	case "partial":
		return warn(v)
	}
	return dim(v)
}
