package tui

import (
	"fmt"
	"strings"

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
			"outcomes still waiting, the guard runner's version and this repository's "+
			"checks; they do not read your whole setup."), "  ", width)
	}
	for _, issue := range issues {
		out += wrapTo(warn(issue.headline), "  ", width)
		out += wrapTo(issue.detail, "    ", width)
	}

	out += section("History on this machine")
	for _, line := range m.historyLines() {
		out += wrapTo(fmt.Sprintf("%6s  %s", line.value, secondary(line.label)), "  ", width)
	}
	out += wrapTo(dim("These are counts of what is stored here. A count says nothing about "+
		"whether the records are current or complete, and an empty one means nothing was "+
		"recorded on THIS machine."), "  ", width)

	out += section("Repository checks")
	out += m.checkLines(width)

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
	switch msg.String() {
	case "t":
		m.technical = !m.technical
	case "up", "k":
		m.bodyOffset--
	case "down", "j":
		m.bodyOffset++
	case "pgup":
		m.bodyOffset -= max(1, m.bodyBudget()-3)
	case "pgdown":
		m.bodyOffset += max(1, m.bodyBudget()-3)
	case "home":
		m.bodyOffset = 0
	case "end":
		m.bodyOffset = m.bodyMax
	}
	m.bodyOffset = max(0, min(m.bodyOffset, m.bodyMax))
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
	out = append(out, m.checkIssues()...)
	for _, master := range m.snap.Posture.Masters {
		if !master.Stale() {
			continue
		}
		agent := status.HarnessLabel(master.Harness)
		out = append(out, diagnosticIssue{
			headline: "The guard runner for " + agent + " in " + scopeReach(master.Scope) +
				" is out of date",
			detail: "It was written by an older Steer, or carries no version at all, so a fix " +
				"shipped since then is not running. Open Fixes and run the maintenance action " +
				"under Maintenance needed.",
		})
	}
	for _, master := range m.snap.Posture.Masters {
		if !master.RunnerUnapproved() {
			continue
		}
		agent := status.HarnessLabel(master.Harness)
		out = append(out, diagnosticIssue{
			headline: agent + " has not approved the guard runner in " + scopeReach(master.Scope),
			detail: agent + " runs no hook it has not approved, so no guard runs there, while " +
				"the rules it reads still name them. Restart " + agent + " and approve the " +
				"definitions in /hooks.",
		})
	}
	for _, r := range m.snap.Posture.Rules {
		if !r.NeedsSync() {
			continue
		}
		agent := status.HarnessLabel(r.Harness)
		detail := status.RulesPhrase(r) + ". The agent reads only the rules in that file, " +
			"so a fix that rests on a missing rule is not in effect."
		switch {
		case r.State != status.RulesDrifted:
		case r.Scope == m.scope:
			detail += " Open Fixes and run the maintenance action under Maintenance needed."
		default:
			detail += " The Fixes action rebuilds this dashboard's scope only; for this one, run: " +
				status.RulesRepairCommand(r.Scope)
		}
		out = append(out, diagnosticIssue{
			headline: "The rules " + agent + " reads in " + scopeReach(r.Scope) +
				" do not match Steer's",
			detail: detail,
		})
	}
	return out
}

// checkLines report this repository's own checks: whether they run at the end of
// a turn here, what the last run that did any work recorded, and where each
// declared check's proof and last execution stand. Three separate readings: a
// registered runner is not authorization, and neither is a passing script proof
// that a particular check ran.
func (m *Model) checkLines(width int) string {
	c := m.snap.Checks
	out := wrapTo(status.CheckAdoptionPhrase(c), "  ", width)
	if c.Repo == "" {
		return out
	}
	out += wrapTo(dim("last run: "+status.CheckRunPhrase(c.LatestRun)), "  ", width)
	for _, row := range c.Checks {
		out += wrapTo(status.CheckPhrase(row), "  ", width)
	}
	return out
}

// checkIssues are the open steps in this repository's check tier. A stale proof
// is not among them: a stale check keeps running, and its label reports what
// moved rather than asking for a repair.
func (m *Model) checkIssues() []diagnosticIssue {
	c := m.snap.Checks
	if c.Repo == "" {
		return nil
	}
	var out []diagnosticIssue
	registered := false
	for _, a := range c.Runner {
		registered = registered || a.Registered
	}
	if c.Manifest && !registered {
		out = append(out, diagnosticIssue{
			headline: "No agent runs this repository's checks at the end of a turn",
			detail: "The checks declared here run only when one is registered to run them. " +
				"Run: steer check install --harness claude-code --scope global",
		})
	}
	if c.Manifest && !c.Adopted {
		out = append(out, diagnosticIssue{
			headline: "This machine has not authorized this repository's checks",
			detail: "Authorization is machine-local, so a clone carries none of it, and " +
				"nothing here runs until it is given. Run: steer check adopt",
		})
	}
	for _, a := range c.Runner {
		step, ok := status.RunnerTimeoutStep(a)
		if !ok {
			continue
		}
		out = append(out, diagnosticIssue{
			headline: status.HarnessLabel(a.Harness) + " in " + scopeReach(a.Scope) + ": " + step.Title,
			detail:   "A run stopped part way through reports no result at all. Run: " + step.Fix,
		})
	}
	for _, row := range c.Checks {
		if row.Validation != status.ValidationNone {
			continue
		}
		out = append(out, diagnosticIssue{
			headline: "The check " + row.ID + " was never replayed on this machine",
			detail: "Nothing here shows it fails on the problem it names, so a passing run " +
				"proves nothing about it. Run: steer check test " + row.ID,
		})
	}
	if r := c.LatestRun; r != nil && r.IntegrationFailure {
		out = append(out, diagnosticIssue{
			headline: "The last run passed without running every check it declares",
			detail: "That is the script's wiring, not a failed assertion: " +
				strings.Join(r.Missing, ", ") + " never ran. Add the managed line to check.sh.",
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
		return wrapTo(warn("Unreadable: ")+"The file at "+team.Path+" could not be read: "+team.Error,
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
		if trust := status.RunnerTrustPhrase(master); trust != "" {
			phrase += "; " + trust
		}
		if master.Stale() || master.RunnerUnapproved() {
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
