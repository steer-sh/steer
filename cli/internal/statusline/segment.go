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
)

// Prefix names the row, so it stays identifiable under another script's output.
const Prefix = "steer"

const (
	armedGlyph   = "⏻"
	unarmedGlyph = "✕"
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
	// Decisions is the number of queue entries waiting on the operator.
	Decisions int
	// Observed is true when the ledger holds at least one row.
	Observed bool
}

// Render builds the row. The tail carries the most urgent single item: a gate that
// is not armed, then decisions waiting, then whether the gate has ever fired.
func Render(in Inputs) string {
	if !in.Armed {
		return fmt.Sprintf("%s %s gate off → steer commit-gate install", Prefix, unarmedGlyph)
	}

	parts := []string{}
	if reviewer := reviewerLabel(in.Model, in.Runs); reviewer != "" {
		parts = append(parts, reviewer)
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
		return "1 decision"
	}
	return fmt.Sprintf("%d decisions", n)
}

// reviewerLabel names the reviewer the row reports. The model is printed as it is
// configured: the version is what distinguishes one reviewer from the next, and it
// is the half a shortening rule drops first.
func reviewerLabel(model string, runs int) string {
	model = strings.TrimSpace(model)
	if model == "" || runs <= 0 {
		return ""
	}
	return fmt.Sprintf("%d×%s", runs, model)
}

// Collect reads the row's facts. Every read fails soft: a missing settings.json,
// ledger or queue leaves its field zero rather than failing the row.
func Collect() Inputs {
	in := Inputs{Armed: gateArmed()}

	if cfg, err := gate.Load(); err == nil {
		in.Model = cfg.ReviewerModel
		in.Runs = cfg.ReviewerRuns
		if info, err := os.Stat(cfg.LogFile()); err == nil && info.Size() > 0 {
			in.Observed = true
		}
	}

	if q, err := cadence.ReadQueue(cadence.StateDir()); err == nil {
		candidates, flagged := cadence.PendingCounts(q)
		in.Decisions = candidates + flagged
	}
	return in
}

// gateArmed reports whether either scope carries a gate that fires on a commit.
func gateArmed() bool {
	for _, scope := range []string{claude.ProjectScope, claude.GlobalScope} {
		if claude.CommitGateHookArmed(scope).Effective() {
			return true
		}
	}
	return false
}
