package tui

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/steer-sh/steer-cli/internal/cadence"
	"github.com/steer-sh/steer-cli/internal/gate/mine"
	"github.com/steer-sh/steer-cli/internal/status"
)

// The Problems panel answers two questions in one list: what problems are showing
// up, and are our responses helping. A queue row is evidence nobody has decided
// yet; a tracked row is a record with a result. Enter expands either kind in
// place; authoring and deciding stay in /steer-report.

// headingToConsider heads the queue rows. They sit below the tracked inventory:
// a tracked row names a problem and offers a key that acts on it, while a queue
// row is an undecided pile whose only step is the handoff, so putting the piles
// first buries every row the panel can actually act on.
var headingToConsider = status.HeadingUngrouped

// retireForm is the one editor the panel opens over a record: the reason a
// problem stops being tracked. mine.Retire writes it and touches no hook, no
// check.sh and no instruction file, so the rule keeps running in its tier.
type retireForm struct {
	id string
}

// problemRow is one entry of the list: a queue candidate or a tracked record,
// never both. heading is the section it files under.
type problemRow struct {
	heading   string
	candidate *cadence.Candidate
	item      *status.ScoredIntervention
}

// problemRows builds the list in render order: the tracked inventory first,
// grouped by the action each record asks for, then the undecided queue. The
// cursor indexes this order, so an arrow key walks the list down the screen.
func (m *Model) problemRows() []problemRow {
	if m.snap == nil {
		return nil
	}
	var rows []problemRow
	items := m.snap.Interventions
	for _, i := range classOrder(items) {
		it := items[i]
		rows = append(rows, problemRow{
			heading: status.ActionHeading[status.ActionFor(it.Observation, it.Scope, it.Tier, it.Artifact)],
			item:    &items[i],
		})
	}
	for i := range m.snap.Attention.Candidates {
		rows = append(rows, problemRow{
			heading:   headingToConsider,
			candidate: &m.snap.Attention.Candidates[i],
		})
	}
	return rows
}

// classOrder is the order the tracked inventory renders in: grouped by the action
// each record asks for, in status.ActionOrder.
func classOrder(items []status.ScoredIntervention) []int {
	ord := []int{}
	for _, act := range status.ActionOrder {
		for i, it := range items {
			if status.ActionFor(it.Observation, it.Scope, it.Tier, it.Artifact) == act {
				ord = append(ord, i)
			}
		}
	}
	return ord
}

// problemsKey drives the panel: enter opens and closes the detail, esc closes it,
// the paging keys scroll it, t reveals the technical block, and x opens the
// stop-tracking editor over a tracked record. Enter itself never writes.
func (m *Model) problemsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	rows := m.problemRows()
	if len(rows) == 0 {
		return m, nil
	}
	row := rows[m.cursor[panelProblems]]
	switch msg.String() {
	case "enter":
		m.setExpanded(!m.expanded)
	case "esc":
		m.setExpanded(false)
	case "t":
		if m.expanded {
			m.technical = !m.technical
			m.detailOffset = 0
		}
	case "pgdown":
		m.scrollDetailBy(m.detailBudget())
	case "pgup":
		m.scrollDetailBy(-m.detailBudget())
	case "home":
		m.detailOffset = 0
	case "end":
		m.scrollDetailBy(m.detailLines)
	case "x":
		if row.item != nil {
			m.openRetire(row.item.ID)
		}
	}
	return m, nil
}

// setExpanded opens or closes the detail area, resetting what only means
// something while one is open.
func (m *Model) setExpanded(open bool) {
	m.expanded = open
	m.technical = false
	m.detailOffset = 0
}

// scrollDetailBy moves the detail's first visible line, clamped against the line
// count the view recorded. A block that fits its budget never scrolls.
func (m *Model) scrollDetailBy(n int) {
	budget := m.detailBudget()
	if budget <= 0 || m.detailLines <= budget {
		m.detailOffset = 0
		return
	}
	m.detailOffset += n
	if max := m.detailLines - budget; m.detailOffset > max {
		m.detailOffset = max
	}
	if m.detailOffset < 0 {
		m.detailOffset = 0
	}
}

// openRetire opens the stop-tracking editor over one record.
func (m *Model) openRetire(id string) {
	m.form = &retireForm{id: id}
	m.editing = true
	m.input.Prompt = "reason: "
	m.input.Placeholder = "why this problem no longer needs tracking"
	m.input.SetValue("")
	m.input.CursorEnd()
	m.input.Focus()
}

// retireEditing drives the stop-tracking editor: enter writes, esc cancels.
func (m *Model) retireEditing(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.editing = false
		m.form = nil
		m.note("cancelled")
		return m, nil
	case "enter":
		var buf strings.Builder
		id := m.form.id
		err := mine.Retire(&buf, m.cfg.StateDir, id, strings.TrimSpace(m.input.Value()), "removed")
		if err != nil {
			// The write failed, so the editor stays open with the typed reason intact.
			m.note("error: %v", err)
			return m, nil
		}
		m.editing = false
		m.form = nil
		m.refresh()
		m.note("%q is no longer tracked; the change stays where it is", id)
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// viewRetireForm renders the stop-tracking editor over the selected record.
func (m *Model) viewRetireForm() string {
	width := m.bodyWidth()
	out := m.title("Stop tracking " + m.form.id + " — enter to save, esc to cancel")
	out += wrapTo("This ends the measurement only. Whatever the fix put in place stays there until you remove it yourself.", "  ", width)
	m.input.Width = max(1, width-14)
	return out + "\n" + inset(wrapTo(m.input.View(), "", width-3), width, true)
}

// viewProblems renders the queue and the tracked inventory as one navigable list
// of compact summaries, with the selected entry's detail area under it.
func (m *Model) viewProblems() string {
	if m.editing && m.form != nil {
		return m.viewRetireForm()
	}
	rows := m.problemRows()
	out := m.title("Problems — what recurs, and whether our fixes help")
	if len(rows) == 0 {
		return out + dim("nothing recorded on this machine yet. Run /steer-report to look for problems.")
	}
	width := m.bodyWidth()
	heading := ""
	for i, r := range rows {
		if r.heading != heading {
			heading = r.heading
			out += headingStyle.Render(strings.TrimRight(wrapTo(heading, "", width), "\n")) + "\n"
		}
		selected := i == m.cursor[panelProblems]
		summary := m.problemSummary(r, selected, width)
		if selected && m.expanded {
			summary = m.expandedSummary(r)
		}
		out += summary
		if selected && m.expanded {
			out += m.problemDetail(r, width)
		}
		out += "\n"
	}
	return out
}

// summaryIndent is the column the stacked labels of one summary start in, under
// the selection marker and the title.
const summaryIndent = "    "

// problemSummary is one compact entry: the title, then labeled lines for the fix,
// the result, and the one short reason or next step the entry needs.
func (m *Model) problemSummary(r problemRow, selected bool, width int) string {
	out := selectedRow(problemTitle(r), "", selected, width)
	for _, f := range summaryFields(r) {
		out += field(f.label, f.value, summaryIndent, width)
	}
	return out
}

// summaryField is one labeled line of a summary.
type summaryField struct{ label, value string }

// summaryFields are the lines under one title. A queue row names its source and
// the handoff; a tracked row names the fix, the result, and either the reason the
// result is unavailable or the step the reader can take — never a generic
// instruction that names no decision.
func summaryFields(r problemRow) []summaryField {
	if r.candidate != nil {
		return []summaryField{
			{"Evidence", candidateEvidence(*r.candidate)},
			{"Next", status.CandidateNext},
		}
	}
	it := *r.item
	v := problemView(it)
	fields := []summaryField{
		{fixLabel(it), fixSummary(it)},
		{"Result", verdictBadge(it.Observation)},
	}
	why, next := status.ProblemWhy(v), status.ProblemNext(v)
	if why != "" {
		fields = append(fields, summaryField{"Why", why})
	}
	if next != "" {
		fields = append(fields, summaryField{"Next", next})
	}
	return fields
}

// problemTitle names the entry. A queue row covers no problem yet, so it says so
// rather than borrowing a title from one item of its pile.
func problemTitle(r problemRow) string {
	if r.item != nil {
		return r.item.Class
	}
	return status.CandidateTitle(*r.candidate)
}

// candidateEvidence is the queue row's own counts, in the source's words. The
// terminal prints the same sentence, so the phrasing lives in status.
func candidateEvidence(c cadence.Candidate) string {
	return status.CandidateEvidence(c)
}

// fixLabel distinguishes a recorded fix from a suggestion that was declined. A
// declined record installed nothing, so what it describes is the fix it
// considered.
func fixLabel(it status.ScoredIntervention) string {
	return status.FixLabel(problemView(it))
}

// fixSummary describes what the fix does and what type it is. It never claims the
// fix is installed and running: the record carries what was decided, not current
// setup evidence, and the target path belongs in the detail area.
func fixSummary(it status.ScoredIntervention) string {
	v := problemView(it)
	// The path belongs in the detail area, so the summary names the type alone.
	v.Target = ""
	return status.FixSummary(v)
}

// problemView reduces a tracked record to the fields the shared phrasing reads,
// so this panel and the terminal cannot answer one record differently.
func problemView(it status.ScoredIntervention) status.ProblemView {
	return status.ProblemView{
		Declined:       it.Status == "rejected",
		Tier:           it.Tier,
		Target:         it.Target,
		Intervention:   it.Intervention,
		Observation:    it.Observation,
		Scope:          it.Scope,
		Artifact:       it.Artifact,
		Unit:           it.Unit,
		BeforeCoverage: it.BeforeCoverage,
		AfterCoverage:  it.AfterCoverage,
	}
}

// summaryReason splits the entry's one explanatory sentence into the reason a
// result is unavailable and the step the reader can take. An observation that
// asks nothing carries a reason and no step; one that asks the reader to look at
// something carries a step.
func summaryReason(it status.ScoredIntervention) (why, next string) {
	v := problemView(it)
	return status.ProblemWhy(v), status.ProblemNext(v)
}

// problemDetail is the expanded area under the selected summary: the full
// explanation, the fix or the declined proposal, the result, the evidence, the
// comparison limits, the action, and the technical block behind t.
func (m *Model) problemDetail(r problemRow, width int) string {
	width = min(width, 104)
	block := detailBlock(r, width-3)
	lines := strings.Split(strings.TrimRight(block, "\n"), "\n")
	if m.technical {
		lines = append(lines, strings.Split(strings.TrimRight(
			technicalBlock(r, width-3), "\n"), "\n")...)
	}
	m.detailLines = len(lines)
	budget := m.detailBudget()
	if budget <= 0 || len(lines) <= budget {
		m.detailOffset = 0
		return inset(strings.Join(lines, "\n"), width, false)
	}
	start := m.detailOffset
	if max := len(lines) - budget; start > max {
		start = max
	}
	out := strings.Join(lines[start:start+budget], "\n") + "\n"
	return inset(out+hiddenNote(start, len(lines)-start-budget), width, false)
}

// detailIndent is the column the detail area's own body sits in.
const detailIndent = "  "

// detailBlock is the detail's content, before any scrolling. A queue row explains
// the handoff; a tracked row explains itself in the order a reader asks: what
// goes wrong, what was chosen, what is known, what the evidence is, what to do.
func detailBlock(r problemRow, width int) string {
	out := "\n"
	if r.candidate != nil {
		return out + candidateDetail(*r.candidate, width)
	}
	it := *r.item
	if it.Problem != "" {
		out += detailSection("What goes wrong", it.Problem, width)
	}
	out += detailSection(fixLabel(it)+" in full", fixDetail(it), width)
	if it.Status == "rejected" && it.Reason != "" {
		out += wrapTo("Why it was declined: "+it.Reason, detailIndent, width)
	}
	if it.Reopen != "" {
		out += wrapTo("Reconsider it when: "+it.Reopen, detailIndent, width)
	}
	if it.RejectedTS > 0 {
		out += wrapTo("Declined on "+
			time.Unix(int64(it.RejectedTS), 0).Format("2006-01-02"), detailIndent, width)
	}
	out += detailSection("Result", resultDetail(it), width)
	out += evidenceDetail(it, width)
	out += actionDetail(it, width)
	return out
}

// detailSection is one titled paragraph of the detail area.
func detailSection(title, body string, width int) string {
	if strings.TrimSpace(body) == "" {
		return ""
	}
	return headingStyle.Render(title) + "\n" + wrapTo(body, detailIndent, width)
}

// candidateDetail explains a queue row: what the sweep found, and the handoff.
// Enter installs nothing, records no decision and starts no agent, so the text
// names the one thing that does.
func candidateDetail(c cadence.Candidate, width int) string {
	out := detailSection("What the sweep found", candidateEvidence(c), width)
	body := "This evidence is not sorted into problems yet, so no title and no fix exist for it."
	// The claim is made only where the marked lines are: a row whose pile carries
	// no examples renders the dump command alone, and the sentence would describe
	// that command as an item of the pile.
	if len(c.Examples) > 0 {
		body += " A line marked · is one item of the pile, never a problem on its own."
	}
	out += detailSection("Not grouped yet", body, width)
	if c.Dump != "" {
		out += wrapTo("read them all with: "+c.Dump, detailIndent, width)
	}
	for _, ex := range c.Examples {
		out += wrapTo("· "+ex, detailIndent, width)
	}
	return out + detailSection("What to do",
		"Run /steer-report in your coding agent. It reads this evidence with the findings in "+
			"context and asks you which fixes to apply or skip.", width)
}

// fixDetail describes the fix and where it stands now. The artifact reading says
// whether the entry the record names is still there; it says nothing about whether
// a coding agent runs it.
func fixDetail(it status.ScoredIntervention) string {
	out := strings.TrimRight(fixSummary(it), ".")
	if it.Status == "rejected" {
		return out + ". Declining one suggestion establishes only that this one was " +
			"declined; another fix may still work."
	}
	if it.Target != "" {
		out += ". It was recorded against " + it.Target
	}
	if phrase := status.ArtifactPhrase(it.Tier, it.Artifact); phrase != "" {
		return out + ". Now " + phrase + "."
	}
	return out + "."
}

// resultDetail is the result label with its own explanation under it.
func resultDetail(it status.ScoredIntervention) string {
	label := status.ObservationLabel(it.Observation)
	sentence := status.ActionSentence(it.Observation, it.Scope, it.Tier, it.Artifact)
	if sentence == "" {
		return label + ". Nothing is asked of you here."
	}
	return label + ". " + sentenceCase(sentence)
}

// evidenceDetail is the before and after counts on their own lines, each with its
// denominator and the right noun. Under a withheld result the counts describe each
// period and compare neither, so the block says so.
func evidenceDetail(it status.ScoredIntervention, width int) string {
	out := headingStyle.Render("Evidence") + "\n"
	out += evidencePeriod("Before:", it.Before, it.Unit, it.BeforeCoverage, width)
	out += "\n" + evidencePeriod("After:", it.After, it.Unit, it.AfterCoverage, width)
	if it.Observation.Firing.Applicable {
		if it.Observation.Firing.LogPresent {
			out += wrapTo(fmt.Sprintf("guard activity: %d blocks in the after period (%s)", it.Observation.Firing.Rows, it.Observation.Firing.Scope), detailIndent, width)
		} else {
			out += wrapTo("guard activity is unknown because the audit log is absent", detailIndent, width)
		}
	}
	if phrase := status.CheckActivityPhrase(it.Observation); phrase != "" {
		out += wrapTo(phrase, detailIndent, width)
	}
	out += wrapTo("These counts describe the recorded evidence. They do not establish that the fix caused the change.", detailIndent, width)
	return out
}

// incidenceLine is one window's occurrences over its denominator, in the noun the
// record is counted in.
func incidenceLine(in cadence.Incidence, unit string) string {
	return fmt.Sprintf("%s in %d %s", recurrences(in.Hits), in.Units,
		status.UnitName(unit, in.Units))
}

// actionDetail is the entry's action area: the step where one exists, then what
// stopping the tracking would and would not do.
func actionDetail(it status.ScoredIntervention, width int) string {
	out := headingStyle.Render("What you can do") + "\n"
	if _, next := summaryReason(it); next != "" {
		out += wrapTo(sentenceCase(next), detailIndent, width)
	}
	out += wrapTo("x — stop tracking. "+sentenceCase(status.RemovalPhrase(it.Tier, it.Artifact)),
		detailIndent, width)
	return out
}

// sentenceCase capitalizes a phrase that starts a sentence of its own.
func sentenceCase(s string) string {
	r := []rune(s)
	if len(r) == 0 {
		return s
	}
	return string(unicode.ToUpper(r[0])) + string(r[1:])
}

// technicalBlock holds what supports inspection and repair: the exact pattern,
// the path, the eligibility rule, the range and the identifiers. It is behind t
// because none of it is needed to read the result.
func technicalBlock(r problemRow, width int) string {
	out := headingStyle.Render("Technical details") + "\n"
	if r.candidate != nil {
		c := *r.candidate
		out += wrapTo("source        "+c.Corpus, detailIndent, width)
		if c.Dump != "" {
			out += wrapTo("read them     "+c.Dump, detailIndent, width)
		}
		return out
	}
	it := *r.item
	rows := [][2]string{
		{"record id", it.ID},
		{"identified by", "/" + it.Match + "/"},
		{"type", status.TierPhrase(it.Tier, it.Target)},
		{"counted over", status.UnitName(it.Unit, 2) + " from " + it.Corpus},
		{"work included", status.ScopePhrase(it.Scope)},
	}
	if it.SignalKind != "" {
		rows = append(rows, [2]string{"signal", it.SignalKind})
	}
	if a := it.Artifact; a != nil {
		if a.ID != "" {
			rows = append(rows, [2]string{"fix id", a.ID})
		}
		if a.Scope != "" {
			rows = append(rows, [2]string{"store", a.Scope})
		}
		if a.Repo != "" {
			rows = append(rows, [2]string{"repository", a.Repo})
		}
		rows = append(rows, [2]string{"fix state", a.State})
		if a.RemovalRecorded() {
			rows = append(rows, [2]string{"removal archived", a.Removed})
		}
	}
	rows = append(rows, [2]string{"observation", status.ObservationLabel(it.Observation)})
	if it.LegacyNote != "" {
		rows = append(rows, [2]string{"older note", it.LegacyNote})
	}
	rows = append(rows, [2]string{"stop tracking", status.RetireCommand(it.ID)})
	for _, row := range rows {
		out += wrapTo(fmt.Sprintf("%-14s %s", row[0], row[1]), detailIndent, width)
	}
	return out
}

// verdictBadge colors a result by whether it demands action, and prints the label
// rather than the token. The results the cadence sweep queues read as warnings;
// the ones it leaves quiet read as dim.
func verdictBadge(obs mine.Observation) string {
	label := status.ObservationLabel(obs)
	if obs.Recurrence.Occurred {
		return warn(label)
	}
	return dim(label)
}

// recurrences counts occurrences with its own noun, so no bare number reaches a
// line without saying what it counts.
func recurrences(n int) string {
	return fmt.Sprintf("%d %s", n, plural("recurrence", n))
}

// plural is the TUI's own English plural for the nouns it owns.
func plural(word string, n int) string {
	if n == 1 {
		return word
	}
	if word == "recurrence" {
		return "recurrences"
	}
	return word + "s"
}

// expandedSummary leaves space for readable detail in a short terminal.
func (m *Model) expandedSummary(r problemRow) string {
	summary := m.problemSummary(r, true, m.bodyWidth())
	if m.bodyBudget() <= 0 {
		return summary
	}
	lines := strings.Split(strings.TrimRight(summary, "\n"), "\n")
	keep := max(1, (m.bodyBudget()-m.problemWindowOverhead()-2)/2)
	if len(lines) > keep {
		lines = lines[:keep]
	}
	return strings.Join(lines, "\n") + "\n"
}

func (m *Model) problemWindowOverhead() int {
	if m.bodyBudget() < 10 {
		return 0
	}
	return lipgloss.Height(strings.TrimRight(m.title("Problems — what recurs, and whether our fixes help"), "\n")) + 2
}

func evidencePeriod(label string, incidence cadence.Incidence, unit string, coverage cadence.Coverage, width int) string {
	indent := detailIndent + "        "
	lines := strings.Split(strings.TrimRight(wrapTo(incidenceLine(incidence, unit), indent, width), "\n"), "\n")
	lines[0] = detailIndent + secondary(fmt.Sprintf("%-8s", label)) + strings.TrimPrefix(lines[0], indent)
	return strings.Join(lines, "\n") + "\n" + wrapTo(secondary("Coverage: "+status.CoveragePhrase(coverage)), indent, width)
}
