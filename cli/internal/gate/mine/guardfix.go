package mine

import "github.com/steer-sh/steer-cli/internal/steerpath"

// The record fields a guard-tier record binds its fix through. A record carrying
// all of them names one guard in one store on one machine; the adapters that read
// a store live outside this package, so a writer freezes those facts at
// registration and this is where a reader tests them.
const (
	HookIDKey    = "hook_id"
	HookScopeKey = "hook_scope"
	HookRepoKey  = "hook_repo"
	HookHostKey  = "hook_host"
)

// GuardFix is a guard-tier record's binding: the guard, the store that answered at
// registration, and, for a project store, the checkout it was rooted at and the
// machine that read it. Scope alone cannot name a project store, because a project
// store resolves from the working directory and every checkout has its own.
type GuardFix struct {
	ID    string
	Scope string
	Repo  string
	Host  string
}

// GuardFixOf reads a record's guard binding. bound is false for a record that names
// no hook; ownership is never inferred from a unique name in today's stores,
// because the store that holds a name here is not evidence it held it there.
func GuardFixOf(record Record) (GuardFix, bool) {
	fix := GuardFix{
		ID:    str(record[HookIDKey]),
		Scope: str(record[HookScopeKey]),
		Repo:  str(record[HookRepoKey]),
		Host:  str(record[HookHostKey]),
	}
	return fix, fix.ID != ""
}

// Fixture receipt states. They answer one question — what does this machine know
// about the stored cases behind this guard — and none of them claims the guard
// blocks correctly in the field.
const (
	// FixtureCurrent is a retained pass whose four components all still match.
	FixtureCurrent = "current"
	// FixtureStale is a retained pass that no longer matches, or whose components
	// could not all be read. A stale guard keeps running.
	FixtureStale = "stale"
	// FixtureNone is a resolved guard with no retained pass. It makes no
	// historical claim: a verification that failed and one that consolidation
	// invalidated leave the same manifest state.
	FixtureNone = "none"
	// FixtureUnavailable is a guard this reading cannot attribute or reach, so
	// neither a pass nor its absence is established.
	FixtureUnavailable = "unavailable"
)

// FixtureKey names one guard in one store, as this machine resolves it.
type FixtureKey struct {
	Scope  string
	HookID string
}

// FixtureReceipt is the guard tier's reading of its stored cases, in the check
// tier's vocabulary. Cases, VerifiedAt and Shadowed describe a retained pass and
// are absent without one; a zero Shadowed is a counted zero, not a missing count.
type FixtureReceipt struct {
	// Applicable is false for a tier that stores no fixtures, and for a record
	// naming no guard.
	Applicable bool     `json:"applicable"`
	State      string   `json:"state,omitempty"`
	Cases      *int     `json:"cases,omitempty"`
	Changed    []string `json:"changed,omitempty"`
	VerifiedAt string   `json:"verified_at,omitempty"`
	Shadowed   *int     `json:"shadowed,omitempty"`
	Reason     string   `json:"reason,omitempty"`
}

func unavailableReceipt(reason string) FixtureReceipt {
	return FixtureReceipt{Applicable: true, State: FixtureUnavailable, Reason: reason}
}

// fixtureReceiptOf reads one record's receipt out of the gathered map.
//
// The map is keyed by the store this machine resolved, so the ownership test is
// per record and happens here. A mismatch is unavailable, never stale: another
// store is no evidence that this record's guard changed.
func fixtureReceiptOf(record Record, ev EvidenceSet) FixtureReceipt {
	fix, bound := GuardFixOf(record)
	if !firingsApplicable(str(record["tier"])) || !bound {
		return FixtureReceipt{}
	}
	switch {
	case fix.Scope == "":
		return unavailableReceipt("the record names no store")
	case fix.Host != "" && ev.FixtureHost != "" && fix.Host != ev.FixtureHost:
		return unavailableReceipt("registered on host " + fix.Host +
			"; this command reads the stores on host " + ev.FixtureHost)
	}
	if fix.Scope == steerpath.ProjectScope {
		switch {
		case fix.Repo == "":
			return unavailableReceipt("the record does not establish its owning checkout")
		case ev.FixtureProjectRoot == "":
			return unavailableReceipt("registered against the project store in " + fix.Repo +
				"; this command resolves no project store")
		case fix.Repo != ev.FixtureProjectRoot:
			return unavailableReceipt("registered against the project store in " + fix.Repo +
				"; this command resolves " + ev.FixtureProjectRoot)
		}
	}
	receipt, ok := ev.FixtureReceipts[FixtureKey{Scope: fix.Scope, HookID: fix.ID}]
	if !ok {
		return unavailableReceipt("no " + fix.Scope + " guard store on this machine holds " + fix.ID)
	}
	return receipt
}
