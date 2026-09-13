package mine

import (
	"fmt"
	"sort"
	"strings"
)

// The record fields a check-tier record binds its fix through. A record carrying all
// of them names one declared check in one repository, validated and authorized to run
// on this machine; the adapters that read those facts live outside this package, so a
// writer hands them in and this is where the contract is enforced.
const (
	CheckIDKey       = "check_id"
	CheckKindKey     = "check_kind"
	CheckRepoKey     = "check_repo"
	CheckDigestKey   = "check_digest"
	CheckReceiptKey  = "check_receipt"
	CheckHarnessKey  = "check_harness"
	CheckAdoptedKey  = "check_adopted"
	checkBindingFlag = "--check-id"
)

// CheckBindingKeys is that contract in the order an error reports it.
var CheckBindingKeys = []string{
	CheckIDKey, CheckKindKey, CheckRepoKey, CheckDigestKey,
	CheckReceiptKey, CheckHarnessKey, CheckAdoptedKey,
}

// Execution attribution of a check-tier record: whether a check execution record can
// be joined to it at all.
const (
	// AttributionCheckID is a bound record, whose execution evidence joins on the
	// check id the record names.
	AttributionCheckID = "check-id"
	// AttributionUnavailable is an unbound record. Its target names a script, and a
	// script's aggregate outcome attributes no result to this record's assertion.
	AttributionUnavailable = "unavailable"
)

// CheckFix is a check-tier record's binding: the declared check, the repository it is
// declared in, the detector the record was registered against, the receipt that
// validated it, and the adoption and harness registration that let it run.
type CheckFix struct {
	ID        string
	Kind      string
	Repo      string
	Digest    string
	Receipt   string
	Harnesses []string
	AdoptedTS float64
}

// CheckFixOf reads a record's binding. bound is false for a record written before
// bindings existed; identity is never inferred from the record's target, which names
// the script a fix landed in and no check inside it.
func CheckFixOf(record Record) (CheckFix, bool) {
	fix := CheckFix{
		ID:        str(record[CheckIDKey]),
		Kind:      str(record[CheckKindKey]),
		Repo:      str(record[CheckRepoKey]),
		Digest:    str(record[CheckDigestKey]),
		Receipt:   str(record[CheckReceiptKey]),
		AdoptedTS: numFloat(record[CheckAdoptedKey]),
	}
	for _, item := range asList(record[CheckHarnessKey]) {
		if id := str(item); id != "" {
			fix.Harnesses = appendUnique(fix.Harnesses, id)
		}
	}
	return fix, fix.ID != ""
}

// CheckAttribution reports whether execution evidence can be attributed to a
// check-tier record. It is the reader's half of the binding contract: an unbound
// legacy record stays readable and reports unavailable rather than borrowing the
// outcome of whatever script its target names.
func CheckAttribution(record Record) string {
	if _, bound := CheckFixOf(record); bound {
		return AttributionCheckID
	}
	return AttributionUnavailable
}

// requireCheckBinding refuses a new check-tier record that binds no check. Aggregate
// oracle success is not evidence that a particular assertion ran, so a record with no
// check id can never be scored against execution evidence.
func requireCheckBinding(checkID string, facts map[string]any) error {
	if checkID == "" {
		return fmt.Errorf("a check tier needs %s (the id in .steer/checks/manifest.json). "+
			"Declare the check first with `steer check add <id>`", checkBindingFlag)
	}
	var missing []string
	for _, key := range CheckBindingKeys {
		if !truthy(facts[key]) {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("the binding of check %s is missing %s — a check-tier record binds "+
			"the check, its repository, its detector digest, a current receipt, and the "+
			"adoption and harness registration that run it", pyRepr(checkID),
			strings.Join(missing, ", "))
	}
	return nil
}

// requireCheckScope refuses a scope a repository check cannot satisfy. The check runs
// in the one repository that declares it, so a window admitting any other repository
// pools work the check never saw and can never come back true.
func requireCheckScope(checkID string, facts map[string]any, scopeMap map[string]any) error {
	repo := str(facts[CheckRepoKey])
	if truthy(scopeMap["all"]) {
		return fmt.Errorf("--scope-all cannot be the window of check %s: it runs in %s alone. "+
			"Scope the record with --scope-repo-root %s", pyRepr(checkID), repo, repo)
	}
	scoped := str(scopeMap["repo_root"])
	if scoped == "" {
		return fmt.Errorf("a check-tier record names the repository it runs in: pass "+
			"--scope-repo-root %s", repo)
	}
	if !sameRepoRoot(scoped, repo) {
		return fmt.Errorf("--scope-repo-root %s is not the repository check %s is declared in. "+
			"Pass --scope-repo-root %s", pyRepr(scoped), pyRepr(checkID), repo)
	}
	return nil
}
