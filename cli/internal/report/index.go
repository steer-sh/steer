package report

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/steer-sh/steer-cli/internal/harness"
)

// rowTextLen bounds a row's text in the index and in a signal listing; the full
// text of every occurrence is in the item's details.
const rowTextLen = 120

// Row is one cluster in the index or in a signal listing. Its text is the first
// occurrence, clipped.
type Row struct {
	ID         string `json:"id"`
	Sessions   int    `json:"sessions"`
	Count      int    `json:"count"`
	SendGroups int    `json:"send_groups,omitempty"`
	Text       string `json:"text"`
}

// SignalSummary sizes one signal kind.
type SignalSummary struct {
	Signal   string `json:"signal"`
	Clusters int    `json:"clusters"`
	Sessions int    `json:"sessions"`
	// InIndex is true for the kinds whose rows the index lists. The others are
	// read with `steer report show <signal>`.
	InIndex bool `json:"in_index"`
}

// InstructionSummary is an instruction document without its directives, which
// its details hold.
type InstructionSummary struct {
	ID         string     `json:"id"`
	Harness    harness.ID `json:"harness"`
	Path       string     `json:"path"`
	Directives int        `json:"directives"`
	SteerRules []string   `json:"steer_rules"`
	GuardRules []string   `json:"guard_rules"`
}

// Page says which slice of a listing one output holds. NextCursor is absent on
// the last page.
type Page struct {
	Cursor     int `json:"cursor"`
	Shown      int `json:"shown"`
	Total      int `json:"total"`
	Remaining  int `json:"remaining"`
	NextCursor int `json:"next_cursor,omitempty"`
}

// Index is the first page of the index: coverage, one summary per signal and
// per instruction document, and the rows of every signal the index lists.
type Index struct {
	DigestVersion    int                  `json:"digest_version"`
	ReportID         string               `json:"report_id"`
	GeneratedAt      string               `json:"generated_at"`
	WindowDays       int                  `json:"window_days"`
	ProjectsScanned  int                  `json:"projects_scanned"`
	Stats            Stats                `json:"stats"`
	InstructionFiles []InstructionSummary `json:"instruction_files"`
	Signals          []SignalSummary      `json:"signals"`
	Rows             []Row                `json:"rows"`
	Page             Page                 `json:"page"`
}

// IndexContinuation is a later page of the index.
type IndexContinuation struct {
	DigestVersion int    `json:"digest_version"`
	ReportID      string `json:"report_id"`
	Rows          []Row  `json:"rows"`
	Page          Page   `json:"page"`
}

// Listing is one page of a signal's clusters, optionally filtered.
type Listing struct {
	DigestVersion int    `json:"digest_version"`
	ReportID      string `json:"report_id"`
	Signal        string `json:"signal"`
	Match         string `json:"match,omitempty"`
	Rows          []Row  `json:"rows"`
	Page          Page   `json:"page"`
}

// DetailItem heads one requested item: a cluster or an instruction document.
type DetailItem struct {
	ID         string     `json:"id"`
	Signal     string     `json:"signal,omitempty"`
	Pattern    string     `json:"pattern,omitempty"`
	Count      int        `json:"count,omitempty"`
	Sessions   int        `json:"sessions,omitempty"`
	SendGroups int        `json:"send_groups,omitempty"`
	FirstSeen  string     `json:"first_seen,omitempty"`
	LastSeen   string     `json:"last_seen,omitempty"`
	Harness    harness.ID `json:"harness,omitempty"`
	Path       string     `json:"path,omitempty"`
	SteerRules []string   `json:"steer_rules,omitempty"`
	GuardRules []string   `json:"guard_rules,omitempty"`
}

// DetailRow is one occurrence of a cluster, or one directive of a document.
type DetailRow struct {
	ID                           string           `json:"id"`
	Session                      string           `json:"session,omitempty"`
	Timestamp                    string           `json:"timestamp,omitempty"`
	Section                      string           `json:"section,omitempty"`
	Group                        int              `json:"group,omitempty"`
	AfterInterrupt               bool             `json:"after_interrupt,omitempty"`
	Text                         string           `json:"text"`
	PreviousHumanText            *ObservedText    `json:"previous_human_text,omitempty"`
	FirstObservedHumanTextInScan *ObservedText    `json:"first_observed_human_text_in_scan,omitempty"`
	RecentToolCalls              *RecentToolCalls `json:"recent_tool_calls,omitempty"`
}

// Detail is one page of the rows of the requested items.
type Detail struct {
	DigestVersion int          `json:"digest_version"`
	ReportID      string       `json:"report_id"`
	Items         []DetailItem `json:"items"`
	Rows          []DetailRow  `json:"rows"`
	Page          Page         `json:"page"`
}

// IndexPage renders the index from cursor within maxBytes. Cursor 0 is the first
// page, with the whole header.
func IndexPage(d *Digest, cursor, maxBytes int) ([]byte, error) {
	var rows []Row
	for _, list := range d.Signals.lists() {
		if list.inIndex {
			rows = append(rows, rowsOf(list.items)...)
		}
	}
	if cursor > 0 {
		return paged(rows, cursor, maxBytes, func(shown []Row, p Page) any {
			return IndexContinuation{DigestVersion: d.DigestVersion, ReportID: d.ReportID, Rows: shown, Page: p}
		})
	}
	head := Index{
		DigestVersion:    d.DigestVersion,
		ReportID:         d.ReportID,
		GeneratedAt:      d.GeneratedAt,
		WindowDays:       d.WindowDays,
		ProjectsScanned:  len(d.ProjectsScanned),
		Stats:            d.Stats,
		InstructionFiles: []InstructionSummary{},
	}
	for _, f := range d.InstructionFiles {
		head.InstructionFiles = append(head.InstructionFiles, InstructionSummary{
			ID: f.ID, Harness: f.Harness, Path: f.Path, Directives: len(f.Directives),
			SteerRules: f.SteerRules, GuardRules: f.GuardRules,
		})
	}
	for _, list := range d.Signals.lists() {
		head.Signals = append(head.Signals, SignalSummary{
			Signal:   list.kind,
			Clusters: len(list.items),
			Sessions: distinctSessions(list.items),
			InIndex:  list.inIndex,
		})
	}
	return paged(rows, cursor, maxBytes, func(shown []Row, p Page) any {
		page := head
		page.Rows, page.Page = shown, p
		return page
	})
}

// SignalPage renders one signal's clusters from cursor within maxBytes. A
// non-nil match keeps the clusters whose pattern or any occurrence matches it.
func SignalPage(d *Digest, signal string, match *regexp.Regexp, cursor, maxBytes int) ([]byte, error) {
	var items []*Cluster
	found := false
	for _, list := range d.Signals.lists() {
		if list.kind == signal {
			items, found = list.items, true
		}
	}
	if !found {
		return nil, fmt.Errorf("unknown signal %q: want one of %s", signal, strings.Join(SignalNames(), ", "))
	}
	if match != nil {
		var kept []*Cluster
		for _, c := range items {
			if clusterMatches(c, match) {
				kept = append(kept, c)
			}
		}
		items = kept
	}
	listing := Listing{DigestVersion: d.DigestVersion, ReportID: d.ReportID, Signal: signal}
	if match != nil {
		listing.Match = match.String()
	}
	return paged(rowsOf(items), cursor, maxBytes, func(shown []Row, p Page) any {
		page := listing
		page.Rows, page.Page = shown, p
		return page
	})
}

// DetailPage renders every occurrence of the requested clusters and every
// directive of the requested documents, from cursor within maxBytes.
func DetailPage(d *Digest, ids []string, cursor, maxBytes int) ([]byte, error) {
	clusters := map[string]*Cluster{}
	signals := map[string]string{}
	for _, list := range d.Signals.lists() {
		for _, c := range list.items {
			clusters[c.ID] = c
			signals[c.ID] = list.kind
		}
	}
	files := map[string]InstructionFile{}
	for _, f := range d.InstructionFiles {
		files[f.ID] = f
	}

	detail := Detail{DigestVersion: d.DigestVersion, ReportID: d.ReportID}
	var rows []DetailRow
	for _, id := range ids {
		if c, ok := clusters[id]; ok {
			detail.Items = append(detail.Items, DetailItem{
				ID: id, Signal: signals[id], Pattern: c.Pattern, Count: c.Count, Sessions: c.Sessions,
				SendGroups: c.SendGroups, FirstSeen: c.FirstSeen, LastSeen: c.LastSeen,
			})
			for _, o := range c.Occurrences {
				rows = append(rows, DetailRow{
					ID: id, Session: o.Session, Timestamp: o.Timestamp, Group: o.Group,
					AfterInterrupt: o.AfterInterrupt, Text: o.Text,
					PreviousHumanText: o.PreviousHumanText, FirstObservedHumanTextInScan: o.FirstObservedHumanTextInScan,
					RecentToolCalls: o.RecentToolCalls,
				})
			}
			continue
		}
		if f, ok := files[id]; ok {
			detail.Items = append(detail.Items, DetailItem{
				ID: id, Harness: f.Harness, Path: f.Path, SteerRules: f.SteerRules, GuardRules: f.GuardRules,
			})
			for _, dir := range f.Directives {
				rows = append(rows, DetailRow{ID: id, Section: dir.Section, Text: dir.Text})
			}
			continue
		}
		return nil, fmt.Errorf("report %s holds no item %q", d.ReportID, id)
	}
	return paged(rows, cursor, maxBytes, func(shown []DetailRow, p Page) any {
		page := detail
		page.Rows, page.Page = shown, p
		return page
	})
}

// SignalNames lists the signal kinds a listing accepts, in index order.
func SignalNames() []string {
	var out []string
	for _, list := range (Signals{}).lists() {
		out = append(out, list.kind)
	}
	return out
}

func rowsOf(clusters []*Cluster) []Row {
	rows := make([]Row, 0, len(clusters))
	for _, c := range clusters {
		text := c.Pattern
		if len(c.Occurrences) > 0 {
			text = c.Occurrences[0].Text
		}
		rows = append(rows, Row{ID: c.ID, Sessions: c.Sessions, Count: c.Count, SendGroups: c.SendGroups, Text: clip(text, rowTextLen)})
	}
	return rows
}

func clusterMatches(c *Cluster, re *regexp.Regexp) bool {
	if re.MatchString(c.Pattern) {
		return true
	}
	for _, o := range c.Occurrences {
		if re.MatchString(o.Text) {
			return true
		}
	}
	return false
}

// paged renders the rows from cursor, as many as fit in maxBytes. A page always
// holds at least one row when any remain, so a row larger than the bound still
// reaches the reader and the cursor always advances.
func paged[R any](rows []R, cursor, maxBytes int, frame func([]R, Page) any) ([]byte, error) {
	if cursor < 0 || cursor > len(rows) {
		return nil, fmt.Errorf("cursor %d is outside 0..%d", cursor, len(rows))
	}
	if maxBytes <= 0 {
		maxBytes = DefaultMaxDigestBytes
	}
	render := func(n int) ([]byte, error) {
		p := Page{Cursor: cursor, Shown: n, Total: len(rows), Remaining: len(rows) - cursor - n}
		if p.Remaining > 0 {
			p.NextCursor = cursor + n
		}
		shown := rows[cursor : cursor+n]
		if shown == nil {
			shown = []R{}
		}
		return json.MarshalIndent(frame(shown, p), "", "  ")
	}
	out, err := render(0)
	if err != nil {
		return nil, err
	}
	for n := 1; cursor+n <= len(rows); n++ {
		next, err := render(n)
		if err != nil {
			return nil, err
		}
		if len(next) > maxBytes && n > 1 {
			break
		}
		out = next
		if len(next) > maxBytes {
			break
		}
	}
	return out, nil
}
