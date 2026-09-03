package tui

import (
	"fmt"
	"strings"

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
	rate := m.snap.Rate
	// The panel groups by the action each class asks for, in status.ActionOrder,
	// so the cursor still walks one flat inventory in the order it is shown.
	byAction := map[string][]int{}
	for i, it := range items {
		un := status.Unmeasurable(it.Verdict, it.Unit, it.RequiredAfter, rate)
		byAction[status.ActionFor(it.Verdict, un)] = append(byAction[status.ActionFor(it.Verdict, un)], i)
	}
	for _, act := range status.ActionOrder {
		idx := byAction[act]
		if len(idx) == 0 {
			continue
		}
		out += dim(status.ActionHeading[act]) + "\n"
		for _, i := range idx {
			it := items[i]
			sel := i == m.cursor[panelClasses]
			un := status.Unmeasurable(it.Verdict, it.Unit, it.RequiredAfter, rate)
			badge := it.Verdict
			if un {
				badge = "unmeasurable"
			}
			line := fmt.Sprintf("%s %-22s %s %s", pointer(sel), truncate(it.Class, 22), verdictBadge(badge),
				dim(fmt.Sprintf("%s → %s", it.Tier, it.Target)))
			out += line + "\n"
			out += "     " + dim(detailLine(it)) + "\n"
			out += "     " + dim(measureLine(it, un, rate)) + "\n"
			for _, r := range confoundLines(it) {
				out += "     " + warn(r) + "\n"
			}
		}
	}
	return out
}

// detailLine is the second row for a class: its regex and rate columns.
func detailLine(it status.ScoredIntervention) string {
	s := "/" + it.Match + "/"
	if it.Unit != "" {
		s += "   " + it.Unit + "s"
	}
	if it.SignalKind != "" {
		s += " [" + it.SignalKind + "]"
	}
	if it.Verdict != "unscored" && it.Verdict != "" {
		unit := it.Unit
		if unit == "" {
			unit = "unit"
		}
		s += "   " + status.RecurrencePhrase(it.Before, it.After, unit)
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

// measureLine is the third row for a class: what to do about it, the range where
// the scorer made a claim, the frozen scope, and each window's coverage under it.
func measureLine(it status.ScoredIntervention, unmeasurable bool, rate status.ChainRate) string {
	unit := it.Unit
	if unit == "" {
		unit = "unit"
	}
	parts := []string{}
	if s := status.ActionSentence(it.Verdict, unmeasurable); s != "" {
		parts = append(parts, s)
	}
	if e := status.EffectClaimPhrase(it.Verdict, it.Effect); e != "" {
		parts = append(parts, e)
	}
	if r := status.RatePhrase(it.RequiredAfter, unit, rate); r != "" {
		parts = append(parts, r)
	} else if r := status.RequiredPhrase(it.RequiredAfter, it.Verdict); r != "" {
		parts = append(parts, r)
	}
	parts = append(parts,
		"scope "+status.ScopePhrase(it.Scope),
		"before "+status.CoveragePhrase(it.BeforeCoverage),
		"after "+status.CoveragePhrase(it.AfterCoverage))
	if a := status.AttributionPhrase(it.Attribution); a != "" {
		parts = append(parts, a)
	}
	if unmeasurable || it.Verdict == "harmful" {
		parts = append(parts, status.RemovalPhrase(it.Tier, it.Target))
	}
	return strings.Join(parts, " · ")
}

// confoundLines are the withheld-verdict reasons and the softer composition shifts.
func confoundLines(it status.ScoredIntervention) []string {
	out := status.ConfoundPhrases("confound", it.Confounds)
	return append(out, status.ConfoundPhrases("composition", it.Composition)...)
}

// verdictBadge colors a verdict by whether it demands action. The abstentions the
// cadence queues read as warnings; the abstentions it leaves quiet read as dim.
func verdictBadge(v string) string {
	switch v {
	case "effective", "class-faded":
		return ok(v)
	case "harmful", "ineffective", "class-grew", "not-triggered":
		return errText(v)
	case "class-steady", "confounded", "evidence-incomplete", "legacy-unscoped", "unattributed":
		return warn(v)
	}
	return dim(v)
}
