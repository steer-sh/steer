package claude

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/steer-sh/steer-cli/internal/harness"
	"github.com/steer-sh/steer-cli/internal/hookstore"
	"github.com/steer-sh/steer-cli/internal/instructions"
)

// ManifestRules returns the rules the manifest names, ordered by the id that
// names each one so a second sync over identical state writes identical bytes.
//
// The manifest has two sources. A hook's rule is the prose behind a guard; a
// rules entry is an instruction-tier rule with no guard behind it. The derived
// section cannot tell them apart, and that is the point: both are one bullet the
// agent reads. An id naming both is refused when the rule is written, so sorting
// on the id alone is total.
func ManifestRules(scope string) ([]string, error) {
	entries, err := manifestRuleEntries(scope)
	if err != nil {
		return nil, err
	}
	rules := make([]string, 0, len(entries))
	for _, e := range entries {
		rules = append(rules, e.text)
	}
	return rules, nil
}

// ruleEntry is one line of the derived section and the id that names it.
type ruleEntry struct{ id, text string }

// manifestRuleEntries is ManifestRules keeping each line's id, with blank text
// dropped the same way.
func manifestRuleEntries(scope string) ([]ruleEntry, error) {
	manifest, err := LoadManifest(scope)
	if err != nil {
		return nil, err
	}

	entries := make([]ruleEntry, 0, len(manifest.Hooks)+len(manifest.Rules))
	for id, hook := range manifest.Hooks {
		entries = append(entries, ruleEntry{id, hook.Rule})
	}
	for id, rule := range manifest.Rules {
		entries = append(entries, ruleEntry{id, rule.Text})
	}
	// Sorted on the id, then the text. Both write boundaries refuse an id naming a
	// guard and a rule, so only a hand-edited manifest reaches a collision here; it
	// yields both lines rather than losing one, and the second key keeps the order
	// total whichever map the reader ranged first.
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].id != entries[j].id {
			return entries[i].id < entries[j].id
		}
		return entries[i].text < entries[j].text
	})

	out := make([]ruleEntry, 0, len(entries))
	for _, e := range entries {
		if rule := strings.TrimSpace(e.text); rule != "" {
			out = append(out, ruleEntry{e.id, rule})
		}
	}
	return out, nil
}

// RulesPresence compares one harness's rules section with the manifest. The
// manifest is what a record binds; the section is what the agent reads. They part
// when a person edits the document, or when a harness was installed after the
// last sync, and nothing but a sync rewrites the section.
type RulesPresence struct {
	Path string
	// Expected counts the rules the manifest names in this scope.
	Expected int
	// Missing are the ids whose rule text the section does not carry.
	Missing []string
	// Extra are section lines the manifest does not name. The next sync removes them.
	Extra []string
}

// Drifted reports whether the section differs from the manifest in either direction.
func (p RulesPresence) Drifted() bool { return len(p.Missing) > 0 || len(p.Extra) > 0 }

// TargetRulesPresence reads one target's rules section against the scope's
// manifest. It writes nothing.
func TargetRulesPresence(scope string, target harness.Registrar) (RulesPresence, error) {
	out := RulesPresence{Missing: []string{}, Extra: []string{}}
	entries, err := manifestRuleEntries(scope)
	if err != nil {
		return out, err
	}
	path, err := target.InstructionPath(scope)
	if err != nil {
		return out, err
	}
	out.Path = path
	carried, err := instructions.SectionRules(path)
	if err != nil {
		return out, err
	}

	have := make(map[string]bool, len(carried))
	for _, line := range carried {
		have[line] = true
	}
	want := make(map[string]bool, len(entries))
	for _, e := range entries {
		want[e.text] = true
		if !have[e.text] {
			out.Missing = append(out.Missing, e.id)
		}
	}
	for _, line := range carried {
		if !want[line] {
			out.Extra = append(out.Extra, line)
		}
	}
	out.Expected = len(entries)
	return out, nil
}

// SyncTargets brings every target harness in line with the shared store: the
// master registration, and the rules section derived from the manifest. It names
// no hook, so it is also the retry for an install or a removal that stopped part
// way through the targets.
func SyncTargets(scope string, targets ...harness.Registrar) error {
	scope = strings.ToLower(strings.TrimSpace(scope))
	if err := ValidateScope(scope); err != nil {
		return err
	}

	rules, err := ManifestRules(scope)
	if err != nil {
		return err
	}
	hooks, err := ListIndividualHooks(scope)
	if err != nil {
		return err
	}

	for _, target := range harness.Targets(targets, Registrar{}) {
		if len(hooks) == 0 {
			if err := target.RemoveMaster(scope); err != nil {
				return err
			}
		} else if err := target.RegisterMaster(scope); err != nil {
			return err
		}
		if err := target.SetRules(rules, scope); err != nil {
			return err
		}
	}
	return nil
}

// SyncResult names each target a store sync wrote and each it left as it was.
type SyncResult struct {
	Scope      string
	Hooks      int
	MasterPath string
	Synced     []harness.Harness
	Current    []harness.Harness
}

// SyncStore registers the store's existing master in each target and writes each
// target's rules section, touching only the parts that differ from the store. It
// never writes the store itself: no master, manifest or fixture state. A store
// holding guards must hold a master that embeds exactly those guards.
func SyncStore(scope string, targets ...harness.Registrar) (*SyncResult, error) {
	scope = strings.ToLower(strings.TrimSpace(scope))
	if err := ValidateScope(scope); err != nil {
		return nil, err
	}
	hooks, err := ListIndividualHooks(scope)
	if err != nil {
		return nil, err
	}
	master, err := hookstore.MasterPath(scope)
	if err != nil {
		return nil, err
	}
	// A target reports the path its registration names, which under project scope
	// is relative to the config it was read from. Comparing that to the store's own
	// path would re-register on every sync run outside the repository root.
	registration, err := hookstore.MasterRegistration(scope, "")
	if err != nil {
		return nil, err
	}
	result := &SyncResult{Scope: scope, Hooks: len(hooks), MasterPath: master}
	if len(hooks) > 0 {
		if err := MasterCovers(scope); err != nil {
			return nil, fmt.Errorf("%w — run: steer hooks rebuild --scope %s", err, scope)
		}
	}
	rules, err := ManifestRules(scope)
	if err != nil {
		return nil, err
	}

	for _, target := range harness.Targets(targets, Registrar{}) {
		registered, err := target.RegisteredMasterPath(scope)
		if err != nil {
			return nil, err
		}
		stale := false
		if reader, ok := target.(masterCommandStaleReader); ok {
			if stale, err = reader.MasterCommandStale(scope); err != nil {
				return nil, err
			}
		}
		register := len(hooks) > 0 && (stale || !SamePath(registered, registration))
		deregister := len(hooks) == 0 && registered != ""
		presence, readErr := TargetRulesPresence(scope, target)
		rewrite := readErr != nil || presence.Drifted()

		switch {
		case register:
			err = target.RegisterMaster(scope)
		case deregister:
			err = target.RemoveMaster(scope)
		}
		if err != nil {
			return nil, err
		}
		if rewrite {
			if err := target.SetRules(rules, scope); err != nil {
				return nil, err
			}
		}
		if register || deregister || rewrite {
			result.Synced = append(result.Synced, target.Harness())
		} else {
			result.Current = append(result.Current, target.Harness())
		}
	}
	return result, nil
}

// masterCommandStaleReader is implemented by a harness whose registered command can
// name the right master in a form that needs a rewrite.
type masterCommandStaleReader interface {
	MasterCommandStale(scope string) (bool, error)
}

// SamePath reports whether two master paths name one file. A project path is
// relative to the harness config that carries it, and a caller reads that config
// at the working directory, so both resolve against the working directory.
func SamePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	absA, errA := filepath.Abs(filepath.FromSlash(a))
	absB, errB := filepath.Abs(filepath.FromSlash(b))
	return errA == nil && errB == nil && absA == absB
}

// masterSourcesPrefix opens the object in which a generated master carries each
// guard's source, keyed by guard name.
const masterSourcesPrefix = "\nSTEER_HOOK_SOURCES = "

// MasterCovers reports an error unless the store's master exists and embeds
// every guard in the store with its current source, and no other guard.
//
// It is the refusal CompareMasterSources feeds: sync reads the same comparison
// status reads, so one definition of source agreement serves both.
func MasterCovers(scope string) error {
	dir, err := GetHooksDir(scope)
	if err != nil {
		return err
	}
	path, err := hookstore.MasterPath(scope)
	if err != nil {
		return err
	}
	c := CompareMasterSources(dir, path)
	switch c.State {
	case ComparisonNoMaster:
		return fmt.Errorf("the store holds guards but no master at %s", path)
	case ComparisonUnavailable, ComparisonNotApplicable:
		return fmt.Errorf("%s", c.Reason)
	}
	var gaps []string
	for _, g := range c.Guards {
		if g.State != SourceMatches {
			gaps = append(gaps, gapPhrase(g))
		}
	}
	if len(gaps) > 0 {
		sort.Strings(gaps)
		return fmt.Errorf("%s does not match the store's guards: %s", path, strings.Join(gaps, "; "))
	}
	return nil
}
