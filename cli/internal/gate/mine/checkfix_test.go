package mine

import (
	"io"
	"testing"

	"github.com/steer-sh/steer-cli/internal/transcript"
)

const boundRoot = "/src/one/steer"

func boundFacts() map[string]any {
	return map[string]any{
		CheckIDKey:      "no-count",
		CheckKindKey:    "script",
		CheckRepoKey:    boundRoot,
		CheckDigestKey:  "deadbeef",
		CheckReceiptKey: "run-1",
		CheckHarnessKey: []any{"claude-code"},
		CheckAdoptedKey: 1000,
		CheckHostKey:    "host-a",
	}
}

func checkRegister(t *testing.T, stateDir, ledger string, o RegisterOpts) error {
	t.Helper()
	o.Prose, o.Ledger = testProse, ledger
	o.Class, o.Match, o.Tier = "widget flag", "widget parser", "check"
	o.Target, o.InstalledTS, o.Force = ".steer/checks/no-count.sh", 2000, true
	return Register(io.Discard, stateDir, o)
}

// A new check-tier record binds its check, and the binding lands in the record.
func TestCheckTierRecordCarriesItsBinding(t *testing.T) {
	stateDir, ledger := writeLedger(t, nil)
	if err := checkRegister(t, stateDir, ledger, RegisterOpts{
		CheckID: "no-count", CheckFacts: boundFacts(),
		ScopeHarness: []string{"claude-code"}, ScopeRepoRoot: boundRoot,
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	records, _ := Load(stateDir)
	if len(records) != 1 {
		t.Fatalf("%d records; want the registered one", len(records))
	}
	fix, bound := CheckFixOf(records[0])
	if !bound || fix.ID != "no-count" || fix.Repo != boundRoot || fix.Receipt != "run-1" {
		t.Fatalf("binding %+v bound=%v; want the frozen check", fix, bound)
	}
	if CheckAttribution(records[0]) != AttributionCheckID {
		t.Errorf("attribution %q; want execution joined on the check id", CheckAttribution(records[0]))
	}
	scope := records[0]["scope"].(map[string]any)
	if scope["repo_root"] != boundRoot || numInt(scope["version"]) != ScopeVersionV3 {
		t.Errorf("scope %v; want the exact root at version %d", scope, ScopeVersionV3)
	}
}

// Each half of the contract is refused on its own: an unbound record, a window that
// admits every repository, and one naming a repository the check is not declared in.
func TestCheckTierRecordRefusesAnUnboundOrUnscopedFix(t *testing.T) {
	stateDir, ledger := writeLedger(t, nil)
	if err := checkRegister(t, stateDir, ledger, RegisterOpts{
		ScopeHarness: []string{"claude-code"}, ScopeRepoRoot: boundRoot,
	}); err == nil {
		t.Error("a check-tier record with no check id was registered")
	}
	partial := boundFacts()
	delete(partial, CheckReceiptKey)
	if err := checkRegister(t, stateDir, ledger, RegisterOpts{
		CheckID: "no-count", CheckFacts: partial,
		ScopeHarness: []string{"claude-code"}, ScopeRepoRoot: boundRoot,
	}); err == nil {
		t.Error("a binding with no validation receipt was registered")
	}
	if err := checkRegister(t, stateDir, ledger, RegisterOpts{
		CheckID: "no-count", CheckFacts: boundFacts(), ScopeAll: true,
	}); err == nil {
		t.Error("--scope-all was accepted for a repository-bound check")
	}
	if err := checkRegister(t, stateDir, ledger, RegisterOpts{
		CheckID: "no-count", CheckFacts: boundFacts(), ScopeHarness: []string{"claude-code"},
	}); err == nil {
		t.Error("a check-tier record was registered without naming its repository")
	}
	if err := checkRegister(t, stateDir, ledger, RegisterOpts{
		CheckID: "no-count", CheckFacts: boundFacts(),
		ScopeHarness: []string{"claude-code"}, ScopeRepoRoot: "/src/two/steer",
	}); err == nil {
		t.Error("a record was scoped to a repository the check is not declared in")
	}
	if records, _ := Load(stateDir); len(records) != 0 {
		t.Fatalf("%d records written by refused registrations", len(records))
	}
}

// A record written before bindings existed stays readable, and its target names no
// check: aggregate oracle success attributes no result to its assertion.
func TestLegacyCheckRecordReadsAsUnbound(t *testing.T) {
	legacy := Record{"id": "r", "class": "c", "tier": "check", "target": "check.sh",
		"status": "active"}
	fix, bound := CheckFixOf(legacy)
	if bound || fix.ID != "" {
		t.Fatalf("binding %+v bound=%v; want no identity inferred from the target", fix, bound)
	}
	if CheckAttribution(legacy) != AttributionUnavailable {
		t.Errorf("attribution %q; want unavailable", CheckAttribution(legacy))
	}
}

// A scope installed on a legacy record keeps a bound check inside its repository.
func TestScopeSetKeepsABoundCheckInItsRepository(t *testing.T) {
	stateDir, ledger := writeLedger(t, nil)
	bound := Record{"id": "r", "class": "c", "match": "widget parser", "tier": "check",
		"target": ".steer/checks/no-count.sh", "status": "active"}
	for k, v := range boundFacts() {
		bound[k] = v
	}
	if err := Save(stateDir, []Record{bound}); err != nil {
		t.Fatal(err)
	}
	opts := ScopeSetOpts{Ledger: ledger, Force: true, ScopeAll: true}
	if err := ScopeSet(io.Discard, stateDir, "r", opts); err == nil {
		t.Error("a bound check record was given an every-repository window")
	}
	opts = ScopeSetOpts{Ledger: ledger, Force: true, ScopeRepoRoot: boundRoot}
	if err := ScopeSet(io.Discard, stateDir, "r", opts); err != nil {
		t.Errorf("the check's own repository was refused: %v", err)
	}
}

// The exact-root predicate separates repositories the basename regex pools, and a
// transcript's project key names the same root as a ledger row's absolute path.
func TestExactRepoRootSeparatesSameBasenameRepositories(t *testing.T) {
	m, err := BuildScope(false, nil, "", boundRoot, "")
	if err != nil {
		t.Fatal(err)
	}
	check(t, "the root is stored exactly", m["repo_root"], boundRoot)
	check(t, "an exact root costs a version", m["version"], ScopeVersionV3)
	scope, _, err := ParseScope(Record{"scope": m})
	if err != nil {
		t.Fatal(err)
	}
	subject := func(root string) Subject { return Subject{RepoRoot: root, RepoRootKnown: root != ""} }
	check(t, "the declaring repository is eligible", scope.ClassifySubject(subject(boundRoot)), Eligible)
	check(t, "a sibling of the same name is not", scope.ClassifySubject(subject("/src/two/steer")), Ineligible)
	check(t, "a session names the root through its project key",
		scope.ClassifySubject(subject(transcript.NormalizeProject(boundRoot))), Eligible)
	check(t, "a unit with no root is unknown coverage", scope.ClassifySubject(subject("")), Unknown)

	// The older predicate keeps its basename-regex semantics, which both roots match.
	legacy, err := BuildScope(false, nil, "^steer$", "", "")
	if err != nil {
		t.Fatal(err)
	}
	check(t, "a basename scope stays at version 1", legacy["version"], ScopeVersionV1)
	old, _, err := ParseScope(Record{"scope": legacy})
	if err != nil {
		t.Fatal(err)
	}
	both := Subject{Repo: "steer", RepoKnown: true}
	check(t, "the basename regex admits either checkout", old.ClassifySubject(both), Eligible)

	if _, err := BuildScope(false, nil, "", "steer", ""); err == nil {
		t.Error("a relative repository root was accepted")
	}
}

// A chain keeps the whole root its rows recorded beside the basename.
func TestChainKeepsTheWholeRepositoryRoot(t *testing.T) {
	deny := row(1000, "deny")
	deny["repo"] = boundRoot
	ch := chain(t, 1000, deny)
	check(t, "the basename is kept for the older predicate", ch.Repo, "steer")
	check(t, "the whole root is preserved", ch.RepoRoot, boundRoot)
	check(t, "the root is known", ch.RepoRootKnown, true)

	bare := chain(t, 2000, row(2000, "deny"))
	check(t, "a row with no repo metadata knows no root", bare.RepoRootKnown, false)
}
