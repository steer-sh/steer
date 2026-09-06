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
	ord := classOrder(items, m.snap.Rate)
	it := items[ord[m.cursor[panelClasses]]]
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
	section := ""
	for pos, i := range classOrder(items, rate) {
		it := items[i]
		un := status.Unmeasurable(it.Verdict, it.Unit, it.RequiredAfter, rate)
		if act := status.ActionFor(it.Verdict, un); act != section {
			section = act
			out += dim(status.ActionHeading[act]) + "\n"
		}
		line := fmt.Sprintf("%s %-22s %s %s", pointer(pos == m.cursor[panelClasses]),
			truncate(it.Class, 22), verdictBadge(it.Verdict, un),
			dim(status.TierPhrase(it.Tier, it.Target)))
		out += line + "\n"
		// The explanation leads: the regex and the range below it both measure
		// something the reader has to understand first.
		for _, p := range status.ProsePhrases(it.Problem, it.Intervention, it.Status == "rejected") {
			out += "     " + p + "\n"
		}
		out += "     " + dim(detailLine(it)) + "\n"
		out += "     " + dim(measureLine(it, un, rate)) + "\n"
		for _, r := range confoundLines(it) {
			out += "     " + warn(r) + "\n"
		}
	}
	return out
}

// classOrder is the order the panel renders the inventory in: grouped by the
// action each class asks for, in status.ActionOrder. The cursor indexes this
// order, so an arrow key walks the list down the screen.
func classOrder(items []status.ScoredIntervention, rate status.ChainRate) []int {
	ord := []int{}
	for _, act := range status.ActionOrder {
		for i, it := range items {
			un := status.Unmeasurable(it.Verdict, it.Unit, it.RequiredAfter, rate)
			if status.ActionFor(it.Verdict, un) == act {
				ord = append(ord, i)
			}
		}
	}
	return ord
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
	gap := status.EvidenceGap(it.Verdict, unit, it.BeforeCoverage, it.AfterCoverage, it.Attribution)
	if s := status.GapSentence(gap, it.Verdict, unmeasurable); s != "" {
		parts = append(parts, s)
	}
	if gap != nil && gap.Command != "" {
		parts = append(parts, gap.Command)
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

// verdictBadge colors a verdict by whether it demands action, and prints the
// label rather than the token. The abstentions the cadence queues read as
// warnings; the abstentions it leaves quiet read as dim.
func verdictBadge(v string, unmeasurable bool) string {
	label := status.VerdictLabel(v, unmeasurable)
	switch v {
	case "effective", "class-faded":
		return ok(label)
	case "harmful", "ineffective", "class-grew", "not-triggered":
		return errText(label)
	case "class-steady", "confounded", "evidence-incomplete", "legacy-unscoped", "unattributed":
		return warn(label)
	}
	return dim(label)
}
