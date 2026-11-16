package cmd

import (
	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/config"
	"github.com/steer-sh/steer-cli/internal/guardscope"
	"github.com/steer-sh/steer-cli/internal/harness"
	"github.com/steer-sh/steer-cli/internal/protection"
	"github.com/steer-sh/steer-cli/internal/status"
)

// protectionTargets are the harnesses Steer is set up in, which are the only ones
// self-protection registers into: Steer never sets up a harness on its own.
func protectionTargets() ([]harness.Registrar, error) {
	candidates, err := guardscope.HookRegistrars(true)
	if err != nil {
		return nil, err
	}
	return status.SetUpTargets(candidates), nil
}

// globalProtectionRuns reports whether the global self-protection guard runs in
// every harness Steer is set up in; a read that fails reads as not running.
func globalProtectionRuns() bool {
	return status.ReadProtection().State == status.ProtectionOn
}

// globallyCovered reports whether every harness in targets is one the global
// guard runs in, so a project copy's removal leaves none of them unprotected.
func globallyCovered(targets []harness.Registrar) bool {
	setUp, err := protectionTargets()
	if err != nil {
		return false
	}
	for _, target := range targets {
		covered := false
		for _, s := range setUp {
			covered = covered || s.Harness().ID == target.Harness().ID
		}
		if !covered {
			return false
		}
	}
	return true
}

// reconcileProtection brings the global self-protection guard in line with its
// setting and reports the change through r. A failure is a step left to the
// operator, never a failed install: the rest of the install still stands. With
// verifyLater set, the fixture rerun after a change runs off this process.
func reconcileProtection(r *installReport, verifyLater func(scope string)) {
	cfg, err := config.Load()
	if err != nil {
		r.action("Steer could not read its settings, so self-protection was not checked: " + err.Error())
		return
	}
	targets, err := protectionTargets()
	if err != nil {
		r.action("Self-protection was not checked: " + err.Error())
		return
	}
	change, err := protection.Reconcile(protection.Enabled(cfg), targets,
		protection.Options{Unverified: verifyLater != nil})
	if err != nil {
		r.action("Self-protection was not installed: " + err.Error())
		return
	}
	if change != protection.Unchanged && verifyLater != nil {
		verifyLater(claude.GlobalScope)
	}
	reportProtectionChange(r, change, targets)
	if legacy, err := protection.LegacyIn(claude.ProjectScope); err == nil && legacy && globalProtectionRuns() {
		r.action("This repository carries the self-protection guard an earlier Steer installed; " +
			"the global guard now protects it. Run: steer hooks rebuild --scope project")
	}
}

// reportProtectionChange names what a reconcile did, and the approval Codex needs
// before it runs a guard runner registered for the first time.
func reportProtectionChange(r *installReport, change protection.Change, targets []harness.Registrar) {
	switch change {
	case protection.Installed:
		r.success("Installed Steer's self-protection for " + harnessNames(targets) +
			"; it blocks an agent from changing Steer's own hooks, settings and registrations. " +
			protection.OffCommand + " turns it off.")
	case protection.Updated:
		r.success("Updated Steer's self-protection guard.")
	case protection.Removed:
		r.success("Removed Steer's self-protection guard, as its setting is off.")
		return
	default:
		return
	}
	if line := codexApprovalLine(claude.GlobalScope, targets); line != "" {
		r.action(line)
	}
}
