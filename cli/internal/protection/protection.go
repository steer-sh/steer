// Package protection owns Steer's self-protection: a guard Steer installs in the
// global store, whenever the setting allows it, that blocks an agent from editing
// Steer's own state, settings and hook registrations to get past a block.
//
// It is best-effort and lexical. It stops a careless agent; an agent that hides a
// write behind bash -c, an interpreter, eval or a variable gets past it.
package protection

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/config"
	"github.com/steer-sh/steer-cli/internal/harness"
)

//go:embed guard.py
var guardSource []byte

const (
	// HookName is the guard's id in the global store. An earlier Steer installed
	// the same id from its preset catalog, in either scope.
	HookName = "steer-self-protection"
	// SettingKey is the steer config key that turns the guard off.
	SettingKey = "protection.self"
	// Rule is the line the guard adds to each harness's Steer Rules section.
	Rule = "never change Steer's own hooks, settings or harness registrations, or turn its protection off | tell the user what was blocked and let them change Steer"
	// HookVersion labels the guard's revision in the manifest.
	HookVersion = "self-protection-v1"
	// OffCommand is the command the user runs to turn the guard off.
	OffCommand = "steer config set " + SettingKey + " off"
)

// Setting values. An empty value is the default, which is on.
const (
	SettingOn  = "on"
	SettingOff = "off"
)

// Enabled reports whether the settings file leaves self-protection on.
func Enabled(cfg config.Config) bool {
	return strings.TrimSpace(cfg.Protection.Self) != SettingOff
}

// Desired is the guard this binary installs.
func Desired() claude.DesiredHook {
	return claude.DesiredHook{
		HookName: HookName,
		FileName: HookName + ".py",
		Rule:     Rule,
		Content:  guardSource,
		Metadata: claude.HookInstallMetadata{
			Source:        claude.SourceSteer,
			SourceID:      HookName,
			PromptVersion: "none",
			HookVersion:   HookVersion,
		},
	}
}

// Installed states of the global store's self-protection guard.
const (
	// StoreAbsent: the store holds no guard of that id.
	StoreAbsent = "absent"
	// StoreCurrent: the store holds the guard this binary installs.
	StoreCurrent = "current"
	// StoreOutdated: the store holds Steer's guard under that id with other bytes
	// or metadata, or the one an earlier Steer installed from its catalog.
	StoreOutdated = "outdated"
	// StoreForeign: the id belongs to a guard Steer did not install.
	StoreForeign = "foreign"
)

// StoreState reads the scope's self-protection guard against Desired.
func StoreState(scope string) (string, error) {
	manifest, err := claude.LoadManifest(scope)
	if err != nil {
		return "", err
	}
	entry, ok := manifest.Hooks[HookName]
	if !ok {
		return StoreAbsent, nil
	}
	source := strings.ToLower(strings.TrimSpace(entry.Source))
	if Legacy(entry) {
		return StoreOutdated, nil
	}
	if source != claude.SourceSteer {
		return StoreForeign, nil
	}
	desired := Desired()
	if strings.TrimSpace(entry.HookVersion) != HookVersion || strings.TrimSpace(entry.RuleHash) != claude.HashRule(Rule) {
		return StoreOutdated, nil
	}
	if file := strings.TrimSpace(entry.File); file != "" && file != desired.FileName {
		return StoreOutdated, nil
	}
	dir, err := claude.GetHooksDir(scope)
	if err != nil {
		return "", err
	}
	actual, err := os.ReadFile(filepath.Join(dir, desired.FileName))
	if os.IsNotExist(err) {
		return StoreOutdated, nil
	}
	if err != nil {
		return "", err
	}
	want, err := claude.DesiredFileContent(desired)
	if err != nil {
		return "", err
	}
	if claude.ContentSHA256(actual) != claude.ContentSHA256(want) {
		return StoreOutdated, nil
	}
	return StoreCurrent, nil
}

// Legacy reports whether entry is the self-protection guard an earlier Steer
// installed from its preset catalog.
func Legacy(entry claude.ManifestEntry) bool {
	return strings.EqualFold(strings.TrimSpace(entry.Source), claude.SourceFactory) &&
		strings.TrimSpace(entry.SourceID) == HookName
}

// LegacyIn reports whether the scope's store holds that earlier guard.
func LegacyIn(scope string) (bool, error) {
	manifest, err := claude.LoadManifest(scope)
	if err != nil {
		return false, err
	}
	entry, ok := manifest.Hooks[HookName]
	return ok && Legacy(entry), nil
}

// Change is what Reconcile did to the global store.
type Change string

const (
	Unchanged Change = ""
	Installed Change = "installed"
	Updated   Change = "updated"
	Removed   Change = "removed"
)

// Options controls one Reconcile.
type Options struct {
	// Unverified skips the fixture rerun that follows consolidation.
	Unverified bool
}

// Reconcile brings the global store in line with the setting: on, it installs or
// updates the guard and registers the master in targets; off, it removes it. It
// writes nothing while targets is empty, because those are the harnesses Steer is
// set up in and a guard registered nowhere protects nothing. A guard Steer did not
// install under the id is never replaced.
func Reconcile(enabled bool, targets []harness.Registrar, opts Options) (Change, error) {
	if len(targets) == 0 {
		return Unchanged, nil
	}
	scope := claude.GlobalScope
	state, err := StoreState(scope)
	if err != nil {
		return Unchanged, err
	}
	if !enabled {
		if state == StoreAbsent || state == StoreForeign {
			return Unchanged, nil
		}
		legacy, err := LegacyIn(scope)
		if err != nil {
			return Unchanged, err
		}
		removeOpts := claude.RemoveOptions{ShippedCopy: !legacy, Unverified: opts.Unverified,
			Reason: "self-protection was turned off"}
		if _, err := claude.RemoveManagedHook(scope, HookName, removeOpts, targets...); err != nil {
			return Unchanged, err
		}
		return Removed, nil
	}
	switch state {
	case StoreForeign:
		return Unchanged, fmt.Errorf("the global guard %s was not installed by Steer, so self-protection cannot be installed under its id; remove it with: steer remove %s --scope global", HookName, HookName)
	case StoreCurrent:
		drifted, _, err := claude.TargetDrift(scope, Rule, targets)
		if err != nil {
			return Unchanged, err
		}
		// A master that does not carry the guard runs none of it; the install
		// consolidates it again.
		if !drifted && claude.MasterCovers(scope) == nil {
			return Unchanged, nil
		}
	}
	desired := Desired()
	if _, err := claude.InstallManagedHook(scope, claude.InstallManagedHookInput{
		HookName:   desired.HookName,
		FileName:   desired.FileName,
		Rule:       desired.Rule,
		Content:    desired.Content,
		Metadata:   desired.Metadata,
		Unverified: opts.Unverified,
	}, targets...); err != nil {
		return Unchanged, err
	}
	if state == StoreAbsent {
		return Installed, nil
	}
	return Updated, nil
}

// RetireLegacy archives and removes the self-protection guard an earlier Steer
// installed in scope from its catalog, which the caller does only while the global
// guard runs in its place. It reports whether it removed one.
func RetireLegacy(scope string, targets ...harness.Registrar) (bool, error) {
	legacy, err := LegacyIn(scope)
	if err != nil || !legacy {
		return false, err
	}
	opts := claude.RemoveOptions{Reason: "self-protection now runs from the global store"}
	if _, err := claude.RemoveManagedHook(scope, HookName, opts, targets...); err != nil {
		return false, err
	}
	return true, nil
}
