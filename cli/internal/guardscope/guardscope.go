// Package guardscope decides whether a guard-tier intervention's measurement
// window is one the guard actually covers. It sits beside the harness adapters
// so every entry point that writes a record — the CLI and the dashboard — asks
// the same question; `internal/gate/mine` may import no adapter and cannot.
package guardscope

import (
	"fmt"
	"sort"
	"strings"

	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/codex"
	"github.com/steer-sh/steer-cli/internal/harness"
	"github.com/steer-sh/steer-cli/internal/pi"
	"github.com/steer-sh/steer-cli/internal/tier"
)

// RegistrarFor maps a registry id to its adapter. The guard store is shared, so
// an adapter only ever writes that harness's own registration and rules.
func RegistrarFor(id harness.ID) (harness.Registrar, error) {
	switch id {
	case harness.ClaudeCode:
		return claude.Registrar{}, nil
	case harness.Codex:
		return codex.Registrar{}, nil
	case harness.Pi:
		return pi.Registrar{}, nil
	default:
		return nil, fmt.Errorf("harness %q registers no hooks", id)
	}
}

// HookRegistrars returns an adapter for every harness Steer can register into,
// keeping the registry's order. onlyInstalled drops the ones whose configuration
// directory is absent from this machine; an unlisted harness is dropped when absent
// either way.
func HookRegistrars(onlyInstalled bool) ([]harness.Registrar, error) {
	out := make([]harness.Registrar, 0, len(harness.All()))
	for _, entry := range harness.All() {
		if !entry.Capabilities.HookRegistration {
			continue
		}
		if (onlyInstalled || !harness.Listed(entry.ID)) && !entry.Installed() {
			continue
		}
		registrar, err := RegistrarFor(entry.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, registrar)
	}
	return out, nil
}

// IsGuardTier says the record's fix is a steer guard, whose enforcement is per
// harness. The ladder's vocabulary lives in internal/tier; both spellings of the
// guard rung are live and this reads them both from there.
func IsGuardTier(value string) bool { return tier.IsGuard(value) }

// Guarded names every hook-registering harness whose configuration invokes the
// master guard hook, at either scope. A guard registered at one scope runs, so
// either answer counts.
func Guarded() ([]harness.ID, error) {
	var out []harness.ID
	for _, entry := range harness.All() {
		if !entry.Capabilities.HookRegistration {
			continue
		}
		registrar, err := RegistrarFor(entry.ID)
		if err != nil {
			return nil, err
		}
		for _, scope := range []string{harness.ProjectScope, harness.GlobalScope} {
			registered, err := registrar.MasterRegistered(scope)
			if err != nil || !registered {
				continue
			}
			out = append(out, entry.ID)
			break
		}
	}
	return out, nil
}

// Normalize parses the comma-separated and repeated forms a flag hands over, and
// validates each id against the registry. Blank entries are dropped, so a caller
// must read the returned length rather than the raw slice's.
func Normalize(values []string) ([]harness.ID, error) {
	var out []harness.ID
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			id, err := harness.Parse(part)
			if err != nil {
				return nil, err
			}
			if !contains(out, id) {
				out = append(out, id)
			}
		}
	}
	return out, nil
}

// Check refuses a guard-tier record whose measurement window admits a harness the
// guard does not run in. A scope decides which units are counted, not where the
// guard runs, so such a window pools guarded and unguarded work and can never come
// back true. Install the guard everywhere, then score each harness on its own
// record.
func Check(tier string, scopeAll bool, scopeHarness []string, force bool) error {
	if !IsGuardTier(tier) || force {
		return nil
	}
	requested, err := Normalize(scopeHarness)
	if err != nil {
		return err
	}
	guarded, err := Guarded()
	if err != nil {
		return err
	}
	where := "this guard is registered in no harness — run `steer register` first"
	if names := idStrings(guarded); len(names) > 0 {
		where = "this guard is registered in: " + strings.Join(names, ", ")
	}
	if len(requested) == 0 {
		return fmt.Errorf("a --tier %s record must name its harnesses with --scope-harness: "+
			"%s. A scope naming none counts every harness, including the ones the guard "+
			"never runs in. Score each harness on its own record, or pass --force", tier, where)
	}
	if scopeAll {
		return nil // BuildScope rejects the combination itself.
	}
	var missing, impossible []string
	for _, id := range requested {
		if contains(guarded, id) {
			continue
		}
		entry, ok := harness.Get(id)
		if ok && !entry.Capabilities.HookRegistration {
			impossible = append(impossible, string(id))
			continue
		}
		missing = append(missing, string(id))
	}
	// A harness that registers no hooks can never carry the guard, so naming it is
	// a scope to correct rather than a registration to go and write.
	if len(impossible) > 0 {
		return fmt.Errorf("--scope-harness names %s, which runs no steer guard at all: "+
			"drop it from the scope, or pass --force. %s",
			strings.Join(impossible, ", "), where)
	}
	if len(missing) > 0 {
		return fmt.Errorf("--scope-harness names %s, where this guard does not run: "+
			"register it there with `steer register --harness %s`, drop it from the scope, "+
			"or pass --force. %s", strings.Join(missing, ", "), missing[0], where)
	}
	for _, id := range requested {
		if RunnerUnapproved(id) {
			return fmt.Errorf("--scope-harness names %s, whose guard runner is registered but "+
				"has no approval record: %s runs no hook until the user approves it, so its "+
				"sessions would read the rule's text with no guard behind it. Restart it and "+
				"approve the definitions in /hooks, drop it from the scope, or pass --force",
				id, harnessName(id))
		}
	}
	return nil
}

// RunnerUnapproved reports a harness whose every registered guard runner is known
// to lack the approval the harness needs before it runs a hook. Codex is the one
// harness whose approval record Steer reads. A present record is not proof of
// approval, and an unreadable one is not proof of its absence, so neither counts.
func RunnerUnapproved(id harness.ID) bool {
	if id != harness.Codex {
		return false
	}
	registered := false
	for _, scope := range []string{harness.ProjectScope, harness.GlobalScope} {
		a := codex.MasterArming(scope, ".")
		if !a.Registered {
			continue
		}
		registered = true
		if !a.TrustStateKnown || a.TrustRecordPresent {
			return false
		}
	}
	return registered
}

func harnessName(id harness.ID) string {
	if entry, ok := harness.Get(id); ok {
		return entry.DisplayName
	}
	return string(id)
}

func idStrings(ids []harness.ID) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, string(id))
	}
	sort.Strings(out)
	return out
}

func contains(ids []harness.ID, want harness.ID) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}
