// Package tui is the steer dashboard: a control panel over the loop's write
// surface. Each action calls the same package function the equivalent CLI
// command calls, so a write is never reimplemented. The read model is one
// status.SystemStatus, recomposed after every mutation.
package tui

import (
	"fmt"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/evidence"
	"github.com/steer-sh/steer-cli/internal/gate"
	"github.com/steer-sh/steer-cli/internal/gate/mine"
	"github.com/steer-sh/steer-cli/internal/harness"
	"github.com/steer-sh/steer-cli/internal/settings"
	"github.com/steer-sh/steer-cli/internal/status"
	"github.com/steer-sh/steer-cli/internal/styles"
)

// panel identifies one tab of the dashboard.
type panel int

const (
	panelChannels panel = iota
	panelProblems
	panelGuards
	panelReviewer
	panelDiagnostics
	panelCount
)

var panelNames = [panelCount]string{"Arming", "Problems", "Guards", "Reviewer", "Diagnostics"}

// Model is the dashboard's whole state: the composed snapshot, the active panel,
// a per-panel cursor, and the one text editor the panels share.
type Model struct {
	cfg gate.Config
	// targets are the harnesses a built-in guard toggle registers into. The
	// dashboard takes them from the same flags the CLI reads, so a toggle here and
	// a `steer preset enable` write the same registrations.
	targets []harness.Registrar
	scope   string
	snap    *status.SystemStatus
	chains  []mine.Chain

	active panel
	cursor [panelCount]int
	width  int
	height int
	// message is the last action's feedback and messagePanel is the panel it
	// belongs to. A panel renders only its own.
	message      string
	messagePanel panel
	err          error

	// the stop-tracking editor: one reason field, opened from the Problems panel
	input   textinput.Model
	editing bool
	form    *retireForm

	// expanded says the selected problem's detail area is open; technical says its
	// technical block is open too. detailOffset is the first visible line of that
	// area and detailLines is the block's line count, which the view records so a
	// paging key can clamp against it.
	expanded     bool
	technical    bool
	detailOffset int
	detailLines  int

	// reading is true while the Channels panel's outcome read is in flight. It
	// walks every transcript tree on the machine, so it runs as a command and this
	// flag keeps a second enter from starting a concurrent one.
	reading bool

	// reviewer is the pending settings edit, nil unless the Reviewer panel is editing.
	reviewer *reviewerEdit
	// impactOf grades a pending reviewer identity against the measurements still
	// collecting. It is a field so a test can drive the confirmation step without
	// authoring a corpus; every construction sets it to settings.Impact.
	impactOf func(gate.Config) []mine.InstrumentImpact

	quitting bool
}

// New builds a dashboard model over one scope, gate config and harness target set.
func New(cfg gate.Config, scope string, targets ...harness.Registrar) *Model {
	in := textinput.New()
	in.CharLimit = 200
	m := &Model{cfg: cfg, scope: scope, targets: targets, input: in, impactOf: settings.Impact}
	m.refresh()
	return m
}

// Run starts the dashboard event loop.
func Run(cfg gate.Config, scope string, targets ...harness.Registrar) error {
	p := tea.NewProgram(New(cfg, scope, targets...), tea.WithAltScreen())
	_, err := p.Run()
	return err
}

func (m *Model) Init() tea.Cmd { return nil }

// refresh recomposes the snapshot and the cached chains so panels render current
// state. Every mutation calls it.
func (m *Model) refresh() {
	// The dashboard mutates registrations, never the evidence: it reads the stored
	// snapshots and rebuilds none.
	ev := evidence.Gather(evidence.Options{GateStateDir: m.cfg.StateDir, AllProjects: true})
	s, err := status.Collect(status.Options{
		GateStateDir: m.cfg.StateDir,
		Config:       m.cfg,
		WindowDays:   30,
		AllProjects:  true,
		Scope:        m.scope,
		Evidence:     ev,
	})
	if err != nil {
		m.err = err
		return
	}
	m.snap = s
	m.chains = ev.Chains
	for i := range m.cursor {
		m.clampCursor(panel(i))
	}
}

// clampCursor keeps a panel's cursor inside its current row count.
func (m *Model) clampCursor(p panel) {
	n := m.rowCount(p)
	if n == 0 {
		m.cursor[p] = 0
		return
	}
	if m.cursor[p] >= n {
		m.cursor[p] = n - 1
	}
	if m.cursor[p] < 0 {
		m.cursor[p] = 0
	}
}

// rowCount is the selectable row count for a panel.
func (m *Model) rowCount(p panel) int {
	if m.snap == nil {
		return 0
	}
	switch p {
	case panelChannels:
		return len(m.channelRows())
	case panelProblems:
		return len(m.problemRows())
	case panelGuards:
		return len(m.guardRows())
	case panelReviewer:
		return len(settings.Fields)
	}
	return 0
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case outcomesReadMsg:
		m.outcomesRead(msg)
		return m, nil
	case tea.KeyMsg:
		if m.editing {
			// The two editors share one text input and answer different keys, so the
			// active panel routes the keystroke.
			if m.active == panelReviewer {
				return m.reviewerEditing(msg)
			}
			return m.retireEditing(msg)
		}
		return m.updateNav(msg)
	}
	return m, nil
}

// updateNav handles keys while navigating panels.
func (m *Model) updateNav(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		m.quitting = true
		return m, tea.Quit
	case "tab", "right", "l":
		m.leavePanel()
		m.active = (m.active + 1) % panelCount
		return m, nil
	case "shift+tab", "left", "h":
		m.leavePanel()
		m.active = (m.active + panelCount - 1) % panelCount
		return m, nil
	case "up", "k":
		m.cursor[m.active]--
		m.clampCursor(m.active)
		m.setExpanded(false)
		return m, nil
	case "down", "j":
		m.cursor[m.active]++
		m.clampCursor(m.active)
		m.setExpanded(false)
		return m, nil
	case "r":
		m.refresh()
		m.note("refreshed")
		return m, nil
	}
	return m.panelKey(msg)
}

// leavePanel closes what only means something on the panel being left: the
// expanded problem, and the feedback the panel's own last action wrote. A
// cancellation carried onto another page reads as that page's own state.
func (m *Model) leavePanel() {
	m.setExpanded(false)
	m.message = ""
}

// panelKey dispatches an action key to the active panel.
func (m *Model) panelKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.active {
	case panelChannels:
		return m.channelsKey(msg)
	case panelProblems:
		return m.problemsKey(msg)
	case panelGuards:
		return m.guardsKey(msg)
	case panelReviewer:
		return m.reviewerKey(msg)
	case panelDiagnostics:
		return m.diagnosticsKey(msg)
	}
	return m, nil
}

func (m *Model) View() string {
	if m.quitting {
		return ""
	}
	if m.err != nil {
		return styles.ErrorBox.Render(fmt.Sprintf("dashboard error: %v", m.err)) + "\n"
	}
	var body string
	switch m.active {
	case panelChannels:
		body = m.viewChannels()
	case panelProblems:
		body = m.viewProblems()
	case panelGuards:
		body = m.viewGuards()
	case panelReviewer:
		body = m.viewReviewer()
	case panelDiagnostics:
		body = m.viewDiagnostics()
	}
	return m.chrome(body)
}

// chrome wraps a panel body in the tab bar, a configure line and the key legend.
func (m *Model) chrome(body string) string {
	tabs := make([]string, panelCount)
	for i := 0; i < int(panelCount); i++ {
		name := panelNames[i]
		if panel(i) == m.active {
			tabs[i] = lipgloss.NewStyle().Foreground(styles.BrandPurple).Bold(true).Render("[" + name + "]")
		} else {
			tabs[i] = styles.MutedText.Render(" " + name + " ")
		}
	}
	header := lipgloss.JoinHorizontal(lipgloss.Top, tabs...)

	msg := ""
	if m.visibleMessage() != "" {
		msg = "\n" + styles.InfoText.Render("• "+m.visibleMessage())
	}
	legend := styles.MutedText.Render(m.legend())
	return fmt.Sprintf("%s\n%s\n\n%s%s\n\n%s\n", header, m.reachLine(),
		windowBody(body, m.bodyBudget()), msg, legend)
}

// reachLine says where a write on this screen lands, and only on the panels that
// write. Problems and Diagnostics read evidence from every project, so a scope
// label above them would say the list is filtered to one. The instrument was here
// too: it names how the evidence was produced, which is provenance for one result
// rather than a fact about the screen, so it moved into those results.
func (m *Model) reachLine() string {
	switch m.active {
	case panelChannels, panelGuards:
		return styles.MutedText.Render("Configure: " + scopeReach(m.scope))
	case panelReviewer:
		return styles.MutedText.Render("Configure: every project on this machine")
	}
	return styles.MutedText.Render("Reading evidence from every project on this machine")
}

// scopeReach names one installation reach in the reader's words.
func scopeReach(scope string) string {
	if scope == claude.ProjectScope {
		return "this project"
	}
	return "every project on this machine"
}

// visibleMessage is the feedback the active panel owns. A panel's own action
// wrote it, so it belongs on that panel and nowhere else.
func (m *Model) visibleMessage() string {
	if m.messagePanel != m.active {
		return ""
	}
	return m.message
}

// bodyBudget is the number of body lines that fit under the chrome: the tab bar,
// the scope line, the blank between them, the note and the key legend. It is zero
// until the harness reports a size, and zero never windows.
func (m *Model) bodyBudget() int {
	if m.height <= 0 {
		return 0
	}
	overhead := 6
	if m.visibleMessage() != "" {
		overhead += 2
	}
	if budget := m.height - overhead; budget > 0 {
		return budget
	}
	return 1
}

// legend is the per-panel key hint line. It names what the keys do here, so it
// changes with the panel and with whether a detail area is open.
func (m *Model) legend() string {
	common := "tab/←→ panel · ↑↓ move · r refresh · q quit"
	switch m.active {
	case panelChannels:
		// Enter does two things here, and which one depends on the selected row.
		// The hint names the action row only while it is on screen.
		if rows := m.channelRows(); len(rows) > 0 && rows[len(rows)-1].kind == "read-outcomes" {
			return "enter turn on/off or read outcomes · " + common
		}
		return "enter turn on/off · " + common
	case panelProblems:
		if m.editing {
			return "enter save · esc cancel"
		}
		if m.expanded {
			return "enter/esc close · t technical details · pgup/pgdn/home/end scroll · " +
				"x stop tracking · " + common
		}
		return "enter open · x stop tracking · " + common
	case panelGuards:
		return m.guardsLegend() + common
	case panelReviewer:
		if m.editing {
			return "enter save · esc cancel"
		}
		return "enter edit setting · " + common
	case panelDiagnostics:
		if m.technical {
			return "t hide technical details · " + common
		}
		return "t technical details · " + common
	}
	return common
}

// detailBudget is the lines an expanded detail area may use. It leaves the
// summary above it and the window's own indicator room, so opening an entry never
// pushes the entry off the screen.
func (m *Model) detailBudget() int {
	budget := m.bodyBudget()
	if budget <= 0 {
		return 0
	}
	if n := budget - 8; n > minDetailLines {
		return n
	}
	return minDetailLines
}

// minDetailLines is the shortest detail area worth paging through.
const minDetailLines = 4

// note sets the feedback message and files it under the panel now active, which
// is the panel whose action wrote it.
func (m *Model) note(format string, a ...any) {
	m.noteFor(m.active, format, a...)
}

// noteFor files the feedback under a named panel. An operation that finishes
// after the reader moved away names its own panel: m.active by then is a panel
// that did nothing.
func (m *Model) noteFor(p panel, format string, a ...any) {
	m.messagePanel = p
	m.message = fmt.Sprintf(format, a...)
}
