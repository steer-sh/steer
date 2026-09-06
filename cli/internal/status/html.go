// HTML export: one self-contained file rendered from the same SystemStatus the
// terminal view reads. No server, no runtime dependency, no re-derivation from
// raw state files.

package status

import (
	"embed"
	"fmt"
	"html/template"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/steer-sh/steer-cli/internal/cadence"
)

//go:embed templates/dashboard.html.tmpl
var templateFS embed.FS

// verdictOrder ranks the verdict vocabulary for display: the verdicts that
// demand action first, then the abstentions, then the settled ones.
var verdictOrder = []string{
	"harmful", "ineffective", "class-grew",
	"legacy-unscoped", "evidence-incomplete", "confounded",
	"collecting", "inconclusive", "class-inconclusive", "no-baseline", "unscored",
	"class-steady", "class-faded", "effective",
}

// tierOrder is the intervention ladder, cheapest tier first.
var tierOrder = []string{"check", "guard", "instruction", "review"}

// stage is one node of the loop stepper.
type stage struct {
	Name  string
	Count int
	Note  string
	Done  bool
}

// card is one headline number in the summary grid.
type card struct {
	Label string
	Value string
	Meta  string
}

// group is a set of live interventions sharing a tier.
type group struct {
	Key   string
	Items []ScoredIntervention
}

// note is one row of an action group that is not an intervention: a count of
// unresolved work plus the command that clears it.
type note struct {
	Label string
	Text  string
	Fix   string
}

// flagged is a set of queued rows sharing one action, keyed by its heading.
type flagged struct {
	Key   string
	Items []cadence.Intervention
	Notes []note
}

// view is the precomputed render model. The template does layout only; every
// grouping, count and label is decided here.
type view struct {
	Status    *SystemStatus
	Generated string
	Stages    []stage
	Cards     []card
	Attention []flagged
	Ledger    []ScoredIntervention
	Tiers     []group
	Quiet     bool
	Readiness *Readiness
	Host      string
}

// Export carries what the page shows beyond the snapshot. Readiness is derived from
// THIS process at export time — the reviewer probe reads this process's PATH — so it
// stays out of the SystemStatus contract and rides here instead, next to the host
// that produced it. A caller that cannot answer passes the zero value.
type Export struct {
	// Readiness is the short answer, or nil to render no Answer section.
	Readiness *Readiness
	// Host names the machine that produced the answer.
	Host string
}

// RenderHTML writes the snapshot as one self-contained HTML page.
func (s *SystemStatus) RenderHTML(w io.Writer, x Export) error {
	rate := s.Rate
	tmpl, err := template.New("dashboard.html.tmpl").Funcs(template.FuncMap{
		"pct":   pct,
		"delta": delta,
		// The rate columns print only where the scorer made a claim; the counts
		// carry their denominator in words everywhere else.
		"pctIf":     pctFor,
		"deltaIf":   deltaFor,
		"claim":     EffectClaimPhrase,
		"recur":     RecurrencePhrase,
		"prose":     ProsePhrases,
		"tierWord":  TierPhrase,
		"label":     VerdictLabel,
		"gap":       EvidenceGap,
		"gapsay":    GapSentence,
		"removal":   RemovalPhrase,
		"unmeas":    func(verdict, unit string, required *int) bool { return Unmeasurable(verdict, unit, required, rate) },
		"wait":      func(required *int, unit string) string { return RatePhrase(required, unit, rate) },
		"stamp":     stamp,
		"slug":      slug,
		"arming":    armingWord,
		"present":   presentCount,
		"effect":    EffectPhrase,
		"scope":     ScopePhrase,
		"cover":     CoveragePhrase,
		"needs":     RequiredPhrase,
		"reasons":   ConfoundPhrases,
		"attrib":    AttributionPhrase,
		"trust":     TrustPhrase,
		"master":    MasterPhrase,
		"resolves":  ResolvePhrase,
		"observed":  ObservedPhrase,
		"mobserved": MasterObservedPhrase,
		"retire":    RetireCommand,
	}).ParseFS(templateFS, "templates/dashboard.html.tmpl")
	if err != nil {
		return fmt.Errorf("failed to parse the dashboard template: %w", err)
	}
	return tmpl.Execute(w, buildView(s, x))
}

// buildView precomputes every grouping and headline the page shows.
func buildView(s *SystemStatus, x Export) *view {
	active, refused := 0, 0
	measured := 0
	for _, it := range s.Interventions {
		switch it.Status {
		case "active":
			active++
		case "rejected":
			refused++
		}
		if scoredVerdict(it.Verdict) {
			measured++
		}
	}
	effective := s.Summary.Counts["effective"]

	v := &view{
		Status:    s,
		Generated: s.GeneratedAt,
		Ledger:    sortedLedger(s.Interventions),
		Attention: attentionGroups(s.Attention.Interventions, s.Rate, s.Posture.Unresolved),
		Tiers:     tierGroups(s.Interventions),
		// A row that owes an adjudication is unresolved work, and it is the reason a
		// window reads evidence-incomplete. A page calling the loop quiet beside a
		// chip counting those rows contradicts itself.
		Quiet: len(s.Attention.Interventions) == 0 && len(s.Attention.Candidates) == 0 &&
			len(s.Attention.Unscored) == 0 && s.Posture.Unresolved == 0,
		Readiness: x.Readiness,
		Host:      x.Host,
	}
	v.Stages = []stage{
		{"Observed", s.Telemetry.ReviewChains, "decided review sequences", s.Telemetry.ReviewChains > 0},
		{"Clustered", s.Summary.Total, "recurring problems on record", s.Summary.Total > 0},
		{"Fixed", active, "fixes installed", active > 0},
		{"Measured", measured, "problems with a result", measured > 0},
		{"Effective", effective, "fixes with fewer recurrences", effective > 0},
	}
	v.Cards = []card{
		{"Decided review sequences", itoa(s.Telemetry.ReviewChains), chainMeta(s.Chains)},
		{"Recurring problems", itoa(s.Summary.Total), verdictMeta(s.Summary.Counts)},
		{"Guard blocks", itoa(s.Telemetry.AuditFirings), "actions blocked, in the global audit log"},
		{"Declined suggestions", itoa(refused), "kept for their reopen condition"},
		{"Auto-mode denials", itoa(s.Telemetry.DenialsRows), "actions Claude Code auto mode blocked"},
	}
	return v
}

// scoredVerdict reports whether a verdict carries a measurement. An abstention, a
// withheld label and the session-unit placeholder do not, so the set is named
// positively: a label added later counts as unmeasured until it is listed.
func scoredVerdict(v string) bool {
	switch v {
	case "effective", "harmful", "ineffective", "class-grew", "class-faded", "class-steady":
		return true
	}
	return false
}

// sortedLedger orders the full inventory by verdict urgency, then by class.
func sortedLedger(items []ScoredIntervention) []ScoredIntervention {
	out := append([]ScoredIntervention{}, items...)
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := verdictRank(out[i].Verdict), verdictRank(out[j].Verdict)
		if ri != rj {
			return ri < rj
		}
		return out[i].Class < out[j].Class
	})
	return out
}

func verdictRank(v string) int {
	for i, name := range verdictOrder {
		if name == v {
			return i
		}
	}
	return len(verdictOrder)
}

// attentionGroups splits the cadence queue by the action each row asks of the
// reader, in ActionOrder, so the page and the terminal group and sort alike.
func attentionGroups(items []cadence.Intervention, rate ChainRate, unresolved int) []flagged {
	byAction := map[string][]cadence.Intervention{}
	for _, it := range items {
		act := ActionFor(it.Verdict, Unmeasurable(it.Verdict, it.Unit, it.RequiredAfter, rate))
		byAction[act] = append(byAction[act], it)
	}
	notes := map[string][]note{}
	if unresolved > 0 {
		notes[ActionAdjudicate] = append(notes[ActionAdjudicate], note{
			Label: "unresolved",
			Text:  fmt.Sprintf("%d review(s) still owe a decision from the coding agent", unresolved),
			Fix:   BackfillCommand,
		})
	}
	out := []flagged{}
	for _, act := range ActionOrder {
		if len(byAction[act]) == 0 && len(notes[act]) == 0 {
			continue
		}
		out = append(out, flagged{Key: ActionHeading[act], Items: byAction[act], Notes: notes[act]})
	}
	return out
}

// tierGroups buckets the live inventory by enforcement tier, cheapest first.
func tierGroups(items []ScoredIntervention) []group {
	byTier := map[string][]ScoredIntervention{}
	for _, it := range items {
		if it.Status != "active" {
			continue
		}
		tier := it.Tier
		if tier == "" {
			tier = "unrouted"
		}
		byTier[tier] = append(byTier[tier], it)
	}
	seen := map[string]bool{}
	out := []group{}
	add := func(tier string) {
		if seen[tier] || len(byTier[tier]) == 0 {
			return
		}
		seen[tier] = true
		out = append(out, group{Key: tier, Items: byTier[tier]})
	}
	for _, tier := range tierOrder {
		add(tier)
	}
	rest := make([]string, 0, len(byTier))
	for tier := range byTier {
		if !seen[tier] {
			rest = append(rest, tier)
		}
	}
	sort.Strings(rest)
	for _, tier := range rest {
		add(tier)
	}
	return out
}

// chainMeta describes the finding load the carried chains hold.
func chainMeta(chains []ChainSummary) string {
	if len(chains) == 0 {
		return "no chains on this machine"
	}
	findings, accepted := 0, 0
	for _, c := range chains {
		findings += c.Findings
		accepted += c.Accepted
	}
	return fmt.Sprintf("%d recent chains carry %d findings, %d accepted", len(chains), findings, accepted)
}

// verdictMeta lists the verdict breakdown in display order.
func verdictMeta(counts map[string]int) string {
	if len(counts) == 0 {
		return "no fixes recorded on this machine"
	}
	parts := []string{}
	for _, v := range verdictOrder {
		if counts[v] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[v], v))
		}
	}
	rest := make([]string, 0, len(counts))
	for v, n := range counts {
		if verdictRank(v) == len(verdictOrder) && n > 0 {
			rest = append(rest, fmt.Sprintf("%d %s", n, v))
		}
	}
	sort.Strings(rest)
	parts = append(parts, rest...)
	return strings.Join(parts, " · ")
}

// delta renders the signed rate movement the ledger's Δ column shows, plus the
// CSS class that colors a fall green and a rise red.
func delta(before, after float64) template.HTML {
	d := (after - before) * 100
	class, sign := "dimtext", ""
	switch {
	case d < -0.05:
		class = "d-down"
	case d > 0.05:
		class, sign = "d-up", "+"
	}
	return template.HTML(fmt.Sprintf("<span class=%q>%s%.1f%%</span>", class, sign, d))
}

// pct renders an incidence rate as a percentage.
func pct(rate float64) string { return fmt.Sprintf("%.1f%%", rate*100) }

// pctFor prints a window's rate only under a verdict that states an effect.
func pctFor(verdict string, in cadence.Incidence) string {
	if verdict != "effective" && verdict != "harmful" {
		return ""
	}
	return pct(in.Rate)
}

// deltaFor prints the between-window change under the same rule.
func deltaFor(verdict string, before, after cadence.Incidence) template.HTML {
	if verdict != "effective" && verdict != "harmful" {
		return template.HTML(`<span class="dimtext">—</span>`)
	}
	return delta(before.Rate, after.Rate)
}

// stamp renders an epoch second as a readable local timestamp.
func stamp(ts float64) string {
	if ts == 0 {
		return "—"
	}
	return time.Unix(int64(ts), 0).Format("2006-01-02 15:04")
}

// slug turns a verdict or tier into a CSS class suffix.
func slug(s string) string {
	if s == "" {
		return "none"
	}
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' {
			return r
		}
		if r >= 'A' && r <= 'Z' {
			return r + 32
		}
		return '-'
	}, s)
}

// armingWord names a scope's arming state in one word. A registration whose
// command does not resolve is broken, not armed: the harness runs the tool anyway
// and says nothing.
func armingWord(a ScopeArming) string {
	switch {
	case a.Registered && a.Resolvable == ResolveMissing:
		return "broken"
	case a.Effective:
		return "armed"
	case a.Registered:
		return "inert"
	default:
		return "off"
	}
}

// presentCount counts the corpora present on this machine.
func presentCount(rows []CorpusRow) int {
	n := 0
	for _, r := range rows {
		if r.Present {
			n++
		}
	}
	return n
}

func itoa(n int) string { return fmt.Sprintf("%d", n) }
