package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/steer-sh/steer-cli/internal/cadence"
	"github.com/steer-sh/steer-cli/internal/gate/mine"
	"github.com/steer-sh/steer-cli/internal/sessions"
)

// storedSessions reads the transcript-session snapshot store, the session corpus the
// wizard previews and writes against.
func storedSessions() []mine.SessionUnit {
	units, _ := sessions.Load(sessions.StateDir())
	return units
}

// formKind is which write a wizard commits when its last step is filled.
type formKind int

const (
	formRegister formKind = iota
	formReject
	formRetire
)

// formStep is one prompt of a wizard: a label and the value typed for it.
type formStep struct {
	label string
	value string
}

// formState is a running register or reject wizard over one candidate class.
type formState struct {
	kind  formKind
	cand  cadence.Candidate
	id    string // intervention id, for a retire wizard
	steps []formStep
	idx   int
}

// corpus is the evidence the wizard's class is counted over, taken from the candidate
// it opened on, so the preview and the write agree without asking the author.
func (f *formState) corpus() string {
	if f == nil || f.cand.Corpus == "" {
		return mine.CorpusReviewFindings
	}
	return f.cand.Corpus
}

// candidatesKey opens the register wizard on enter and the reject wizard on x.
func (m *Model) candidatesKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	cands := m.snap.Attention.Candidates
	if len(cands) == 0 {
		return m, nil
	}
	cand := cands[m.cursor[panelCandidates]]
	switch msg.String() {
	case "enter", "e":
		m.startForm(formRegister, cand)
	case "x":
		m.startForm(formReject, cand)
	}
	return m, nil
}

// startForm seeds a wizard with the candidate's pattern and opens the editor.
func (m *Model) startForm(kind formKind, cand cadence.Candidate) {
	class := cand.Pattern
	if class == "" {
		class = cand.Corpus
	}
	// A session carries no changed-path list, so the transcript corpus offers a signal
	// kind in place of the path predicate its scope cannot decide.
	scopeSteps := []formStep{
		{label: "scope-harness", value: ""},
		{label: "scope-repo", value: ""},
		{label: "scope-path", value: ""},
	}
	if cand.Corpus == mine.CorpusTranscript {
		scopeSteps = []formStep{
			{label: "signal-kind", value: mine.SignalAny},
			{label: "scope-harness", value: ""},
			{label: "scope-repo", value: ""},
		}
	}
	steps := []formStep{{label: "class", value: class}, {label: "match", value: cand.Pattern}}
	steps = append(steps, scopeSteps...)
	if kind == formRegister {
		steps = append(steps,
			formStep{label: "tier", value: "check"},
			formStep{label: "target", value: "check.sh"})
	} else {
		steps = append(steps,
			formStep{label: "reason", value: ""},
			formStep{label: "reopen", value: ""})
	}
	m.form = &formState{kind: kind, cand: cand, steps: steps}
	m.editing = true
	m.matchCount = ""
	m.loadStep()
}

// loadStep binds the text input to the current wizard step.
func (m *Model) loadStep() {
	step := m.form.steps[m.form.idx]
	m.input.Prompt = step.label + ": "
	m.input.SetValue(step.value)
	m.input.CursorEnd()
	m.input.Focus()
	m.updateMatchPreview()
}

// updateEditing drives the wizard: enter advances or commits, esc cancels.
func (m *Model) updateEditing(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.editing = false
		m.form = nil
		m.note("cancelled")
		return m, nil
	case "enter":
		m.form.steps[m.form.idx].value = strings.TrimSpace(m.input.Value())
		if m.form.idx < len(m.form.steps)-1 {
			m.form.idx++
			m.loadStep()
			return m, nil
		}
		return m, m.commitForm()
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.updateMatchPreview()
	return m, cmd
}

// updateMatchPreview recomputes the live preview under the active step: a match count
// on the match step, and the eligible/ineligible/unknown split on either scope step.
func (m *Model) updateMatchPreview() {
	if m.form == nil {
		m.matchCount = ""
		return
	}
	switch m.form.steps[m.form.idx].label {
	case "match":
		regex := strings.TrimSpace(m.input.Value())
		if regex == "" {
			m.matchCount = ""
			return
		}
		var buf strings.Builder
		err := mine.Match(&buf, m.cfg.StateDir, mine.MatchOpts{
			Regex:      regex,
			Corpus:     m.form.corpus(),
			SignalKind: m.scopeVal("signal-kind"),
			Sessions:   storedSessions(),
		})
		if err != nil {
			m.matchCount = "invalid regex: " + err.Error()
			return
		}
		m.matchCount = lastLine(buf.String())
	case "scope-harness", "scope-repo", "scope-path":
		if m.form.corpus() == mine.CorpusTranscript {
			cov, err := mine.CoverCorpusSessions(storedSessions(),
				splitList(m.scopeVal("scope-harness")), m.scopeVal("scope-repo"))
			if err != nil {
				m.matchCount = "invalid scope: " + err.Error()
				return
			}
			m.matchCount = fmt.Sprintf(
				"scope: %d eligible · %d ineligible · %d unknown scope, of %d sessions",
				cov.Eligible, cov.Ineligible, cov.Unknown, cov.Total)
			return
		}
		cov, err := mine.CoverCorpus(m.cfg.StateDir, "", splitList(m.scopeVal("scope-harness")),
			m.scopeVal("scope-repo"), m.scopeVal("scope-path"))
		if err != nil {
			m.matchCount = "invalid scope: " + err.Error()
			return
		}
		// Split the unknown column by cause: a chain the scope cannot classify is a
		// regex problem the author can fix here; one with no adjudication is not.
		m.matchCount = fmt.Sprintf("scope: %d eligible · %d ineligible · %d unknown scope · %d without a ledger, of %d chains",
			cov.Eligible, cov.Ineligible, cov.UnknownScope, cov.UnknownEvidence, cov.Total)
	default:
		m.matchCount = ""
	}
}

// scopeVal reads a scope step's value, taking the live input when it is the active step.
func (m *Model) scopeVal(label string) string {
	if m.form.steps[m.form.idx].label == label {
		return strings.TrimSpace(m.input.Value())
	}
	for _, s := range m.form.steps {
		if s.label == label {
			return s.value
		}
	}
	return ""
}

// commitForm runs the wizard's write, then refreshes and closes the editor.
func (m *Model) commitForm() tea.Cmd {
	val := func(label string) string {
		for _, s := range m.form.steps {
			if s.label == label {
				return s.value
			}
		}
		return ""
	}
	// An author who names no predicate means every chain, the all-scope.
	repo, path := val("scope-repo"), val("scope-path")
	harnesses := splitList(val("scope-harness"))
	scopeAll := repo == "" && path == "" && len(harnesses) == 0
	var buf strings.Builder
	var err error
	switch m.form.kind {
	case formRegister:
		err = mine.Register(&buf, m.cfg.StateDir, mine.RegisterOpts{
			Class:        val("class"),
			Match:        val("match"),
			Kind:         "accepted",
			Corpus:       m.form.corpus(),
			SignalKind:   val("signal-kind"),
			Sessions:     storedSessions(),
			Tier:         val("tier"),
			Target:       val("target"),
			ScopeAll:     scopeAll,
			ScopeHarness: harnesses,
			ScopeRepo:    repo,
			ScopePath:    path,
		})
	case formReject:
		err = mine.Reject(&buf, m.cfg.StateDir, mine.RejectOpts{
			Class:        val("class"),
			Match:        val("match"),
			Kind:         "accepted",
			Corpus:       m.form.corpus(),
			SignalKind:   val("signal-kind"),
			Sessions:     storedSessions(),
			Reason:       val("reason"),
			Reopen:       val("reopen"),
			ScopeAll:     scopeAll,
			ScopeHarness: harnesses,
			ScopeRepo:    repo,
			ScopePath:    path,
		})
	case formRetire:
		err = mine.Retire(&buf, m.cfg.StateDir, m.form.id, val("reason"), "removed")
	}
	label := m.form.id
	if label == "" {
		label = val("class")
	}
	m.editing = false
	m.form = nil
	if err != nil {
		m.note("error: %v", err)
		return nil
	}
	m.refresh()
	m.note("%q written", label)
	return nil
}

// viewCandidates renders the candidate queue, or the active wizard.
func (m *Model) viewCandidates() string {
	if m.editing && m.form != nil {
		return m.viewForm()
	}
	cands := m.snap.Attention.Candidates
	out := header("Candidates — new clusters pending routing")
	if len(cands) == 0 {
		return out + dim("none pending. The cadence sweep found no new clusters.")
	}
	for i, c := range cands {
		sel := i == m.cursor[panelCandidates]
		if c.Pattern != "" {
			out += fmt.Sprintf("%s %-30s %s\n", pointer(sel), c.Pattern,
				dim(fmt.Sprintf("%d hits · %d sessions · %s", c.Count, c.Sessions, c.Corpus)))
		} else {
			out += fmt.Sprintf("%s %-30s %s\n", pointer(sel), "("+c.Corpus+" dump)",
				dim(fmt.Sprintf("%d undecided chains · %s", c.UndecidedChains, c.Dump)))
		}
	}
	return out
}

// viewForm renders the running register or reject wizard with the live match
// count under the match step.
func (m *Model) viewForm() string {
	kind := "Register class"
	switch m.form.kind {
	case formReject:
		kind = "Reject class"
	case formRetire:
		kind = "Retire intervention " + m.form.id
	}
	out := header(kind + " — enter to advance, esc to cancel")
	for i, s := range m.form.steps {
		marker := "  "
		val := s.value
		if i == m.form.idx {
			marker = pointer(true)
			out += fmt.Sprintf("%s %s\n", marker, m.input.View())
			if m.matchCount != "" {
				out += "     " + dim(m.matchCount) + "\n"
			}
			continue
		}
		out += fmt.Sprintf("%s %s: %s\n", marker, s.label, val)
	}
	return out
}

// lastLine returns the last non-empty line of s, trimmed.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if t := strings.TrimSpace(lines[i]); t != "" {
			return t
		}
	}
	return ""
}

// splitList reads a comma-separated wizard field into a list, dropping empty entries.
func splitList(v string) []string {
	var out []string
	for _, part := range strings.Split(v, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}
