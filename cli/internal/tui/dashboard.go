// Package tui is the steer dashboard: a control panel over the loop's write
// surface. Every panel is one you can act in — arm a channel, route a candidate,
// retire a class, toggle a preset. Each action calls the same package function
// the equivalent CLI command calls, so a write is never reimplemented. The read
// model is one status.SystemStatus, recomposed after every mutation.
package tui

import (
	"fmt"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

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
	panelCandidates
	panelClasses
	panelPresets
	panelReviewer
	panelHealth
	panelCount
)

var panelNames = [panelCount]string{"Channels", "Candidates", "Classes", "Hooks & presets", "Reviewer", "Health"}

// Model is the dashboard's whole state: the composed snapshot, the active panel,
// a per-panel cursor, and the candidate regex editor.
type Model struct {
	cfg gate.Config
	// targets are the harnesses a preset toggle registers into. The dashboard
	// takes them from the same flags the CLI reads, so a toggle here and a
	// `steer preset enable` write the same registrations.
	targets []harness.Registrar
	scope   string
	snap    *status.SystemStatus
	chains  []mine.Chain

	active  panel
	cursor  [panelCount]int
	width   int
	height  int
	message string
	err     error

	// candidate regex workbench + register/reject wizard
	input      textinput.Model
	editing    bool
	form       *formState
	matchCount string

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
	in.Placeholder = "regex that identifies this problem"
	in.CharLimit = 200
	in.Prompt = "match /"
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
	case panelCandidates:
		return len(m.snap.Attention.Candidates)
	case panelClasses:
		return len(m.snap.Interventions)
	case panelPresets:
		return len(m.snap.Presets)
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
	case tea.KeyMsg:
		if m.editing {
			// The two editors share one text input and answer different keys, so the
			// active panel routes the keystroke.
			if m.active == panelReviewer {
				return m.reviewerEditing(msg)
			}
			return m.updateEditing(msg)
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
		m.active = (m.active + 1) % panelCount
		return m, nil
	case "shift+tab", "left", "h":
		m.active = (m.active + panelCount - 1) % panelCount
		return m, nil
	case "up", "k":
		m.cursor[m.active]--
		m.clampCursor(m.active)
		return m, nil
	case "down", "j":
		m.cursor[m.active]++
		m.clampCursor(m.active)
		return m, nil
	case "r":
		m.refresh()
		m.note("refreshed")
		return m, nil
	}
	return m.panelKey(msg)
}

// panelKey dispatches an action key to the active panel.
func (m *Model) panelKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.active {
	case panelChannels:
		return m.channelsKey(msg)
	case panelCandidates:
		return m.candidatesKey(msg)
	case panelClasses:
		return m.classesKey(msg)
	case panelPresets:
		return m.presetsKey(msg)
	case panelReviewer:
		return m.reviewerKey(msg)
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
	case panelCandidates:
		body = m.viewCandidates()
	case panelClasses:
		body = m.viewClasses()
	case panelPresets:
		body = m.viewPresets()
	case panelReviewer:
		body = m.viewReviewer()
	case panelHealth:
		body = m.viewHealth()
	}
	return m.chrome(body)
}

// chrome wraps a panel body in the tab bar, a scope line and the key legend.
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
	scope := styles.MutedText.Render(fmt.Sprintf("scope: %s   instrument: %s", m.scope, m.snap.Posture.Instrument))

	msg := ""
	if m.message != "" {
		msg = "\n" + styles.InfoText.Render("• "+m.message)
	}
	legend := styles.MutedText.Render(m.legend())
	return fmt.Sprintf("%s\n%s\n\n%s%s\n\n%s\n", header, scope,
		windowBody(body, m.bodyBudget()), msg, legend)
}

// bodyBudget is the number of body lines that fit under the chrome: the tab bar,
// the scope line, the blank between them, the note and the key legend. It is zero
// until the harness reports a size, and zero never windows.
func (m *Model) bodyBudget() int {
	if m.height <= 0 {
		return 0
	}
	overhead := 6
	if m.message != "" {
		overhead += 2
	}
	if budget := m.height - overhead; budget > 0 {
		return budget
	}
	return 1
}

// legend is the per-panel key hint line.
func (m *Model) legend() string {
	common := "tab/←→ panel · ↑↓ move · r refresh · q quit"
	switch m.active {
	case panelChannels:
		return "enter toggle arming · " + common
	case panelCandidates:
		if m.editing {
			return "enter advance/write · shift+tab back · esc cancel"
		}
		return "e edit regex · x reject · " + common
	case panelClasses:
		return "x retire · " + common
	case panelPresets:
		return "enter enable/disable · s sync · b rebuild master · " + common
	case panelReviewer:
		if m.editing {
			return "enter write · esc cancel"
		}
		return "enter edit setting · " + common
	}
	return common
}

// note sets the transient status message.
func (m *Model) note(format string, a ...any) {
	m.message = fmt.Sprintf(format, a...)
}
