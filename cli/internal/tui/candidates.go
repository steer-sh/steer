package tui

import (
	"fmt"
)

// viewCandidates renders the routing queue. The panel is read-only: clustering the
// evidence and drafting a fix is what /steer-report does, with the findings in
// context. A form here asked the reader to author a regex and its prose from memory,
// with the evidence hidden behind the form it was authored in.
func (m *Model) viewCandidates() string {
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
				dim(fmt.Sprintf("%d review(s) carry a finding no problem covers yet · %s", c.UndecidedChains, c.Dump)))
		}
	}
	return out + "\n" + dim("run /steer-report to read these and decide each one.")
}
