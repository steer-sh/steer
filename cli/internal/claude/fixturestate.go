package claude

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/steer-sh/steer-cli/internal/guard/fixture"
	"github.com/steer-sh/steer-cli/internal/hookstore"
)

// FixtureSnapshot is one guard's stored fixture state read against the guard as it
// is installed now. It runs nothing and writes nothing.
//
// Changed and Unavailable are disjoint and both in FixtureComponent order: Changed
// names a component whose current value differs from the one the state was earned
// under, Unavailable one this machine could not read.
type FixtureSnapshot struct {
	Binding     *FixtureBinding
	Changed     []string
	Unavailable []string
	Reason      string
	// NoMasterIdentity marks a retained pass that recorded no master digest. Its
	// guard cannot be reported as current under a master identity it never named.
	NoMasterIdentity bool
}

// ReadHookFixtureSnapshot reads one registered guard's fixture state.
//
// A store or hook that is not here is (nil, nil): absence of a hook is not a
// failure. An unreadable or unparseable store is an error, for the reason
// SteerHookFacts gives — reading a corrupt store as absence reports it as an
// unregistered hook.
func ReadHookFixtureSnapshot(scope, hook string) (*FixtureSnapshot, error) {
	dir, err := GetHooksDir(scope)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	manifest, err := LoadManifest(scope)
	if err != nil {
		return nil, err
	}
	entry, ok := manifest.Hooks[hook]
	if !ok {
		return nil, nil
	}

	snapshot := &FixtureSnapshot{Binding: entry.Fixtures}
	if entry.Fixtures == nil || !entry.Fixtures.Tested {
		return snapshot, nil
	}
	if entry.Fixtures.MasterDigest == "" {
		snapshot.NoMasterIdentity = true
		return snapshot, nil
	}

	manifestFile, err := GetManifestFile(scope)
	if err != nil {
		return nil, err
	}
	fileName, err := hookstore.EntryFileName(manifestFile, hook, entry.File)
	if err != nil {
		return nil, err
	}

	var unavailable []string
	fixturesDigest := ""
	file, found, err := fixture.Load(dir, hook)
	switch {
	case err != nil:
		unavailable = append(unavailable, FixtureComponentFixtures)
		snapshot.Reason = fmt.Sprintf("the stored fixtures could not be read: %v", err)
	case !found:
		unavailable = append(unavailable, FixtureComponentFixtures)
		snapshot.Reason = "the stored fixtures are no longer in the store"
	default:
		if fixturesDigest = fixture.Digest(file); fixturesDigest == "" {
			unavailable = append(unavailable, FixtureComponentFixtures)
			snapshot.Reason = "the stored fixtures could not be encoded"
		}
	}

	hookDigest := fixture.FileDigest(filepath.Join(dir, fileName))
	if hookDigest == "" {
		unavailable = append(unavailable, FixtureComponentHook)
		snapshot.Reason = "the guard file could not be read"
	}

	// A master that is gone is a changed identity, not an unreadable component: the
	// guard no longer runs beneath the master its cases passed under. Any other stat
	// failure says nothing about the bytes, so it reads as unavailable.
	masterDigest := ""
	masterPath, err := GetMasterHookPath(scope)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(masterPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		unavailable = append(unavailable, FixtureComponentMaster)
		snapshot.Reason = fmt.Sprintf("the master hook could not be read: %v", err)
	} else {
		masterDigest = fixture.FileDigest(masterPath)
	}

	snapshot.Unavailable = unavailable
	for _, name := range entry.Fixtures.Changed(fixturesDigest, hookDigest, entry.RuleHash, masterDigest) {
		if !namesComponent(unavailable, name) {
			snapshot.Changed = append(snapshot.Changed, name)
		}
	}
	return snapshot, nil
}

func namesComponent(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
