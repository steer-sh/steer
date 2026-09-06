package tui

import (
	"fmt"

	"github.com/steer-sh/steer-cli/internal/status"
)

// viewHealth is the one read-only panel: corpora, the team lockfile, and the
// instrument the loop measures with.
func (m *Model) viewHealth() string {
	out := header("Health — evidence, lockfile, instrument (read-only)")

	out += section("Evidence on this machine")
	for _, c := range m.snap.Corpora {
		state := dim("absent")
		if c.Present {
			state = ok(fmt.Sprintf("%d rows", c.Size))
		}
		out += fmt.Sprintf("   %-20s %s\n", c.Corpus, state)
		if c.Path != "" {
			out += "     " + dim(c.Path) + "\n"
		}
	}

	if n := m.snap.Posture.Unresolved; n > 0 {
		out += "   " + warn(status.OutcomesWaitingPhrase(n)+
			" — results are withheld until they do (Arming: enter reads them)\n")
	}

	out += section("Guard master runtime")
	for _, master := range m.snap.Posture.Masters {
		phrase := status.MasterPhrase(master)
		if phrase == "" {
			continue
		}
		label := fmt.Sprintf("%s %s", master.Harness, master.Scope)
		if master.Stale() {
			out += fmt.Sprintf("   %-20s %s\n", label, warn(phrase))
			continue
		}
		out += fmt.Sprintf("   %-20s %s\n", label, ok(phrase))
	}

	out += section("Telemetry")
	out += fmt.Sprintf("   reviews %d · denials %d · guard blocks %d\n",
		m.snap.Telemetry.ReviewChains, m.snap.Telemetry.DenialsRows, m.snap.Telemetry.AuditFirings)

	out += section("Team lockfile")
	if m.snap.Team.Present {
		out += fmt.Sprintf("   %s · %d hooks locked · catalog %s\n",
			m.snap.Team.Path, m.snap.Team.HooksLocked, m.snap.Team.CatalogVersion)
	} else {
		out += dim("   no repo-committed lockfile at the project root.\n")
	}

	out += section("Instrument")
	out += fmt.Sprintf("   reviewer %s · instrument %s\n", m.snap.Posture.Reviewer, m.snap.Posture.Instrument)
	out += "   " + dim("review log "+m.snap.Posture.Ledger) + "\n"
	return out
}
