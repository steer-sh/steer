package status

import (
	"fmt"
	"strings"

	"github.com/steer-sh/steer-cli/internal/gate/reviewers"
	"github.com/steer-sh/steer-cli/internal/harness"
)

// A Step is one condition that must hold before a commit is reviewed, named with the
// command that makes it hold. Registration is the first of four states, not the last:
// the configuration names the command, the harness trusts it, the reviewer CLI
// resolves, and a review is finally observed. A reader is told what remains, never
// that the gate is armed while a step is still open.
type Step struct {
	// Title names what is not yet true.
	Title string
	// Fix is the command that clears the step, or "" when only the operator can.
	Fix string
}

// ReviewerStep reports the reviewer CLI as a missing step. A probe that checked
// nothing raises none: an unknown provider is not a claim of absence. A probe naming
// no executable is the caller's report that the provider itself does not resolve, so
// it names the setting rather than an install.
func ReviewerStep(p reviewers.Probe) (Step, bool) {
	if p.State != reviewers.ProbeMissing {
		return Step{}, false
	}
	if p.Executable == "" {
		return Step{
			Title: fmt.Sprintf("the configured reviewer provider %q resolves to no CLI, so every review fails", p.Provider),
			Fix:   "steer config set reviewer.provider <agy|codex|claude-code>",
		}, true
	}
	return Step{
		Title: fmt.Sprintf("the reviewer CLI %q does not resolve here, so every review fails", p.Executable),
		Fix:   fmt.Sprintf("put %s on PATH, or name another provider: steer config set reviewer.provider <agy|codex|claude-code>", p.Executable),
	}, true
}

// CommandStep reports a registered command this process cannot resolve. The harness
// runs the tool anyway and reports nothing, so this line is the only place the
// failure is visible.
func CommandStep(resolvable string) (Step, bool) {
	if resolvable != ResolveMissing {
		return Step{}, false
	}
	return Step{
		Title: "the registered command does not resolve here — the hook is skipped silently",
		Fix:   "steer bin install",
	}, true
}

// TrustStep reports a harness that reviews hook trust itself. Registration alone does
// not prove enforcement there, and only the operator can clear it.
func TrustStep(harnessID, trust, path string) (Step, bool) {
	switch trust {
	case TrustRecordPresent, TrustMissing, TrustUnknown:
	default:
		return Step{}, false
	}
	step := Step{Title: fmt.Sprintf("%s has not confirmed it trusts this hook (%s)",
		HarnessLabel(harnessID), TrustPhrase(trust))}
	switch harness.ID(harnessID) {
	case harness.Codex:
		step.Fix = "restart Codex and approve the definitions in /hooks"
		if path != "" {
			step.Title += ": " + path
		}
	case harness.Pi:
		step.Fix = "trust this project in Pi; a project extension runs only after that"
	}
	return step, true
}

// HarnessLabel is the display name, falling back to the id an unregistered harness
// carries so a label never names an empty harness. Exported because the dashboard
// names a harness on every channel row and must spell it the way a step does.
func HarnessLabel(id string) string {
	if entry, ok := harness.Get(harness.ID(id)); ok {
		return entry.DisplayName
	}
	return id
}

// Readiness is the short answer a reader wants before any inventory: will a commit
// here be reviewed, and what stands in the way.
type Readiness struct {
	// Reviewing is true when at least one registration is enforcing and the reviewer
	// can run.
	Reviewing bool
	// Headline is the answer in one clause.
	Headline string
	// StepsLead introduces confirmed setup problems.
	StepsLead string
	// Steps are what remains, in the order a reader clears them.
	Steps []Step
	// Unverified holds trust diagnostics, which do not establish a broken setup.
	Unverified []string
	// Note is one further fact, "" when there is none — that no review has been
	// observed yet, which is not proof of breakage.
	Note string
}

// ReadinessOf separates setup failures from trust that the snapshot cannot verify.
func ReadinessOf(s *SystemStatus, probe reviewers.Probe) Readiness {
	r := Readiness{}
	reviewerStep, reviewerMissing := ReviewerStep(probe)
	if reviewerMissing {
		r.Steps = append(r.Steps, reviewerStep)
	}
	var enforcing []string
	registered := false
	observed := false
	unknownTrust := false
	for _, a := range s.Posture.CommitGate {
		if !a.Registered {
			continue
		}
		registered = true
		open := false
		if !a.Effective {
			r.Steps = append(r.Steps, Step{
				Title: fmt.Sprintf("the %s %s-scope registration is inert — it runs for no commit",
					HarnessLabel(a.Harness), a.Scope),
				Fix: fmt.Sprintf("steer commit-gate install --harness %s --scope %s", a.Harness, a.Scope),
			})
			open = true
		}
		if step, ok := CommandStep(a.Resolvable); ok {
			r.Steps = append(r.Steps, prefixHarness(step, a))
			open = true
		}
		if _, ok := TrustStep(a.Harness, a.Trust, ""); ok {
			note := fmt.Sprintf("%s %s scope: %s; current hook trust cannot be verified", HarnessLabel(a.Harness), a.Scope, TrustPhrase(a.Trust))
			if phrase := ObservedPhrase(a); phrase != "" {
				note += " · " + phrase
			}
			r.Unverified = append(r.Unverified, note)
			if !open {
				unknownTrust = true
			}
			open = true
		}
		if open {
			continue
		}
		enforcing = append(enforcing, fmt.Sprintf("%s %s", HarnessLabel(a.Harness), a.Scope))
		if a.Observed > 0 {
			observed = true
		}
	}
	if registered {
		steps, unverified := resolverSteps(s.Posture)
		r.Steps = append(r.Steps, steps...)
		r.Unverified = append(r.Unverified, unverified...)
	}
	if !registered {
		r.Steps = append([]Step{{
			Title: "no commit gate is registered, so nothing reviews a commit here",
			Fix:   "steer commit-gate install --scope global",
		}}, r.Steps...)
	}
	r.Reviewing = len(enforcing) > 0 && !reviewerMissing
	switch {
	case r.Reviewing:
		r.Headline = "Commits here are reviewed before they land — " + strings.Join(enforcing, ", ") + "."
		r.StepsLead = "Setup problems:"
	case unknownTrust && len(r.Steps) == 0:
		r.Headline = "Commit review is configured; current hook trust cannot be verified by Steer."
		r.StepsLead = "Setup problems:"
	case registered:
		r.Headline = "Commit review has setup problems; no working channel is confirmed."
		r.StepsLead = "Setup problems:"
	default:
		r.Headline = "Commits here are NOT reviewed."
		r.StepsLead = "Before the first review:"
	}
	if r.Reviewing && !observed {
		r.Note = "no review observed yet — that is expected until the first commit in scope"
	}
	return r
}

// resolverSteps reports the resolver that reads review outcomes back. It never withholds
// the reviewing answer: a commit is still reviewed, but no result can count the review.
// Any working resolver reads every harness's ledger, so a missing one is a step only when
// no registration here resolves. A resolver whose trust is unconfirmed is a diagnostic,
// as the gate's own is: it may run, so it is no claim that none does.
func resolverSteps(p Posture) (steps []Step, unverified []string) {
	candidate := false
	for _, a := range p.Resolver {
		if !a.Registered {
			continue
		}
		if step, ok := CommandStep(a.Resolvable); ok {
			step.Title = "the review-outcome reader's command does not resolve here, so no review outcome is read"
			steps = append(steps, prefixHarness(step, a))
			continue
		}
		candidate = true
		if _, ok := TrustStep(a.Harness, a.Trust, ""); ok {
			unverified = append(unverified, fmt.Sprintf("%s %s scope: review-outcome reader: %s; current hook trust cannot be verified",
				HarnessLabel(a.Harness), a.Scope, TrustPhrase(a.Trust)))
		}
	}
	if candidate || len(steps) > 0 {
		return steps, unverified
	}
	for _, g := range p.CommitGate {
		if g.Registered {
			return []Step{{
				Title: "no review-outcome reader is registered beside the commit gate here, so review outcomes are read only when one runs elsewhere",
				Fix:   fmt.Sprintf("steer commit-gate install --harness %s --scope %s", g.Harness, g.Scope),
			}}, nil
		}
	}
	return nil, nil
}

// prefixHarness names the registration a step belongs to. One store serves several
// harnesses, so a bare step reads as the whole gate being broken.
func prefixHarness(step Step, a ScopeArming) Step {
	step.Title = fmt.Sprintf("%s %s scope: %s", HarnessLabel(a.Harness), a.Scope, step.Title)
	return step
}
