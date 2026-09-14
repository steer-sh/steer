// Package checkscope binds a check-tier intervention to the check it names: the
// declaration, the repository it lives in, its detector digest, the receipt that
// validated it, this machine's adoption of the repository, and the harnesses whose
// Stop runner is registered. It sits beside the harness adapters because
// `internal/gate/mine` may import none, and the guard rung's guardscope is its
// sibling.
package checkscope

import (
	"fmt"
	"sort"
	"strings"

	"github.com/steer-sh/steer-cli/internal/checks"
	"github.com/steer-sh/steer-cli/internal/claude"
	codexhooks "github.com/steer-sh/steer-cli/internal/codex"
	"github.com/steer-sh/steer-cli/internal/gate"
	"github.com/steer-sh/steer-cli/internal/gate/mine"
	"github.com/steer-sh/steer-cli/internal/harness"
	"github.com/steer-sh/steer-cli/internal/tier"
)

// Harnesses are the harnesses whose root Stop event can run an adopted repository's
// oracle. Pi registers no Stop runner.
var Harnesses = []harness.ID{harness.ClaudeCode, harness.Codex}

// Scopes are the two registration scopes a runner can be armed at. A runner
// registered at either scope runs, so either answer counts.
var Scopes = []string{harness.ProjectScope, harness.GlobalScope}

// Arming is one harness's Stop runner registration at one scope, read back from the
// file the harness itself loads.
type Arming struct {
	Registered bool
	Timeout    int
	Command    string
	Path       string
}

// RunnerArming reads one harness's registration at one scope. projectDir is the
// directory whose project configuration Codex reads; Claude Code resolves its own.
func RunnerArming(target harness.ID, scope, projectDir string) (Arming, error) {
	if target == harness.Codex {
		a := codexhooks.CheckHookArming(scope, projectDir)
		return Arming{Registered: a.Registered, Timeout: a.Timeout, Command: a.Command, Path: a.Path}, nil
	}
	a, err := claude.CheckHookArming(scope)
	if err != nil {
		return Arming{}, err
	}
	path, _ := claude.GetSettingsFile(scope)
	return Arming{Registered: a.Registered, Timeout: a.Timeout, Command: a.Command, Path: path}, nil
}

// Armed names every harness running the Stop runner at either scope.
func Armed(projectDir string) ([]harness.ID, error) {
	var out []harness.ID
	for _, target := range Harnesses {
		for _, scope := range Scopes {
			a, err := RunnerArming(target, scope, projectDir)
			if err != nil || !a.Registered {
				continue
			}
			out = append(out, target)
			break
		}
	}
	return out, nil
}

// Bind freezes the state a check-tier record binds, or reports the step that is
// missing; --force does not reach it. tierValue selects the rung, and a record on any
// other rung binds nothing and gets a nil map. scopeHarness is the record's own
// eligibility scope, which must name only harnesses the runner is registered in.
func Bind(tierValue, root, checkID string, scopeHarness []harness.ID) (map[string]any, error) {
	if !tier.IsCheck(tierValue) {
		return nil, nil
	}
	if checkID == "" {
		return nil, fmt.Errorf("a check tier needs --check-id (the id in %s)",
			checks.ManifestPath(root))
	}
	m, err := checks.Load(root)
	if err != nil {
		return nil, err
	}
	var decl checks.Declaration
	for _, d := range m.Checks {
		if d.ID == checkID {
			decl = d
			break
		}
	}
	if decl.ID == "" {
		return nil, fmt.Errorf("no check %q in %s. Declare it with `steer check add %s`",
			checkID, checks.ManifestPath(root), checkID)
	}
	identity, err := checks.RepoIdentity(root)
	if err != nil {
		return nil, err
	}
	det, err := checks.DetectorOf(root, decl)
	if err != nil {
		return nil, fmt.Errorf("reading the detector of check %q: %w", checkID, err)
	}
	history, err := checks.History(root)
	if err != nil {
		return nil, err
	}
	fresh := checks.FreshnessOf(root, decl, history)
	if fresh.State != checks.FreshCurrent {
		return nil, fmt.Errorf("check %q has no current receipt (%s): replay its cases with "+
			"`steer check test %s` before registering it as a fix", checkID, freshWhy(fresh), checkID)
	}
	adoption, adopted, err := checks.AdoptionOf(identity)
	if err != nil {
		return nil, err
	}
	if !adopted {
		return nil, fmt.Errorf("%s is not adopted on this machine, so its checks do not run "+
			"at turn end. Adopt it with `steer check adopt`", identity)
	}
	armed, err := Armed(root)
	if err != nil {
		return nil, err
	}
	if len(armed) == 0 {
		return nil, fmt.Errorf("no harness runs the Stop runner on this machine: register one "+
			"with `steer check install --harness %s`", harness.ClaudeCode)
	}
	if err := requireArmedScope(scopeHarness, armed); err != nil {
		return nil, err
	}
	ids := make([]any, 0, len(scopeHarness))
	for _, id := range idStrings(scopeHarness) {
		ids = append(ids, id)
	}
	return map[string]any{
		mine.CheckIDKey:      decl.ID,
		mine.CheckKindKey:    decl.Kind,
		mine.CheckRepoKey:    identity,
		mine.CheckDigestKey:  det.Digest,
		mine.CheckReceiptKey: fresh.Receipt.RunID,
		mine.CheckHarnessKey: ids,
		mine.CheckAdoptedKey: int(adoption.At.Unix()),
		mine.CheckHostKey:    gate.HostID(),
	}, nil
}

// requireArmedScope refuses a window admitting a harness the runner is not registered
// in, which pools turns the check never ran at with the ones it did.
func requireArmedScope(requested, armed []harness.ID) error {
	where := "the Stop runner is registered in: " + strings.Join(idStrings(armed), ", ")
	if len(requested) == 0 {
		return fmt.Errorf("a check-tier record names the harnesses it is measured on with "+
			"--scope-harness: %s", where)
	}
	var missing []string
	for _, id := range requested {
		if !contains(armed, id) {
			missing = append(missing, string(id))
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("--scope-harness names %s, where the Stop runner is not registered: "+
			"register it with `steer check install --harness %s`, or drop it from the scope. %s",
			strings.Join(missing, ", "), missing[0], where)
	}
	return nil
}

// freshWhy names what moved since the latest receipt, for the error that asks for a
// replay.
func freshWhy(f checks.Freshness) string {
	if f.State == checks.FreshNone {
		return "never validated"
	}
	if len(f.Changed) == 0 {
		return f.State
	}
	return "stale: " + strings.Join(f.Changed, ", ") + " changed"
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
