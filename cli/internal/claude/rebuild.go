package claude

import (
	"fmt"
	"strings"

	"github.com/steer-sh/steer-cli/internal/guard"
	"github.com/steer-sh/steer-cli/internal/harness"
	"github.com/steer-sh/steer-cli/internal/hookstore"
)

// RebuildTarget reports one harness's registration across a rebuild. Previous is
// the master it invoked before, which differs from Path on a machine whose store
// has just moved out from under an old registration.
type RebuildTarget struct {
	Harness  harness.Harness
	Previous string
	Path     string
	Removed  bool
	// CommandChanged reports a registration whose command text this rebuild
	// rewrote. Previous and Path are decoded paths and read the same across a
	// change of quoting, while a harness that records an approval hashes the
	// text, so only this says whether that approval still covers the handler.
	CommandChanged bool
}

// RebuildResult describes what a rebuild wrote.
type RebuildResult struct {
	Scope      string
	StoreDir   string
	LegacyDir  string
	Migrated   bool
	MasterPath string
	Hooks      int
	Stamp      guard.Stamp
	Targets    []RebuildTarget
	// Verification is the fixture rerun that followed regeneration. The master
	// is new, so every guard's tested state was cleared and this re-earns it.
	Verification *FixtureVerification
}

// Moved reports whether any target now invokes a different file than it did.
func (r *RebuildResult) Moved() bool {
	for _, t := range r.Targets {
		if t.Previous != "" && t.Previous != t.Path {
			return true
		}
	}
	return false
}

// RebuildMaster regenerates the master hook and re-registers it, per target, as
// one operation. The two halves are one operation because regeneration alone can
// strand the registration: consolidation writes to the store hookstore.Resolve
// answers, a legacy store migrates the first time anything mutates it, and the
// legacy files are deliberately left behind for older binaries. A rebuild that
// only regenerated would leave the repaired master unused while the stale
// registered one kept running.
//
// A store holding no guards has no master, so each target is deregistered
// instead — the same answer RemoveManagedHook gives when the last guard goes.
func RebuildMaster(scope string, targets ...harness.Registrar) (*RebuildResult, error) {
	scope = strings.ToLower(strings.TrimSpace(scope))
	if err := ValidateScope(scope); err != nil {
		return nil, err
	}

	result := &RebuildResult{Scope: scope, Targets: []RebuildTarget{}}
	legacy, err := hookstore.LegacyDir(scope)
	if err != nil {
		return nil, err
	}
	before, err := hookstore.Resolve(scope)
	if err != nil {
		return nil, err
	}
	result.LegacyDir = legacy

	list := harness.Targets(targets, Registrar{})
	previous := make([]string, len(list))
	priorCommand := make([]string, len(list))
	for i, target := range list {
		path, err := target.RegisteredMasterPath(scope)
		if err != nil {
			return nil, err
		}
		previous[i] = path
		priorCommand[i] = registeredCommand(target, scope)
	}

	if err := ConsolidateHooks(scope); err != nil {
		return nil, err
	}

	hooks, err := ListIndividualHooks(scope)
	if err != nil {
		return nil, err
	}
	result.Hooks = len(hooks)

	store, err := hookstore.Resolve(scope)
	if err != nil {
		return nil, err
	}
	result.StoreDir = store
	result.Migrated = before == legacy && store != legacy

	master, err := GetMasterHookPath(scope)
	if err != nil {
		return nil, err
	}
	result.MasterPath = master
	if len(hooks) > 0 {
		stamp, ok := guard.ReadStamp(master)
		if !ok {
			return nil, fmt.Errorf("rebuilt master at %s carries no runtime stamp", master)
		}
		result.Stamp = stamp
	}

	rules, err := ManifestRules(scope)
	if err != nil {
		return nil, err
	}

	for i, target := range list {
		row := RebuildTarget{Harness: target.Harness(), Previous: previous[i]}
		if err := target.SetRules(rules, scope); err != nil {
			return nil, err
		}
		if len(hooks) == 0 {
			if err := target.RemoveMaster(scope); err != nil {
				return nil, err
			}
			row.Removed = true
			result.Targets = append(result.Targets, row)
			continue
		}
		if err := target.RegisterMaster(scope); err != nil {
			return nil, err
		}
		path, err := target.RegisteredMasterPath(scope)
		if err != nil {
			return nil, err
		}
		row.Path = path
		row.CommandChanged = priorCommand[i] != "" && priorCommand[i] != registeredCommand(target, scope)
		result.Targets = append(result.Targets, row)
	}

	verification, err := VerifyFixtures(scope)
	if err != nil {
		return result, err
	}
	result.Verification = verification

	return result, nil
}

// masterCommandReader is implemented by a harness whose configuration carries the
// master's command as text. Pi bakes the path into an extension and runs no
// shell, so it has no command to compare.
type masterCommandReader interface {
	RegisteredMasterCommand(scope string) (string, error)
}

// registeredCommand reads a target's registered command, or "" when the harness
// keeps none and when it cannot be read. A read that fails is not a rebuild
// failure: the command is reported alongside the registration, never instead of it.
func registeredCommand(target harness.Registrar, scope string) string {
	reader, ok := target.(masterCommandReader)
	if !ok {
		return ""
	}
	command, err := reader.RegisteredMasterCommand(scope)
	if err != nil {
		return ""
	}
	return command
}
