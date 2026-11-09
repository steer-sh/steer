package status

import (
	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/codex"
	"github.com/steer-sh/steer-cli/internal/config"
	"github.com/steer-sh/steer-cli/internal/guardscope"
	"github.com/steer-sh/steer-cli/internal/harness"
	"github.com/steer-sh/steer-cli/internal/protection"
)

// Self-protection states, as ProtectionReading.State spells them.
const (
	// ProtectionOn: the setting is on, a harness holds a global Steer registration,
	// the global store holds the current guard, and every such harness runs it.
	ProtectionOn = "on"
	// ProtectionOff: the setting turns it off.
	ProtectionOff = "off"
	// ProtectionMissing: Steer is set up in a harness, the setting is on, and the
	// global store holds no guard of Steer's under the id.
	ProtectionMissing = "missing"
	// ProtectionOutdated: the store holds an earlier revision of the guard.
	ProtectionOutdated = "outdated"
	// ProtectionNotRunning: the store holds the current guard, and a harness Steer
	// is set up in does not run it: no global master registration, a master that
	// does not carry the guard, or a Codex runner awaiting approval.
	ProtectionNotRunning = "not-running"
	// ProtectionNotSetUp: no harness holds a global Steer registration, so no guard
	// is expected and a guard the store holds runs nowhere.
	ProtectionNotSetUp = "not-set-up"
	// ProtectionUnknown: the setting or the store could not be read.
	ProtectionUnknown = "unknown"
)

// ProtectionRepair is the command that installs or updates the guard.
const ProtectionRepair = "steer bin install"

// ProtectionReading is whether Steer's self-protection guard stands where the
// setting says it should.
type ProtectionReading struct {
	State string `json:"state"`
	// Setting is "on" or "off", the value in force.
	Setting string `json:"setting"`
	// Store is protection.StoreState of the global store, or "unknown".
	Store string `json:"store"`
	// Legacy lists the scopes still holding the guard an earlier Steer installed
	// from its preset catalog.
	Legacy []string `json:"legacy"`
	// Reason says why a not-running guard does not run.
	Reason string `json:"reason,omitempty"`
	Error  string `json:"error,omitempty"`
}

// ReadProtection reads the setting, the global store and the harnesses Steer is
// set up in.
func ReadProtection() ProtectionReading {
	r := ProtectionReading{Setting: protection.SettingOn, Store: ProtectionUnknown, Legacy: []string{}}
	cfg, err := config.Load()
	if err != nil {
		r.State, r.Error = ProtectionUnknown, err.Error()
		return r
	}
	enabled := protection.Enabled(cfg)
	if !enabled {
		r.Setting = protection.SettingOff
	}
	for _, scope := range []string{claude.GlobalScope, claude.ProjectScope} {
		if legacy, err := protection.LegacyIn(scope); err == nil && legacy {
			r.Legacy = append(r.Legacy, scope)
		}
	}
	store, err := protection.StoreState(claude.GlobalScope)
	if err != nil {
		r.State, r.Error = ProtectionUnknown, err.Error()
		return r
	}
	r.Store = store
	candidates, err := guardscope.HookRegistrars(true)
	if err != nil {
		r.State, r.Error = ProtectionUnknown, err.Error()
		return r
	}
	targets := SetUpTargets(candidates)
	if store == protection.StoreCurrent {
		reason, err := notRunningReason(targets)
		if err != nil {
			r.State, r.Error = ProtectionUnknown, err.Error()
			return r
		}
		r.Reason = reason
	}
	r.State = protectionState(enabled, len(targets) > 0, r.Reason == "", store)
	if r.State != ProtectionNotRunning {
		r.Reason = ""
	}
	return r
}

// The reasons a current guard does not run.
const (
	reasonUnregistered = " does not register the global guard runner"
	reasonUncovered    = "the global guard runner does not carry the current guard"
	reasonUnapproved   = "Codex has not approved the global guard runner"
	reasonUnreadable   = "Steer could not read whether Codex approved the global guard runner"
)

// notRunningReason names why the global master does not run the current guard
// in one of targets, or "" when every target runs it.
func notRunningReason(targets []harness.Registrar) (string, error) {
	for _, target := range targets {
		registered, err := target.MasterRegistered(claude.GlobalScope)
		if err != nil {
			return "", err
		}
		if !registered {
			return target.Harness().DisplayName + reasonUnregistered, nil
		}
	}
	if len(targets) > 0 && claude.MasterCovers(claude.GlobalScope) != nil {
		return reasonUncovered, nil
	}
	for _, target := range targets {
		if target.Harness().ID != harness.Codex {
			continue
		}
		a := codex.MasterArming(claude.GlobalScope, ".")
		switch {
		case a.Registered && !a.TrustStateKnown:
			return reasonUnreadable, nil
		case a.Registered && !a.TrustRecordPresent:
			return reasonUnapproved, nil
		}
	}
	return "", nil
}

func protectionState(enabled, setUp, running bool, store string) string {
	switch {
	case !enabled:
		return ProtectionOff
	case !setUp:
		// A guard no harness runs protects nothing, as after steer uninstall.
		return ProtectionNotSetUp
	case store == protection.StoreCurrent && !running:
		return ProtectionNotRunning
	case store == protection.StoreCurrent:
		return ProtectionOn
	case store == protection.StoreOutdated:
		return ProtectionOutdated
	}
	return ProtectionMissing
}

// protectionIssues reports a guard the setting expects and the store lacks, the
// setting turning it off, and an earlier Steer's copy a scope still holds.
func protectionIssues(p ProtectionReading) []Issue {
	var out []Issue
	switch p.State {
	case ProtectionMissing:
		detail := []string{}
		if p.Store == protection.StoreForeign {
			detail = append(detail, "A guard Steer did not install holds the id "+protection.HookName+".")
		}
		out = append(out, Issue{Severity: SeverityAttention, Title: "Self-protection is on but not installed",
			Detail: detail, Fix: ProtectionRepair})
	case ProtectionOutdated:
		out = append(out, Issue{Severity: SeverityAttention, Title: "Self-protection is out of date", Fix: ProtectionRepair})
	case ProtectionNotRunning:
		fix := ProtectionRepair
		switch p.Reason {
		case reasonUnapproved:
			fix = "Open /hooks in Codex and approve Steer's hooks."
		case reasonUnreadable:
			fix = "Inspect /hooks in Codex."
		}
		out = append(out, Issue{Severity: SeverityAttention, Title: "Self-protection is installed but an agent does not run it",
			Detail: []string{p.Reason}, Fix: fix})
	case ProtectionUnknown:
		out = append(out, Issue{Severity: SeverityUnchecked, Title: "Steer could not check self-protection",
			Detail: []string{p.Error}})
	case ProtectionOff:
		out = append(out, Issue{Severity: SeverityInfo, Title: "Self-protection is off",
			Detail: []string{"An agent can change Steer's own hooks, settings and registrations."},
			Fix:    "steer config set " + protection.SettingKey + " on"})
	}
	for _, scope := range p.Legacy {
		// The rebuild retires a repository's copy only while the global guard runs.
		if scope != claude.ProjectScope || p.State != ProtectionOn {
			continue
		}
		out = append(out, Issue{Severity: SeverityInfo,
			Title:  "This repository carries the self-protection guard an earlier Steer installed",
			Detail: []string{"The global guard now protects every repository; this copy runs the old rules."},
			Fix:    "steer hooks rebuild --scope project"})
	}
	return out
}
