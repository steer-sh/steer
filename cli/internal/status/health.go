// The verdict `steer status` leads with: one answer over everything the installation
// needs, and the issues behind it. The text view, --json and the startup notice read
// the same issues.

package status

import (
	"fmt"
	"sort"
	"strings"

	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/gate/reviewers"
	"github.com/steer-sh/steer-cli/internal/harness"
	"github.com/steer-sh/steer-cli/internal/mods"
)

// Verdicts. Healthy covers installation and configuration; it does not promise that a
// future review or hook run succeeds.
const (
	VerdictHealthy   = "healthy"
	VerdictAttention = "needs-attention"
	VerdictUnchecked = "could-not-check"
)

// Issue severities. Attention is a known problem, unchecked a required reading Steer
// could not make, and info a fact that leaves the verdict alone.
const (
	SeverityAttention = "attention"
	SeverityUnchecked = "unchecked"
	SeverityInfo      = "info"
)

// Issue is one thing a reader of the verdict needs to know.
type Issue struct {
	Severity string   `json:"severity"`
	Title    string   `json:"title"`
	Detail   []string `json:"detail,omitempty"`
	// Fix is the action that clears the issue: a command or an instruction, "" when
	// there is none. A renderer prints it whole, so a command stays copyable.
	Fix string `json:"fix,omitempty"`
}

// Health is the verdict and what stands behind it.
type Health struct {
	Verdict string `json:"verdict"`
	Version string `json:"version"`
	// Headline is the first line a reader sees.
	Headline string `json:"headline"`
	// Ready names what the verdict covers on this machine.
	Ready  []string `json:"ready"`
	Issues []Issue  `json:"issues"`
	// Note is a fact that needs no action, "" when there is none.
	Note string `json:"note,omitempty"`
}

// HealthOf derives the verdict from a snapshot.
func HealthOf(s *SystemStatus, probe reviewers.Probe) Health {
	readiness := ReadinessOf(s, probe)
	running, hooks := s.Posture.Builds.Running, s.Posture.Builds.Hooks
	h := Health{Version: running.Label(), Ready: []string{}, Issues: []Issue{}, Note: readiness.Note}
	for _, step := range readiness.Steps {
		h.Issues = append(h.Issues, Issue{Severity: SeverityAttention, Title: capitalize(step.Title), Fix: step.Fix})
	}
	if probe.State == reviewers.ProbeUnknown {
		h.Issues = append(h.Issues, Issue{Severity: SeverityUnchecked,
			Title: fmt.Sprintf("Steer could not check the reviewer %q", probe.Provider)})
	}
	h.Issues = append(h.Issues, trustIssues(s)...)
	h.Issues = append(h.Issues, guardIssues(s)...)
	h.Issues = append(h.Issues, ruleIssues(s.Posture.Rules)...)
	h.Issues = append(h.Issues, checkIssues(s.Checks)...)
	for _, p := range s.StoreProblems {
		h.Issues = append(h.Issues, Issue{Severity: SeverityAttention,
			Title: "Steer cannot read the " + p.Scope + " guard list: " + p.Error,
			Fix:   "edit the entry named above; the guards already installed keep running"})
	}
	if s.Team.Error != "" {
		h.Issues = append(h.Issues, Issue{Severity: SeverityAttention,
			Title: "Steer cannot read the team lockfile", Detail: []string{s.Team.Error}})
	}
	h.Issues = append(h.Issues, modIssues(s)...)
	builds := BuildIssues(s.Posture.Plugins, hooks, running)
	h.Issues = append(h.Issues, builds...)
	if !hooks.Known() && len(hookRows(s)) > 0 {
		h.Issues = append(h.Issues, Issue{Severity: SeverityUnchecked, Title: "Steer could not read the build the hooks run"})
	}
	// A channel that is registered yet unconfirmed, with no step of its own, must not
	// leave a healthy verdict behind it.
	if !readiness.Reviewing && len(readiness.Steps) == 0 {
		h.Issues = append(h.Issues, Issue{Severity: SeverityUnchecked, Title: readiness.Headline})
	}

	h.Verdict = VerdictHealthy
	for _, issue := range h.Issues {
		switch issue.Severity {
		case SeverityAttention:
			h.Verdict = VerdictAttention
		case SeverityUnchecked:
			if h.Verdict == VerdictHealthy {
				h.Verdict = VerdictUnchecked
			}
		}
	}
	sort.SliceStable(h.Issues, func(i, j int) bool {
		return severityOrder(h.Issues[i].Severity) < severityOrder(h.Issues[j].Severity)
	})
	h.Headline = headline(h, buildsDiffer(builds))
	if h.Verdict == VerdictHealthy {
		h.Ready = readyLines(s, readiness, buildsAgree(s.Posture.Plugins, hooks))
	}
	return h
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func severityOrder(severity string) int {
	switch severity {
	case SeverityAttention:
		return 0
	case SeverityUnchecked:
		return 1
	}
	return 2
}

func headline(h Health, differ bool) string {
	state := "healthy"
	switch {
	case h.Verdict == VerdictAttention:
		state = "needs attention"
	case h.Verdict == VerdictUnchecked:
		state = "could not check everything"
	case differ:
		state = "configuration healthy; builds differ"
	}
	return "steer " + h.Version + " — " + state
}

// buildsDiffer reports a build difference among the info issues.
func buildsDiffer(issues []Issue) bool {
	for _, issue := range issues {
		if issue.Severity == SeverityInfo && issue.Title == buildsDifferTitle {
			return true
		}
	}
	return false
}

// buildsAgree reports at least one active plugin copy, all on the hooks' build.
func buildsAgree(plugins []PluginCopy, hooks BuildIdentity) bool {
	for _, p := range plugins {
		if p.CompareToHooks(hooks) != BuildSame {
			return false
		}
	}
	return len(plugins) > 0
}

// readyLines names what a healthy verdict covers here.
func readyLines(s *SystemStatus, r Readiness, agree bool) []string {
	var parts []string
	if r.Reviewing {
		parts = append(parts, "Commit review")
	}
	for _, m := range s.Posture.Masters {
		if m.State == MasterCurrent {
			parts = append(parts, "guards")
			break
		}
	}
	if s.Checks.Armed() {
		parts = append(parts, "repository checks")
	}
	out := []string{}
	switch len(parts) {
	case 0:
	case 1:
		out = append(out, parts[0]+" is ready.")
	case 2:
		out = append(out, parts[0]+" and "+parts[1]+" are ready.")
	default:
		out = append(out, strings.Join(parts[:len(parts)-1], ", ")+" and "+parts[len(parts)-1]+" are ready.")
	}
	if agree {
		out = append(out, "Hooks and plugins run the same build.")
	}
	return out
}

const buildsDifferTitle = "Builds differ"

// BuildIssues compares the active plugin copies with the hooks' build, and names the
// running CLI when it differs. An issue with a direction names the one action; a
// difference involving a development build offers none.
func BuildIssues(plugins []PluginCopy, hooks, running BuildIdentity) []Issue {
	var out []Issue
	var differ []string
	newest := newestAhead(plugins, hooks)
	for _, p := range plugins {
		label := HarnessLabel(p.Harness)
		own := BuildIdentity{Version: p.Version, Commit: p.Commit}
		pair := []string{"Hooks: " + hooks.DetailLabel(), label + " plugin: " + own.DetailLabel()}
		switch p.CompareToHooks(hooks) {
		case BuildNewer:
			// An install from a lesser copy would move the hooks backward after this one.
			if p.Path != newest.Path {
				continue
			}
			out = append(out, Issue{Severity: SeverityAttention, Title: "Hooks are behind the " + label + " plugin",
				Detail: pair, Fix: p.BinInstallCommand()})
		case BuildOlder:
			out = append(out, Issue{Severity: SeverityAttention, Title: "The " + label + " plugin is behind the hooks",
				Detail: pair, Fix: "update the Steer plugin in " + label})
		case BuildConflict:
			out = append(out, Issue{Severity: SeverityAttention,
				Title:  fmt.Sprintf("Release identity conflict: %s names two commits", hooks.Version),
				Detail: pair, Fix: "reinstall the published release of the Steer plugin in " + label})
		case BuildUnknown:
			out = append(out, Issue{Severity: SeverityUnchecked,
				Title:  "Steer could not read the " + label + " plugin's build",
				Detail: []string{p.Path}})
		case BuildUnordered:
			differ = append(differ, label+" plugin: "+own.DetailLabel())
		}
	}
	runningDiffers := running.Known() && hooks.Known() && running != hooks
	if len(differ) > 0 || runningDiffers {
		lines := []string{}
		if runningDiffers {
			lines = append(lines, "CLI: "+running.DetailLabel())
		}
		lines = append(lines, "Hooks: "+hooks.DetailLabel())
		lines = append(lines, differ...)
		out = append(out, Issue{Severity: SeverityInfo, Title: buildsDifferTitle, Detail: lines})
	}
	return out
}

// newestAhead is the plugin copy with the highest version among those ahead of the hooks.
func newestAhead(plugins []PluginCopy, hooks BuildIdentity) PluginCopy {
	var best PluginCopy
	for _, p := range plugins {
		if p.CompareToHooks(hooks) != BuildNewer {
			continue
		}
		if best.Path == "" || p.CompareToHooks(BuildIdentity{Version: best.Version, Commit: best.Commit}) == BuildNewer {
			best = p
		}
	}
	return best
}

// hookRow is one registered hook whose approval the harness reviews.
type hookRow struct {
	what, harness, scope, trust string
}

func hookRows(s *SystemStatus) []hookRow {
	var rows []hookRow
	for _, a := range s.Posture.CommitGate {
		if a.Registered {
			rows = append(rows, hookRow{"commit review", a.Harness, a.Scope, a.Trust})
		}
	}
	for _, a := range s.Posture.Resolver {
		if a.Registered {
			rows = append(rows, hookRow{"review-outcome reader", a.Harness, a.Scope, a.Trust})
		}
	}
	for _, m := range s.Posture.Masters {
		if m.Path != "" && !m.Detached() {
			rows = append(rows, hookRow{"guards", m.Harness, m.Scope, m.Trust})
		}
	}
	for _, a := range s.Checks.Runner {
		if a.Registered {
			rows = append(rows, hookRow{"check runner", a.Harness, a.Scope, a.Trust})
		}
	}
	return rows
}

// trustIssues groups hook approval problems per harness and scope, because one visit to
// /hooks clears them together. Steer reads no approval on an unlisted harness, so its
// unknown state cannot be cleared and raises nothing.
func trustIssues(s *SystemStatus) []Issue {
	type group struct{ harness, scope, trust string }
	whats := map[group][]string{}
	var order []group
	for _, row := range hookRows(s) {
		if !harness.Listed(harness.ID(row.harness)) {
			continue
		}
		switch row.trust {
		case TrustMissing, TrustModified, TrustUnknown, TrustRecordPresent, TrustDisabled, TrustHooksOff:
		default:
			continue
		}
		// ReadinessOf already raises a known-off gate and reader with their repairs.
		off := row.trust == TrustDisabled || row.trust == TrustHooksOff
		if off && (row.what == "commit review" || row.what == "review-outcome reader") {
			continue
		}
		key := group{row.harness, row.scope, row.trust}
		if _, seen := whats[key]; !seen {
			order = append(order, key)
		}
		whats[key] = append(whats[key], row.what)
	}
	var out []Issue
	for _, key := range order {
		label := HarnessLabel(key.harness)
		where := label + " " + key.scope
		covers := strings.Join(whats[key], ", ")
		step, _ := TrustStep(key.harness, key.trust, "")
		switch key.trust {
		case TrustMissing:
			out = append(out, Issue{Severity: SeverityAttention, Title: where + " hooks need approval",
				Detail: []string{"Hooks: " + covers}, Fix: "Open /hooks in " + label + " and approve Steer's hooks."})
		case TrustModified:
			out = append(out, Issue{Severity: SeverityAttention, Title: label + " hook approval needs checking",
				Detail: []string{"Steer's computed hash differs from " + label + "'s saved approval: " + covers},
				Fix:    "Open /hooks in " + label + "; approve any handlers marked for review."})
		case TrustDisabled, TrustHooksOff:
			out = append(out, Issue{Severity: SeverityAttention, Title: where + " hooks are turned off: " + covers, Fix: step.Fix})
		default:
			out = append(out, Issue{Severity: SeverityUnchecked, Title: "Steer could not check " + where + " hook approval",
				Detail: []string{"Hooks: " + covers}, Fix: "Inspect /hooks in " + label + "."})
		}
	}
	return out
}

// guardIssues reports guard runners that do not run what the store holds. A row for an
// unlisted harness counts only where it registers a runner.
func guardIssues(s *SystemStatus) []Issue {
	var out []Issue
	for _, m := range RunnersWithoutInterpreter(s.Posture.Masters, s.Posture.GuardInterpreter) {
		out = append(out, Issue{Severity: SeverityAttention,
			Title:  "Guards may allow every call: " + GuardInterpreter + " is not on this PATH",
			Detail: []string{HarnessLabel(m.Harness) + " " + m.Scope + ": " + m.Path},
			Fix:    "install Python 3, then run: steer status"})
		break
	}
	for _, m := range s.Posture.Masters {
		label := HarnessLabel(m.Harness) + " " + m.Scope
		switch {
		case m.Detached():
			if harness.Listed(harness.ID(m.Harness)) {
				out = append(out, Issue{Severity: SeverityAttention, Title: label + " runs none of this scope's guards",
					Fix: DetachedRepair(s.Posture.Masters, m.Scope)})
			}
		case m.Stale():
			out = append(out, Issue{Severity: SeverityAttention, Title: label + " guard runner is out of date",
				Fix: RebuildCommand(m.Scope)})
		case m.SourcesDrifted():
			out = append(out, Issue{Severity: SeverityAttention,
				Title: label + " guard runner carries an old copy of an edited guard", Fix: RebuildCommand(m.Scope)})
		case m.Sources != nil && m.Sources.State == claude.ComparisonUnavailable:
			out = append(out, Issue{Severity: SeverityUnchecked,
				Title: "Steer could not compare the " + label + " guard runner with its guard files", Detail: []string{m.Sources.Reason}})
		}
	}
	return out
}

func ruleIssues(rows []RulesPresence) []Issue {
	var out []Issue
	for _, r := range rows {
		label := HarnessLabel(r.Harness) + " " + r.Scope
		switch r.State {
		case RulesDrifted:
			out = append(out, Issue{Severity: SeverityAttention, Title: label + " instructions do not match Steer's rules",
				Fix: RulesRepairCommand(r.Scope)})
		case RulesUnknown:
			out = append(out, Issue{Severity: SeverityUnchecked, Title: "Steer could not read the " + label + " instructions",
				Detail: []string{r.Path}})
		}
	}
	return out
}

func checkIssues(c CheckTier) []Issue {
	var out []Issue
	if c.Error != "" {
		out = append(out, Issue{Severity: SeverityUnchecked, Title: "Steer could not read this repository's checks",
			Detail: []string{c.Error}})
	}
	if c.Manifest && !c.Adopted {
		out = append(out, Issue{Severity: SeverityInfo, Title: "This repository declares checks that this machine has not adopted",
			Fix: "steer check adopt"})
	}
	if !c.Adopted {
		return out
	}
	if !c.Armed() {
		out = append(out, Issue{Severity: SeverityAttention, Title: "This repository's checks are adopted, but no harness runs them",
			Fix: "steer check install --harness <claude-code|codex>"})
	}
	for _, a := range c.Runner {
		if !a.Registered {
			continue
		}
		label := HarnessLabel(a.Harness) + " " + a.Scope + " check runner: "
		if step, ok := RunnerTimeoutStep(a); ok {
			out = append(out, Issue{Severity: SeverityAttention, Title: label + step.Title, Fix: step.Fix})
		}
		if step, ok := CommandStep(a.Resolvable); ok {
			out = append(out, Issue{Severity: SeverityAttention, Title: label + step.Title, Fix: step.Fix})
		}
	}
	if c.LatestRun != nil && c.LatestRun.IntegrationFailure {
		out = append(out, Issue{Severity: SeverityAttention, Title: "A declared check did not report a pass in the last run, although check.sh exited cleanly",
			Detail: c.LatestRun.Missing, Fix: "steer check list"})
	}
	return out
}

// modIssues reports Claude Code mods. A mod that handles tool calls is the operator's
// own choice, so it is disclosed and leaves the verdict alone; a mod nobody inspected is
// an unanswered question.
func modIssues(s *SystemStatus) []Issue {
	registered := false
	for _, row := range hookRows(s) {
		registered = registered || row.harness == string(harness.ClaudeCode)
	}
	if !registered {
		return nil
	}
	var out []Issue
	for _, m := range s.Posture.Mods {
		switch m.State {
		case mods.StateToolHooks:
			out = append(out, Issue{Severity: SeverityInfo,
				Title: fmt.Sprintf("Claude Code mod %s handles %s, so it can skip or override Steer's hooks", m.ID, strings.Join(m.Handlers, ", "))})
		case mods.StateUninspected, mods.StateStale, mods.StateFailed:
			out = append(out, Issue{Severity: SeverityUnchecked,
				Title: fmt.Sprintf("Claude Code mod %s is %s, so whether it can skip Steer's hooks is unknown", m.ID, modStatePhrase(m.State)),
				Fix:   "steer mods inspect"})
		}
	}
	return out
}
