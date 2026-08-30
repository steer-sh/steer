package tui

import "fmt"

// viewHealth is the one read-only panel: corpora, the team lockfile, and the
// instrument the loop measures with.
func (m *Model) viewHealth() string {
	out := header("Health — corpora, lockfile, instrument (read-only)")

	out += section("Evidence corpora")
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

	out += section("Telemetry")
	out += fmt.Sprintf("   review chains %d · denials %d · audit firings %d\n",
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
	out += "   " + dim("ledger "+m.snap.Posture.Ledger) + "\n"
	return out
}
