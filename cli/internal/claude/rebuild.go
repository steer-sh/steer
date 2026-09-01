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
	for i, target := range list {
		path, err := target.RegisteredMasterPath(scope)
		if err != nil {
			return nil, err
		}
		previous[i] = path
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

	for i, target := range list {
		row := RebuildTarget{Harness: target.Harness(), Previous: previous[i]}
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
		result.Targets = append(result.Targets, row)
	}

	return result, nil
}
