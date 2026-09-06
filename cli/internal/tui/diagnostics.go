package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/steer-sh/steer-cli/internal/status"
)

// Diagnostics runs a fixed set of checks and shows what they found, plus the
// history this machine holds. It does not read the whole system, so a clean
// screen says these checks found nothing rather than that everything works.

// viewDiagnostics is the read-only panel: the issues these checks detect, the
// history on this machine, and the technical block behind t.
func (m *Model) viewDiagnostics() string {
	width := m.bodyWidth()
	out := m.title("Diagnostics — what these checks found")

	out += section("Issues detected")
	issues := m.diagnosticIssues()
	if len(issues) == 0 {
		out += wrapTo(dim("No issues detected by these checks. They cover the review "+
			"outcomes still waiting and the guard runner's version; they do not read your "+
			"whole setup."), "  ", width)
	}
	for _, issue := range issues {
		out += "  " + warn(issue.headline) + "\n"
		out += wrapTo(issue.detail, "    ", width)
	}

	out += section("History on this machine")
	for _, line := range m.historyLines() {
		out += fmt.Sprintf("   %-34s %s\n", line.label, line.value)
	}
	out += wrapTo(dim("These are counts of what is stored here. A count says nothing about "+
		"whether the records are current or complete, and an empty one means nothing was "+
		"recorded on THIS machine."), "  ", width)

	out += section("Team guard configuration")
	out += m.teamLines(width)

	if !m.technical {
		return out + section("Technical details") +
			wrapTo(dim("press t to show storage paths, runner versions and how the "+
				"evidence was produced"), "  ", width)
	}
	return out + section("Technical details") + m.technicalLines(width)
}

// diagnosticsKey answers t, which opens and closes the technical block. The panel
// selects no row, so t is the only key it owns.
func (m *Model) diagnosticsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "t" {
		m.technical = !m.technical
	}
	return m, nil
}

// diagnosticIssue is one detected limitation: what it is, and where to repair it.
type diagnosticIssue struct {
	headline string
	detail   string
}

// diagnosticIssues are the concrete problems these checks find. Each names its
// consequence and the panel that repairs it, so no issue leaves the reader to
// work out where to go.
func (m *Model) diagnosticIssues() []diagnosticIssue {
	var out []diagnosticIssue
	if n := m.snap.Posture.Unresolved; n > 0 {
		out = append(out, diagnosticIssue{
			headline: status.OutcomesWaitingPhrase(n),
			detail: "A review that has no recorded outcome cannot be counted, so every result " +
				"resting on it is withheld. Open Arming and press enter on the read-outcomes " +
				"row, or run: " + status.BackfillCommand,
		})
	}
	for _, master := range m.snap.Posture.Masters {
		if !master.Stale() {
			continue
		}
		agent := status.HarnessLabel(master.Harness)
		out = append(out, diagnosticIssue{
			headline: "The guard runner for " + agent + " in " + scopeReach(master.Scope) +
				" is out of date",
			detail: "It was written by an older Steer, or carries no version at all, so a fix " +
				"shipped since then is not running. Open Guards and run the maintenance action " +
				"under Maintenance needed.",
		})
	}
	return out
}

// historyLine is one stored count with the noun it counts.
type historyLine struct{ label, value string }

// historyLines are the corpora this machine holds, each counted once and named by
// what one row IS. The counts print in normal text: a stored row proves something
// was recorded, never that it is current, complete or correct, so coloring one
// green would make a health claim off a line count.
func (m *Model) historyLines() []historyLine {
	t := m.snap.Telemetry
	lines := []historyLine{
		{"review sequences", fmt.Sprint(t.ReviewChains)},
		{"agent sessions", fmt.Sprint(t.SessionSnapshots)},
		{"actions blocked by auto mode", fmt.Sprint(t.DenialsRows)},
		{"guard blocks", fmt.Sprint(t.AuditFirings)},
	}
	for i := range lines {
		if lines[i].value == "0" {
			lines[i].value = dim("none recorded here")
		}
	}
	return lines
}

// teamLines report the repo-committed lockfile. Absent is not a fault, and a file
// that is there and will not parse is not absent.
func (m *Model) teamLines(width int) string {
	team := m.snap.Team
	switch {
	case !team.Present:
		return wrapTo(dim("Not configured for this project."), "  ", width)
	case team.Error != "":
		return wrapTo(warn("The file at "+team.Path+" could not be read: "+team.Error),
			"  ", width)
	}
	return wrapTo(fmt.Sprintf("%d guard(s) pinned, catalog %s, from %s",
		team.HooksLocked, team.CatalogVersion, team.Path), "  ", width)
}

// technicalLines hold what supports inspection and repair: where each corpus is
// stored, the runner versions, and how the evidence was produced. The Reviewer
// panel owns the settings themselves, so none is repeated here.
func (m *Model) technicalLines(width int) string {
	out := ""
	for _, c := range m.snap.Corpora {
		state := "absent on this machine"
		if c.Present {
			state = fmt.Sprintf("%d rows", c.Size)
		}
		out += wrapTo(fmt.Sprintf("%-22s %s", c.Corpus, state), "   ", width)
		if c.Path != "" {
			out += wrapTo(dim(c.Path), "     ", width)
		}
	}
	for _, master := range m.snap.Posture.Masters {
		phrase := status.MasterPhrase(master)
		if phrase == "" {
			continue
		}
		label := status.HarnessLabel(master.Harness) + " " + master.Scope
		if master.Stale() {
			out += wrapTo(fmt.Sprintf("%-22s %s", label, warn(phrase)), "   ", width)
			continue
		}
		out += wrapTo(fmt.Sprintf("%-22s %s", label,
			"guard runner matches this Steer — "+phrase), "   ", width)
	}
	out += wrapTo(fmt.Sprintf("%-22s %s", "reviewer", m.snap.Posture.Reviewer), "   ", width)
	out += wrapTo(fmt.Sprintf("%-22s %s", "how evidence was made",
		m.snap.Posture.Instrument), "   ", width)
	out += wrapTo(dim(m.snap.Posture.Ledger), "   ", width)
	return out
}
