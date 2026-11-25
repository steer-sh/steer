// Package statusline renders steer's row for the Claude Code status line. The row
// answers one question — will a commit here be reviewed — and names the command that
// changes the answer.
package statusline

import (
	"fmt"
	"os"
	"strings"

	"github.com/steer-sh/steer-cli/internal/cadence"
	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/gate"
	"github.com/steer-sh/steer-cli/internal/gate/reviewers"
	"github.com/steer-sh/steer-cli/internal/harness"
	"github.com/steer-sh/steer-cli/internal/settings"
	"github.com/steer-sh/steer-cli/internal/status"
)

// Prefix names the row, so it stays identifiable under another script's output.
const Prefix = "steer"

const (
	armedGlyph   = "⏻"
	unarmedGlyph = "✕"
	warnGlyph    = "⚠"
	separator    = " · "
)

// Inputs are the facts the row is rendered from. Collect fills them; Render reads
// them and touches no disk.
type Inputs struct {
	// Armed is true when a commit gate is registered under the matcher that fires
	// on a Bash call, in either scope.
	Armed bool
	// Model and Runs are the configured reviewer. An empty model or a zero Runs
	// leaves the reviewer off the row.
	Model string
	Runs  int
	// Effort is the reviewer's reasoning effort, "" when the provider's own applies.
	Effort string
	// Rounds is how many times in one sequence the gate may block a commit; zero
	// leaves it off the row.
	Rounds int
	// Trouble names a condition under which an armed gate does not review a commit,
	// "" when there is none. TroubleFix is the command that clears it.
	Trouble    string
	TroubleFix string
	// Decisions is the number of queue entries waiting on the operator.
	Decisions int
	// Observed is true when the ledger holds at least one row.
	Observed bool
	// RemovedPlugin names the harness whose Steer plugin the stable executable came
	// from, once that harness no longer has it installed; "" otherwise.
	RemovedPlugin string
	// UninstallCommand removes that harness's registrations.
	UninstallCommand string
}

// Render builds the row. The tail carries the most urgent single item: a removed
// plugin whose hooks still run, a gate that is not armed, an armed gate that does not
// review, then decisions waiting, then whether the gate has ever fired.
func Render(in Inputs) string {
	if in.RemovedPlugin != "" {
		return fmt.Sprintf("%s %s %s plugin removed, hooks still run → %s",
			Prefix, warnGlyph, in.RemovedPlugin, in.UninstallCommand)
	}
	if !in.Armed {
		return fmt.Sprintf("%s %s gate off → steer commit-gate install", Prefix, unarmedGlyph)
	}

	if in.Trouble != "" {
		return fmt.Sprintf("%s %s %s → %s", Prefix, warnGlyph, in.Trouble, in.TroubleFix)
	}

	parts := []string{}
	if reviewer := reviewerLabel(in.Model, in.Runs, in.Effort); reviewer != "" {
		parts = append(parts, reviewer)
	}
	if in.Rounds > 0 {
		parts = append(parts, roundsPhrase(in.Rounds))
	}
	switch {
	case in.Decisions > 0:
		parts = append(parts, fmt.Sprintf("%s → /steer-report", decisionsPhrase(in.Decisions)))
	case !in.Observed:
		parts = append(parts, "never fired")
	default:
		parts = append(parts, "healthy")
	}
	return fmt.Sprintf("%s %s %s", Prefix, armedGlyph, strings.Join(parts, separator))
}

// decisionsPhrase carries the noun with the number. A bare count on a status line
// names nothing the reader can act on.
func decisionsPhrase(n int) string {
	if n == 1 {
		return "1 pending decision"
	}
	return fmt.Sprintf("%d pending decisions", n)
}

// roundsPhrase names the blocking round limit by the reviewer.rounds setting's noun.
func roundsPhrase(n int) string {
	if n == 1 {
		return "1 round"
	}
	return fmt.Sprintf("%d rounds", n)
}

// reviewerLabel names the reviewer the row reports. The model is printed as it is
// configured: the version is what distinguishes one reviewer from the next, and it
// is the half a shortening rule drops first. A configured effort follows it.
func reviewerLabel(model string, runs int, effort string) string {
	model = strings.TrimSpace(model)
	if model == "" || runs <= 0 {
		return ""
	}
	label := fmt.Sprintf("%d×%s", runs, model)
	if effort = strings.TrimSpace(effort); effort != "" {
		label += " @ " + effort
	}
	return label
}

// Collect reads the row's facts. Every read fails soft: a missing settings.json,
// ledger or queue leaves its field zero rather than failing the row.
func Collect() Inputs {
	armed := effectiveGates()
	in := Inputs{Armed: len(armed) > 0}
	if removed := status.RemovedPlugins(); len(removed) > 0 {
		id := removed[0]
		in.RemovedPlugin = status.HarnessLabel(string(id))
		in.UninstallCommand = status.UninstallCommand(id)
	}

	if cfg, err := gate.Load(); err == nil {
		// A provider that does not resolve runs no reviewer, so the row names none.
		resolved, reviewer, resolveErr := settings.Resolve(cfg, nil)
		if resolveErr == nil {
			in.Model = resolved.ReviewerModel
			// A `<provider>-default` model is the CLI's own default and names no model.
			if in.Model == reviewer.Name()+"-default" {
				in.Model = reviewer.Name()
			}
		}
		in.Runs = cfg.ReviewerRuns
		in.Effort = cfg.ReviewerEffort
		in.Rounds = cfg.MaxFirings
		in.Trouble, in.TroubleFix = trouble(settings.ReviewerProbe(cfg, resolveErr), armed, cfg.HookTimeout())
		if info, err := os.Stat(cfg.LogFile()); err == nil && info.Size() > 0 {
			in.Observed = true
		}
	}

	if q, err := cadence.ReadQueue(cadence.StateDir()); err == nil {
		candidates, flagged := cadence.PendingCounts(q, cadence.LoadReadMarks(cadence.StateDir()))
		in.Decisions = candidates + flagged
	}
	return in
}

// trouble names the first condition under which an armed gate does not review: a
// reviewer that cannot run, then a registration whose harness timeout stops a slow
// review. need is the timeout, in seconds, a registration needs.
func trouble(probe reviewers.Probe, armed []status.ScopeArming, need int) (what, fix string) {
	if _, missing := status.ReviewerStep(probe); missing {
		return "reviewer cannot run", "steer status"
	}
	for _, a := range armed {
		if step, short := status.GateTimeoutStep(a, need); short {
			return fmt.Sprintf("hook timeout %ds, a review may take %ds", a.Timeout, need), step.Fix
		}
	}
	return "", ""
}

// effectiveGates lists the Claude Code registrations that fire on a commit.
func effectiveGates() []status.ScopeArming {
	var out []status.ScopeArming
	for _, scope := range []string{claude.ProjectScope, claude.GlobalScope} {
		if a := claude.CommitGateHookArmed(scope); a.Effective() {
			out = append(out, status.ScopeArming{Harness: string(harness.ClaudeCode), Scope: scope,
				Registered: true, Effective: true, Timeout: a.Timeout})
		}
	}
	return out
}
