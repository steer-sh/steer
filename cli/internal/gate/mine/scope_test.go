package mine

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/steer-sh/steer-cli/internal/gate"
)

func repo(r gate.Row, top string) gate.Row { r["repo"] = top; return r }
func paths(r gate.Row, ps ...string) gate.Row {
	list := make([]any, len(ps))
	for i, p := range ps {
		list[i] = p
	}
	r["changed_paths"] = list
	return r
}

func TestBuildScopeValidation(t *testing.T) {
	if _, err := BuildScope(true, nil, "steer", ""); err == nil {
		t.Error("all with a predicate must be refused")
	}
	if _, err := BuildScope(false, nil, "", ""); err == nil {
		t.Error("no scope at all must be refused")
	}
	if _, err := BuildScope(false, nil, "(", ""); err == nil {
		t.Error("a bad repo regex must be refused")
	}
	all, err := BuildScope(true, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	check(t, "all scope stores the flag", all["all"], true)
	check(t, "all scope stores the version", all["version"], ScopeVersionV1)

	pred, err := BuildScope(false, nil, "steer", `cli/.*\.go`)
	if err != nil {
		t.Fatal(err)
	}
	check(t, "predicate scope stores repo", pred["repo"], "steer")
	check(t, "predicate scope stores path", pred["path"], `cli/.*\.go`)
	if _, ok := pred["all"]; ok {
		t.Error("a predicate scope must not carry all")
	}
}

func TestParseScope(t *testing.T) {
	if _, has, err := ParseScope(Record{"id": "x"}); has || err != nil {
		t.Errorf("a legacy record has no scope: has=%v err=%v", has, err)
	}
	if _, _, err := ParseScope(Record{"scope": map[string]any{"version": 3, "all": true}}); err == nil {
		t.Error("a future scope version must be refused")
	}
	s, has, err := ParseScope(Record{"scope": map[string]any{"version": 1, "all": true}})
	if err != nil || !has {
		t.Fatalf("all scope: has=%v err=%v", has, err)
	}
	check(t, "all is parsed", s.All, true)
}

func TestScopeClassifyAndCover(t *testing.T) {
	// A chain in repo steer touching a Go file, one touching a doc, one with no metadata.
	chGo := BuildChains([]gate.Row{
		paths(repo(row(1000, "deny"), "/home/u/steer"), "cli/main.go"),
		concerns(row(1001, "retry"), "round", c("x")),
	}, false)[0]
	chDoc := BuildChains([]gate.Row{
		paths(repo(row(2000, "deny"), "/home/u/steer"), "README.md"),
		concerns(row(2001, "retry"), "round", c("x")),
	}, false)[0]
	chBare := BuildChains([]gate.Row{
		row(3000, "deny"),
		concerns(row(3001, "retry"), "round", c("x")),
	}, false)[0]

	check(t, "repo basename is recovered", chGo.Repo, "steer")
	check(t, "paths are unioned", chGo.Paths, []string{"cli/main.go"})
	check(t, "a bare chain knows no repo", chBare.RepoKnown, false)
	check(t, "a bare chain knows no paths", chBare.PathsKnown, false)

	goScope, _, _ := ParseScope(Record{"scope": map[string]any{"version": 1, "repo": "steer", "path": `\.go$`}})
	check(t, "repo and path both match → eligible", goScope.Classify(chGo), Eligible)
	check(t, "path fails → ineligible", goScope.Classify(chDoc), Ineligible)
	check(t, "no metadata → unknown", goScope.Classify(chBare), Unknown)

	cov := Cover([]Chain{chGo, chDoc, chBare}, goScope)
	check(t, "coverage total", cov.Total, 3)
	check(t, "coverage eligible", cov.Eligible, 1)
	check(t, "coverage ineligible", cov.Ineligible, 1)
	check(t, "coverage unknown", cov.Unknown, 1)
	check(t, "eligible chains are returned", len(cov.EligibleChains()), 1)

	// A definite path non-match excludes even when repo is unknown: AND cannot pass.
	pathScope, _, _ := ParseScope(Record{"scope": map[string]any{"version": 1, "repo": "steer", "path": `\.go$`}})
	chDocNoRepo := BuildChains([]gate.Row{
		paths(row(4000, "deny"), "README.md"),
		concerns(row(4001, "retry"), "round", c("x")),
	}, false)[0]
	check(t, "a definite non-match wins over an unknown", pathScope.Classify(chDocNoRepo), Ineligible)

	// A nil scope admits everything.
	check(t, "nil scope covers all", Cover([]Chain{chGo, chDoc, chBare}, nil).Eligible, 3)
}

func TestPathsFromDiff(t *testing.T) {
	diff := "diff --git a/cli/main.go b/cli/main.go\n" +
		"--- a/cli/main.go\n+++ b/cli/main.go\n@@ -1 +1 @@\n-x\n+y\n" +
		"diff --git a/old.txt b/new.txt\n--- a/old.txt\n+++ b/new.txt\n" +
		"diff --git a/gone.go b/gone.go\n--- a/gone.go\n+++ /dev/null\n" +
		// A file whose real path starts with b/: only the git prefix may be stripped.
		"diff --git a/b/keep.go b/b/keep.go\n--- a/b/keep.go\n+++ b/b/keep.go\n" +
		// Hunk-body lines that look like headers must not be read as paths.
		"diff --git a/sql.go b/sql.go\n--- a/sql.go\n+++ b/sql.go\n" +
		"@@ -1,2 +1,2 @@\n--- old comment\n+++ new comment\n"
	got := pathsFromDiff(diff)
	check(t, "recovers header paths only, keeping a real b/ prefix and ignoring hunk bodies",
		got, []string{"cli/main.go", "old.txt", "new.txt", "gone.go", "b/keep.go", "sql.go"})
}

func TestSlashBase(t *testing.T) {
	check(t, "posix path", slashBase("/home/u/steer"), "steer")
	check(t, "windows path on a posix host", slashBase(`C:\Users\dev\steer`), "steer")
	check(t, "trailing slash", slashBase("/home/u/steer/"), "steer")
	check(t, "bare name", slashBase("steer"), "steer")
}

func TestSidecarRecovery(t *testing.T) {
	stateDir := t.TempDir()
	diffDir := filepath.Join(stateDir, "diffs")
	if err := os.MkdirAll(diffDir, 0o755); err != nil {
		t.Fatal(err)
	}
	diffName := "100-sess-r1.diff"
	body := "diff --git a/cli/gate.go b/cli/gate.go\n--- a/cli/gate.go\n+++ b/cli/gate.go\n@@\n+z\n"
	if err := os.WriteFile(filepath.Join(diffDir, diffName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ledger := filepath.Join(stateDir, "adjudication.jsonl")
	deny := row(100, "deny")
	deny["diff_file"] = diffName
	f, _ := os.Create(ledger)
	f.Write(append(mustJSON(t, deny), '\n'))
	f.Write(append(mustJSON(t, concerns(row(101, "retry"), "round", c("y"))), '\n'))
	f.Close()

	rows, err := LoadRows(stateDir, ledger)
	if err != nil {
		t.Fatal(err)
	}
	ch := BuildChains(rows, false)[0]
	check(t, "sidecar paths are recovered", ch.Paths, []string{"cli/gate.go"})
	check(t, "recovered paths are known", ch.PathsKnown, true)
}

func TestScopedBaselineAndMigration(t *testing.T) {
	// Twelve eligible chains (Go paths) carrying the class, twelve doc chains that never
	// count, before install; twelve clean eligible chains after.
	var rows []gate.Row
	ts := 1000
	mkChain := func(p, text string) {
		rows = append(rows,
			paths(repo(row(ts, "deny"), "/x/steer"), p),
			concerns(row(ts+1, "retry"), "round", c(text)),
			firing(row(ts+2, "clean"), 2))
		ts += 100
	}
	for i := 0; i < 12; i++ {
		mkChain("cli/a.go", "widget parser drops a flag")
	}
	for i := 0; i < 12; i++ {
		mkChain("docs/x.md", "widget parser drops a flag")
	}
	install := ts
	for i := 0; i < 12; i++ {
		mkChain("cli/a.go", "unrelated nit")
	}
	stateDir, ledger := writeLedger(t, rows)

	// A path-scoped register counts only the Go chains in its baseline.
	if err := Register(io.Discard, stateDir, RegisterOpts{
		Ledger: ledger, Class: "widget flag", Match: "widget parser",
		Tier: "check", Target: "check.sh", InstalledTS: int64(install),
		ScopeRepo: "steer", ScopePath: `\.go$`,
	}); err != nil {
		t.Fatal(err)
	}
	recs, _ := Load(stateDir)
	baseline := recs[0]["baseline"].(map[string]any)
	check(t, "scoped baseline counts only eligible chains", numInt(baseline["hits"]), 12)
	check(t, "scope is stored", recs[0]["scope"].(map[string]any)["path"], `\.go$`)

	allRows, _ := LoadRows(stateDir, ledger)
	chains := BuildChains(allRows, false)
	scored := scoreOK(t, recs[0], chains, 12)
	after := scored["after"].(map[string]any)
	check(t, "after-window is scoped to eligible chains", numInt(after["chains"]), 12)
	check(t, "before-window is scoped to eligible chains", numInt(scored["before"].(map[string]any)["chains"]), 12)
}

func TestScopeSetMigration(t *testing.T) {
	var rows []gate.Row
	ts := 1000
	mk := func(text string) {
		rows = append(rows,
			paths(repo(row(ts, "deny"), "/x/steer"), "cli/a.go"),
			concerns(row(ts+1, "retry"), "round", c(text)),
			firing(row(ts+2, "clean"), 2))
		ts += 100
	}
	for i := 0; i < 6; i++ {
		mk("legacy class hit")
	}
	install := ts
	for i := 0; i < 6; i++ {
		mk("legacy class hit")
	}
	stateDir, ledger := writeLedger(t, rows)

	// A legacy record with no scope, written directly.
	legacy := Record{
		"id": "legacy", "class": "legacy", "match": "legacy class", "kind": "defect",
		"tier": "check", "target": "check.sh", "status": "active",
		"installed_ts": install, "installed_date": "x",
	}
	if err := Save(stateDir, []Record{legacy}); err != nil {
		t.Fatal(err)
	}

	migrateTS := ts + 1000
	if err := ScopeSet(io.Discard, stateDir, "legacy", ScopeSetOpts{
		Ledger: ledger, ScopeRepo: "steer", ScopeSetTS: int64(migrateTS),
	}); err != nil {
		t.Fatal(err)
	}
	rec := loadByID(t, stateDir)["legacy"]
	check(t, "scope is installed", rec["scope"].(map[string]any)["repo"], "steer")
	check(t, "scope_set_ts is stamped", numInt(rec["scope_set_ts"]), migrateTS)

	// The after-window opens at scope_set_ts, so every post-install chain predates it.
	allRows, _ := LoadRows(stateDir, ledger)
	scored := scoreOK(t, rec, BuildChains(allRows, false), 1)
	check(t, "migration boundary empties the after-window", numInt(scored["after"].(map[string]any)["chains"]), 0)

	// A second scope on an already-scoped record is refused.
	if err := ScopeSet(io.Discard, stateDir, "legacy", ScopeSetOpts{
		Ledger: ledger, ScopeAll: true,
	}); err == nil {
		t.Error("an installed scope must be frozen")
	}
}

// TestCoverCorpusPreviewsWhatTheWriteFreezes pins the workbench preview against the
// baseline a register would store. An author who reads one number and gets another has
// no way to tell a bad regex from an incomplete corpus.
func TestCoverCorpusPreviewsWhatTheWriteFreezes(t *testing.T) {
	var rows []gate.Row
	ts := 1000
	for i := 0; i < 6; i++ { // adjudicated chains carrying the class
		rows = append(rows, row(ts, "deny"), concerns(row(ts+1, "retry"), "round", c("widget parser drops a flag")))
		ts += 100
	}
	for i := 0; i < 4; i++ { // chains the resolver closed as permanently unadjudicated
		rows = append(rows, row(ts, "deny"), closedMiss(ts+1))
		ts += 100
	}
	install := ts
	stateDir, ledger := writeLedger(t, rows)

	withClock(int64(install)+1_000_000, func() {
		cov, err := CoverCorpus(stateDir, ledger, nil, "", "")
		if err != nil {
			t.Fatal(err)
		}
		check(t, "the preview counts only measurable chains", cov.Eligible, 6)
		check(t, "and says why the rest are unknown", cov.UnknownEvidence, 4)
		check(t, "no scope excluded anything under an all-scope", cov.UnknownScope, 0)

		if err := Register(io.Discard, stateDir, RegisterOpts{
			ScopeAll: true, Ledger: ledger, Class: "widget parser flag loss", Match: "widget parser",
			Kind: "defect", Tier: "check", Target: "check.sh", InstalledTS: int64(install),
		}); err != nil {
			t.Fatal(err)
		}
		recorded, _ := Load(stateDir)
		stored := recorded[0]["baseline"].(map[string]any)
		check(t, "the write froze the window the preview promised",
			numInt(stored["units"]), cov.Eligible)
	})
}

// --- scope v2: the harness predicate ----------------------------------------

func TestScopeWithNoHarnessStaysV1(t *testing.T) {
	// v1 expresses repo and path, so an existing workflow keeps writing a v1 scope an
	// older binary can still read. Only the new predicate costs compatibility.
	pred, err := BuildScope(false, nil, "steer", "")
	if err != nil {
		t.Fatal(err)
	}
	check(t, "repo-only scope version", pred["version"], ScopeVersionV1)

	withHarness, err := BuildScope(false, []string{"codex"}, "steer", "")
	if err != nil {
		t.Fatal(err)
	}
	check(t, "harness scope version", withHarness["version"], ScopeVersionV2)
	check(t, "harness list", withHarness["harness"], []any{"codex"})
}

func TestBuildScopeNormalizesAndValidatesHarnesses(t *testing.T) {
	m, err := BuildScope(false, []string{"codex,claude", "codex"}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	check(t, "sorted and deduplicated", m["harness"], []any{"claude-code", "codex"})

	if _, err := BuildScope(false, []string{"emacs"}, "", ""); err == nil {
		t.Error("an unknown harness id must be refused at write time")
	}
	if _, err := BuildScope(true, []string{"codex"}, "", ""); err == nil {
		t.Error("--scope-all is exclusive with --scope-harness")
	}
	if _, err := BuildScope(false, []string{" , "}, "", ""); err == nil {
		t.Error("a harness list of only separators names no harness")
	}
}

func TestHarnessPredicateAndsWithRepoAndPath(t *testing.T) {
	scope, has, err := ParseScope(Record{"scope": map[string]any{
		"version": ScopeVersionV2,
		"harness": []any{"codex"},
		"repo":    "^steer$",
	}})
	if err != nil || !has {
		t.Fatalf("parse: has=%v err=%v", has, err)
	}
	cases := []struct {
		name string
		ch   Chain
		want Eligibility
	}{
		{"both predicates match", Chain{Harness: "codex", HarnessKnown: true, Repo: "steer", RepoKnown: true}, Eligible},
		{"harness differs", Chain{Harness: "claude-code", HarnessKnown: true, Repo: "steer", RepoKnown: true}, Ineligible},
		{"repo differs", Chain{Harness: "codex", HarnessKnown: true, Repo: "other", RepoKnown: true}, Ineligible},
		{"harness unstamped", Chain{Repo: "steer", RepoKnown: true}, Unknown},
		// A definite non-match outranks an unknown: the chain is excluded whatever the
		// missing stamp would have said.
		{"unknown harness, wrong repo", Chain{Repo: "other", RepoKnown: true}, Ineligible},
	}
	for _, tc := range cases {
		if got := scope.Classify(tc.ch); got != tc.want {
			t.Errorf("%s: Classify = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestAV1ScopeIgnoresHarnessAltogether(t *testing.T) {
	// A record frozen before the predicate existed must classify exactly as it did.
	scope, _, err := ParseScope(Record{"scope": map[string]any{"version": ScopeVersionV1, "repo": "^steer$"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range []string{"claude-code", "codex", ""} {
		ch := Chain{Repo: "steer", RepoKnown: true, Harness: h, HarnessKnown: h != ""}
		if got := scope.Classify(ch); got != Eligible {
			t.Errorf("harness %q: Classify = %v, want Eligible", h, got)
		}
	}
}
