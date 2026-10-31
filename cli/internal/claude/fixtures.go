package claude

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/steer-sh/steer-cli/internal/guard/fixture"
	"github.com/steer-sh/steer-cli/internal/hookstore"
)

// FixtureVerification is one store-wide rerun.
type FixtureVerification struct {
	Scope   string
	Reports []fixture.Report
}

// Failed reports whether any guard failed or could not be verified.
func (v *FixtureVerification) Failed() bool {
	if v == nil {
		return false
	}
	for _, r := range v.Reports {
		if !r.Tested() {
			return true
		}
	}
	return false
}

// SaveFixtures stores one guard's cases in the scope's store.
func SaveFixtures(scope string, file fixture.File) error {
	dir, err := GetHooksDir(scope)
	if err != nil {
		return err
	}
	return fixture.Save(dir, file)
}

// LoadFixtures reads one guard's stored cases.
func LoadFixtures(scope, hook string) (fixture.File, bool, error) {
	dir, err := GetHooksDir(scope)
	if err != nil {
		return fixture.File{}, false, err
	}
	return fixture.Load(dir, hook)
}

// RemoveFixtures drops one guard's cases. Fixtures go with the guard.
func RemoveFixtures(scope, hook string) error {
	dir, err := GetHooksDir(scope)
	if err != nil {
		return err
	}
	return fixture.Remove(dir, hook)
}

// VerifyHookFixtures replays one guard's stored cases and writes the state it
// earns into the manifest. A guard with no fixtures yields no report.
func VerifyHookFixtures(scope, hook string) (*fixture.Report, error) {
	dir, err := GetHooksDir(scope)
	if err != nil {
		return nil, err
	}
	file, ok, err := fixture.Load(dir, hook)
	if err != nil || !ok {
		return nil, err
	}

	manifest, err := LoadManifest(scope)
	if err != nil {
		return nil, err
	}
	entry, hasEntry := manifest.Hooks[hook]
	if !hasEntry {
		return nil, fmt.Errorf("hook not registered: %s", hook)
	}

	manifestFile, err := GetManifestFile(scope)
	if err != nil {
		return nil, err
	}
	fileName, err := hookstore.EntryFileName(manifestFile, hook, entry.File)
	if err != nil {
		return nil, err
	}
	guardPath := filepath.Join(dir, fileName)
	masterPath, err := GetMasterHookPath(scope)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(masterPath); err != nil {
		masterPath = ""
	}

	report, err := fixture.Runner{}.Run(guardPath, masterPath, file)
	if err != nil {
		return nil, err
	}

	entry.Fixtures = &FixtureBinding{
		Digest:       fixture.Digest(file),
		Cases:        len(file.Cases),
		Tested:       report.Tested(),
		HookDigest:   fixture.FileDigest(guardPath),
		MasterDigest: report.MasterDigest,
		RuleHash:     entry.RuleHash,
		VerifiedAt:   time.Now().UTC().Format(time.RFC3339),
		Shadowed:     report.Shadowed(),
	}
	if err := SetHookEntry(scope, hook, entry); err != nil {
		return nil, err
	}
	return &report, nil
}

// VerifyFixtures reruns every guard in the store that carries fixtures.
//
// It runs AFTER ConsolidateHooks and never gates it. A failing fixture must not
// stop an operator removing a guard, so consolidation clears the tested state
// and this pass re-establishes it. A stale claim is the harm; a blocked removal
// is a worse one.
func VerifyFixtures(scope string) (*FixtureVerification, error) {
	manifest, err := LoadManifest(scope)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(manifest.Hooks))
	for name := range manifest.Hooks {
		names = append(names, name)
	}
	sort.Strings(names)
	result := &FixtureVerification{Scope: scope}
	for _, name := range names {
		report, err := VerifyHookFixtures(scope, name)
		if err != nil {
			return result, err
		}
		if report != nil {
			result.Reports = append(result.Reports, *report)
		}
	}
	return result, nil
}

// clearFixtureState drops every guard's tested state. Consolidation rewrites the
// master, and a guard's state was earned under the master it ran beneath.
func clearFixtureState(scope string) error {
	manifest, err := LoadManifest(scope)
	if err != nil {
		return err
	}
	changed := false
	for name, entry := range manifest.Hooks {
		if entry.Fixtures == nil || !entry.Fixtures.Tested {
			continue
		}
		entry.Fixtures.Tested = false
		manifest.Hooks[name] = entry
		changed = true
	}
	if !changed {
		return nil
	}
	return SaveManifest(scope, manifest)
}
