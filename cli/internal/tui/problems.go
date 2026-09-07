package tui

import (
	"fmt"
	"strings"
	"time"

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

// headingToConsider heads the queue rows. They sit above the tracked inventory
// because a problem with no fix is the one the reader has not dealt with.
const headingToConsider = "Problems to consider — no fix is proposed yet"

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
	// unmeasurable is this record's reading of required_after, computed once so
	// the summary, the detail and the section it files under cannot disagree.
	unmeasurable bool
}

// problemRows builds the list in render order: the queue first, then the tracked
// inventory grouped by the action each record asks for. The cursor indexes this
// order, so an arrow key walks the list down the screen.
func (m *Model) problemRows() []problemRow {
	if m.snap == nil {
		return nil
	}
	var rows []problemRow
	for i := range m.snap.Attention.Candidates {
		rows = append(rows, problemRow{
			heading:   headingToConsider,
			candidate: &m.snap.Attention.Candidates[i],
		})
	}
	items := m.snap.Interventions
	for _, i := range classOrder(items, m.snap.Rate) {
		it := items[i]
		un := status.Unmeasurable(it.Verdict, it.Unit, it.RequiredAfter, m.snap.Rate)
		rows = append(rows, problemRow{
			heading:      status.ActionHeading[status.ActionFor(it.Verdict, un)],
			item:         &items[i],
			unmeasurable: un,
		})
	}
	return rows
}

// classOrder is the order the tracked inventory renders in: grouped by the action
// each record asks for, in status.ActionOrder.
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
	v := problemView(it, r.unmeasurable)
	fields := []summaryField{
		{fixLabel(it), fixSummary(it)},
		{"Result", verdictBadge(it.Verdict, r.unmeasurable)},
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

// problemTitle names the entry. A queue row that points at a findings dump names
// no problem yet, so it says so rather than inventing a title.
func problemTitle(r problemRow) string {
	if r.item != nil {
		return r.item.Class
	}
	if r.candidate.Pattern != "" {
		return r.candidate.Pattern
	}
	return "Review findings not yet grouped into a problem"
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
	return status.FixLabel(problemView(it, false))
}

// fixSummary describes what the fix does and what type it is. It never claims the
// fix is installed and running: the record carries what was decided, not current
// setup evidence, and the target path belongs in the detail area.
func fixSummary(it status.ScoredIntervention) string {
	v := problemView(it, false)
	// The path belongs in the detail area, so the summary names the type alone.
	v.Target = ""
	return status.FixSummary(v)
}

// problemView reduces a tracked record to the fields the shared phrasing reads,
// so this panel and the terminal cannot answer one record differently.
func problemView(it status.ScoredIntervention, unmeasurable bool) status.ProblemView {
	return status.ProblemView{
		Declined:       it.Status == "rejected",
		Tier:           it.Tier,
		Target:         it.Target,
		Intervention:   it.Intervention,
		Verdict:        it.Verdict,
		Unit:           it.Unit,
		Unmeasurable:   unmeasurable,
		BeforeCoverage: it.BeforeCoverage,
		AfterCoverage:  it.AfterCoverage,
		Attribution:    it.Attribution,
		Confounds:      it.Confounds,
	}
}

// summaryReason splits the entry's one explanatory sentence into the reason a
// result is unavailable and the step the reader can take. A result the scorer
// withheld carries a reason and no step; a result that asks for work carries a
// step. An unreachable window is the exception: its sentence names two choices,
// so it is a step even though the entry files under accept.
func summaryReason(it status.ScoredIntervention, unmeasurable bool) (why, next string) {
	v := problemView(it, unmeasurable)
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
	out += detailSection("Result", resultDetail(it, r.unmeasurable), width)
	out += evidenceDetail(it, width)
	out += limitsDetail(it, width)
	out += actionDetail(it, r.unmeasurable, width)
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
	if c.Pattern != "" {
		out += detailSection("How it was matched", c.Pattern, width)
	} else {
		out += detailSection("Not grouped yet",
			"These findings are not sorted into problems yet, so no title and no fix exist for them. "+
				"Read them with: "+c.Dump, width)
	}
	for _, ex := range c.Examples {
		out += wrapTo("· "+ex, detailIndent, width)
	}
	return out + detailSection("What to do",
		"Run /steer-report in your coding agent. It reads this evidence with the findings in "+
			"context and asks you which fixes to apply or skip.", width)
}

// fixDetail describes the fix and says what this screen does and does not know
// about it. A tracking record proves the fix was decided on, never that it is
// installed and running right now.
func fixDetail(it status.ScoredIntervention) string {
	out := fixSummary(it)
	if it.Status == "rejected" {
		return out + ". Declining one suggestion establishes only that this one was " +
			"declined; another fix may still work."
	}
	if it.Target != "" {
		out += ". It was recorded against " + it.Target
	}
	return out + ". Steer recorded this fix; this screen does not check that it is " +
		"still set up. The Guards panel reads what is registered now."
}

// resultDetail is the result label with its own explanation under it.
func resultDetail(it status.ScoredIntervention, unmeasurable bool) string {
	label := status.VerdictLabel(it.Verdict, unmeasurable)
	sentence := status.ActionSentence(it.Verdict, unmeasurable)
	if sentence == "" {
		return label + ". Nothing is asked of you here."
	}
	return label + ". " + sentence
}

// evidenceDetail is the before and after counts on their own lines, each with its
// denominator and the right noun. Under a withheld result the counts describe each
// period and compare neither, so the block says so.
func evidenceDetail(it status.ScoredIntervention, width int) string {
	if it.Verdict == "unscored" || it.Verdict == "" {
		return ""
	}
	out := headingStyle.Render("Evidence") + "\n"
	out += evidencePeriod("Before:", it.Before, it.Unit, it.BeforeCoverage, width)
	out += "\n" + evidencePeriod("After:", it.After, it.Unit, it.AfterCoverage, width)
	if a := status.AttributionPhrase(it.Attribution); a != "" {
		out += wrapTo(a, detailIndent, width)
	}
	if !claimsARange(it.Verdict) {
		out += wrapTo("These counts describe each period. They do not compare them, so a "+
			"smaller number after the fix is not evidence that the fix caused it.",
			detailIndent, width)
	}
	return out
}

// claimsARange says whether the scorer placed an effect for this result. Only
// those two results carry a comparison; every other one carries counts alone.
func claimsARange(verdict string) bool {
	return verdict == "effective" || verdict == "harmful"
}

// incidenceLine is one window's occurrences over its denominator, in the noun the
// record is counted in.
func incidenceLine(in cadence.Incidence, unit string) string {
	return fmt.Sprintf("%s in %d %s", recurrences(in.Hits), in.Units,
		status.UnitName(unit, in.Units))
}

// limitsDetail names every changed condition, separating the ones that stop a
// comparison from the ones that only qualify a permitted result.
func limitsDetail(it status.ScoredIntervention, width int) string {
	out := ""
	if limit := status.ComparisonLimit("confound", it.Confounds); limit != "" {
		out += headingStyle.Render("What stops the comparison") + "\n"
		out += wrapTo(limit, detailIndent, width)
		for _, cause := range status.ComparisonCauses(it.Confounds) {
			out += wrapTo(cause, detailIndent+"  ", width)
		}
	}
	if limit := status.ComparisonLimit("composition", it.Composition); limit != "" {
		out += headingStyle.Render("What qualifies the result") + "\n"
		out += wrapTo(limit, detailIndent, width)
		for _, cause := range status.ComparisonCauses(it.Composition) {
			out += wrapTo(dim(cause), detailIndent+"  ", width)
		}
	}
	return out
}

// actionDetail is the entry's action area: the step where one exists, then what
// stopping the tracking would and would not do.
func actionDetail(it status.ScoredIntervention, unmeasurable bool, width int) string {
	out := headingStyle.Render("What you can do") + "\n"
	if _, next := summaryReason(it, unmeasurable); next != "" {
		out += wrapTo(next, detailIndent, width)
	}
	out += wrapTo("x — stop tracking. "+status.RemovalPhrase(it.Tier, it.Target),
		detailIndent, width)
	return out
}

// technicalBlock holds what supports inspection and repair: the exact pattern,
// the path, the eligibility rule, the range and the identifiers. It is behind t
// because none of it is needed to read the result.
func technicalBlock(r problemRow, width int) string {
	out := headingStyle.Render("Technical details") + "\n"
	if r.candidate != nil {
		c := *r.candidate
		out += wrapTo("source        "+c.Corpus, detailIndent, width)
		if c.Pattern != "" {
			out += wrapTo("pattern       /"+c.Pattern+"/", detailIndent, width)
		}
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
	if it.HookID != "" {
		rows = append(rows, [2]string{"guard id", it.HookID})
	}
	if e := status.EffectPhrase(it.Effect); e != "" {
		rows = append(rows, [2]string{"range", e})
	}
	if it.PValue > 0 {
		rows = append(rows, [2]string{"p", fmt.Sprintf("%.3f", it.PValue)})
	}
	if req := status.RequiredPhrase(it.RequiredAfter, it.Unit, it.Verdict); req != "" {
		rows = append(rows, [2]string{"still needs", req})
	}
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
