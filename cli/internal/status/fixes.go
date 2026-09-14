// The instruction and review tiers' share of the snapshot: the rules each store
// holds, and the review addendum entries the commit gate reads beside the text on
// disk. A project entry has two readings, the working tree and HEAD, and the gate
// reads only the second.

package status

import (
	"sort"
	"strings"

	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/gate"
)

// Addendum entry states: what the commit gate reads for one entry, against the
// store on disk that `steer commit-gate addendum add` and `remove` write.
const (
	// AddendumLive is an entry the gate reads with the text on disk.
	AddendumLive = "live"
	// AddendumUncommitted is a project entry on disk that HEAD does not hold, so
	// the gate does not read it yet.
	AddendumUncommitted = "uncommitted"
	// AddendumUncommittedChange is a project entry whose text on disk differs from
	// HEAD; the gate reads the HEAD text.
	AddendumUncommittedChange = "uncommitted-change"
	// AddendumUncommittedRemoval is a project entry at HEAD that the store on disk
	// no longer holds; the gate still reads it.
	AddendumUncommittedRemoval = "uncommitted-removal"
	// AddendumUnreadable is an entry whose store, on disk or at HEAD, could not be
	// read.
	AddendumUnreadable = "unreadable"
)

// RuleInfo is one instruction-tier rule in a store.
type RuleInfo struct {
	ID    string `json:"id"`
	Scope string `json:"scope"`
	Text  string `json:"text"`
}

// AddendumInfo is one review addendum entry. Text is the store on disk; HeadText
// is the committed text the gate reads for a project entry, empty when HEAD holds
// none.
type AddendumInfo struct {
	ID       string `json:"id"`
	Scope    string `json:"scope"`
	Text     string `json:"text,omitempty"`
	HeadText string `json:"head_text,omitempty"`
	State    string `json:"state"`
	Detail   string `json:"detail,omitempty"`
}

// collectRules reads the rules in both stores. An unreadable store yields no rows.
func collectRules() []RuleInfo {
	out := []RuleInfo{}
	for _, scope := range []string{claude.ProjectScope, claude.GlobalScope} {
		manifest, err := claude.LoadManifest(scope)
		if err != nil || manifest == nil {
			continue
		}
		for id, r := range manifest.Rules {
			out = append(out, RuleInfo{ID: id, Scope: scope, Text: r.Text})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Scope != out[j].Scope {
			return out[i].Scope == claude.ProjectScope
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// AddendumInventory joins the addendum entries on disk with what the commit gate
// reads, head. A nil head was not read, so a project entry's state is unreadable.
func AddendumInventory(head *gate.Addendum) []AddendumInfo {
	out := []AddendumInfo{}
	for _, scope := range []string{claude.ProjectScope, claude.GlobalScope} {
		out = append(out, addendumRows(scope, head)...)
	}
	return out
}

// addendumRows is one store's entries, each read against the gate's copy.
func addendumRows(scope string, head *gate.Addendum) []AddendumInfo {
	disk, diskErr := claude.ListAddendum(scope)
	gateErr, gateRead := addendumStoreError(scope, head)
	committed := map[string]string{}
	if gateRead && gateErr == "" {
		for _, e := range head.Entries {
			if e.Scope == scope {
				committed[e.ID] = e.Text
			}
		}
	}
	rows := map[string]*AddendumInfo{}
	for _, e := range disk {
		rows[e.ID] = &AddendumInfo{ID: e.ID, Scope: scope, Text: normalizeEntry(e.Text)}
	}
	if scope == claude.ProjectScope {
		for id, text := range committed {
			row, ok := rows[id]
			if !ok {
				row = &AddendumInfo{ID: id, Scope: scope}
				rows[id] = row
			}
			row.HeadText = text
		}
	}
	out := make([]AddendumInfo, 0, len(rows)+1)
	for _, row := range rows {
		row.State, row.Detail = addendumState(*row, scope, gateRead, gateErr)
		if diskErr != nil {
			row.State, row.Detail = AddendumUnreadable, diskErr.Error()
		}
		out = append(out, *row)
	}
	if diskErr != nil && len(out) == 0 {
		out = append(out, AddendumInfo{Scope: scope, State: AddendumUnreadable, Detail: diskErr.Error()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// addendumState is what the gate reads for one entry. A global entry is read from
// disk, so it is live whenever the store on disk holds it.
func addendumState(row AddendumInfo, scope string, gateRead bool, gateErr string) (string, string) {
	switch {
	case scope != claude.ProjectScope:
		return AddendumLive, ""
	case !gateRead:
		return AddendumUnreadable, "the commit gate's copy at HEAD was not read"
	case gateErr != "":
		return AddendumUnreadable, gateErr
	case row.HeadText == "":
		return AddendumUncommitted, ""
	case row.Text == "":
		return AddendumUncommittedRemoval, ""
	case row.Text != row.HeadText:
		return AddendumUncommittedChange, ""
	}
	return AddendumLive, ""
}

// addendumStoreError is the gate's read failure for one store, and whether the
// gate's addendum was read at all.
func addendumStoreError(scope string, head *gate.Addendum) (string, bool) {
	if head == nil {
		return "", false
	}
	for _, e := range head.Errors {
		if strings.Contains(e, scope+" store") {
			return e, true
		}
	}
	return "", true
}

// AddendumPhrase says what the commit gate reads for one entry, in the reader's
// words.
func AddendumPhrase(a AddendumInfo) string {
	switch a.State {
	case AddendumLive:
		if a.Scope == claude.ProjectScope {
			return "committed; the commit gate reads it"
		}
		return "the commit gate reads it"
	case AddendumUncommitted:
		return "not committed; the commit gate does not read it until the manifest is committed"
	case AddendumUncommittedChange:
		return "changed but not committed; the commit gate reads the committed text: " + a.HeadText
	case AddendumUncommittedRemoval:
		return "removed but not committed; the commit gate still reads it until the removal is committed"
	case AddendumUnreadable:
		return "what the commit gate reads is not known: " + a.Detail
	}
	return "in the state " + a.State
}

// normalizeEntry folds whitespace the way the gate does before it compares or
// renders an entry.
func normalizeEntry(text string) string {
	return strings.Join(strings.Fields(text), " ")
}
