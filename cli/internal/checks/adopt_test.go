package checks

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// adoptableRepo makes a repository whose no-count check validated through Add
// and whose check.sh is the scaffold.
func adoptableRepo(t *testing.T) (string, AdoptOptions) {
	t.Helper()
	root, opts := oracleRepo(t, nil, "")
	writeCases(t, root, "no-count", goodCase(), defectCase("literal test count"))
	if _, err := Add(root, noCount); err != nil {
		t.Fatalf("Add: %v", err)
	}
	return root, AdoptOptions{SteerBin: opts.SteerBin, Timeout: opts.Timeout}
}

// Adoption reuses a current receipt, runs the oracle with isolated evidence,
// and records the exact resolved root; a symlinked path adopts the same root
// and a second repository stays unadopted.
func TestAdoptRecordsTheResolvedRootWithoutLiveEvidence(t *testing.T) {
	root, opts := adoptableRepo(t)
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	res, err := Adopt(link, opts)
	if err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	identity, _ := RepoIdentity(root)
	if res.Already || res.Adoption.Repo != identity || !res.Oracle.Passed() || res.Oracle.Source != SourceAdopt {
		t.Fatalf("result %+v; want a new adoption of %s after a passing adopt run", res, identity)
	}
	if len(res.Validations) != 1 || !res.Validations[0].Reused || res.Adoption.Receipts["no-count"] != res.Validations[0].Run.RunID {
		t.Fatalf("validations %+v receipts %v; want the Add receipt reused and named", res.Validations, res.Adoption.Receipts)
	}
	if len(res.Executions) != 1 || res.Executions[0].Outcome != OutcomePassed {
		t.Fatalf("adopt run records %+v; want no-count passed", res.Executions)
	}
	if stored, _ := Executions(root); len(stored) != 0 {
		t.Fatalf("adoption wrote %d live execution records", len(stored))
	}
	if runs, _ := OracleRuns(root); len(runs) != 0 {
		t.Fatalf("adoption wrote %d live oracle runs", len(runs))
	}
	if _, ok, _ := AdoptionOf(identity); !ok {
		t.Fatal("the resolved root is not adopted")
	}

	otherIdentity, _ := RepoIdentity(t.TempDir())
	if _, ok, _ := AdoptionOf(otherIdentity); ok {
		t.Fatal("a repository that was never adopted reads as adopted")
	}

	again, err := Adopt(root, opts)
	if err != nil || !again.Already || again.Oracle.RunID != "" {
		t.Fatalf("second Adopt: %+v err %v; want already adopted with nothing run", again, err)
	}
	if removed, err := Unadopt(identity); err != nil || !removed {
		t.Fatalf("Unadopt: removed %v err %v", removed, err)
	}
	if _, ok, _ := AdoptionOf(identity); ok {
		t.Fatal("the root is still adopted after Unadopt")
	}
	if removed, err := Unadopt(identity); err != nil || removed {
		t.Fatalf("second Unadopt: removed %v err %v; want nothing to remove", removed, err)
	}
	if history, _ := History(root); len(history) != 1 {
		t.Fatalf("validation history holds %d runs; want the Add run kept", len(history))
	}
}

// A linked worktree of an adopted checkout is adopted with it and reads its
// receipts against its own detector. An unadopt in the worktree withdraws the
// worktree alone, and the main checkout's unadopt withdraws both.
func TestALinkedWorktreeInheritsItsMainCheckoutsAdoption(t *testing.T) {
	root, opts := adoptableRepo(t)
	gitIn(t, root, "init", "-q")
	commitAll(t, root, "checks")
	tree := filepath.Join(t.TempDir(), "tree")
	gitIn(t, root, "worktree", "add", "-q", tree)
	main, _ := RepoIdentity(root)
	worktree, _ := RepoIdentity(tree)
	if got, ok := MainCheckout(worktree); !ok || got != main {
		t.Fatalf("MainCheckout(worktree) = %q, %v; want %s", got, ok, main)
	}
	if _, ok := MainCheckout(main); ok {
		t.Fatal("a main checkout reported a main checkout of its own")
	}
	if _, ok, _ := AdoptionOf(worktree); ok {
		t.Fatal("the worktree of an unadopted checkout reads as adopted")
	}

	if _, err := Adopt(root, opts); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	if a, ok, _ := AdoptionOf(worktree); !ok || a.Repo != main {
		t.Fatalf("worktree adoption %+v, %v; want the main checkout's", a, ok)
	}
	history, _ := History(tree)
	if f := FreshnessOf(tree, noCount, history); f.State != FreshCurrent {
		t.Fatalf("worktree freshness %s; want the main checkout's receipt current", f.State)
	}
	writeFile(t, tree, ".steer/checks/no-count.sh", "exit 0\n")
	if f := FreshnessOf(tree, noCount, history); f.State != FreshStale {
		t.Fatalf("worktree freshness %s after a detector edit; want stale", f.State)
	}

	if removed, err := Unadopt(worktree); err != nil || !removed {
		t.Fatalf("Unadopt(worktree): removed %v err %v", removed, err)
	}
	if _, ok, _ := AdoptionOf(worktree); ok {
		t.Fatal("the worktree is still adopted after its own unadopt")
	}
	if _, ok, _ := AdoptionOf(main); !ok {
		t.Fatal("the worktree's unadopt withdrew the main checkout")
	}

	other := filepath.Join(t.TempDir(), "other")
	gitIn(t, root, "worktree", "add", "-q", other)
	otherIdentity, _ := RepoIdentity(other)
	if _, err := Unadopt(main); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := AdoptionOf(otherIdentity); ok {
		t.Fatal("a worktree stays adopted after its main checkout was unadopted")
	}
}

// Adoption refuses an oracle that passes without running a declared check.
func TestAdoptRefusesAnOracleThatSkipsADeclaredCheck(t *testing.T) {
	root, opts := adoptableRepo(t)
	writeFile(t, root, OracleName, "exit 0\n")
	res, err := Adopt(root, opts)
	if !errors.Is(err, ErrNotAdopted) || !res.Oracle.IntegrationFailure {
		t.Fatalf("err %v oracle %+v; want ErrNotAdopted for an integration failure", err, res.Oracle)
	}
	identity, _ := RepoIdentity(root)
	if _, ok, _ := AdoptionOf(identity); ok {
		t.Fatal("a refused adoption was recorded")
	}
}

// A declared check without a current receipt is replayed, and one that does
// not validate stops adoption before the oracle runs.
func TestAdoptReplaysAStaleCheckAndRefusesOneThatFails(t *testing.T) {
	root, opts := adoptableRepo(t)
	writeFile(t, root, ".steer/checks/no-count.sh", "exit 0\n")
	res, err := Adopt(root, opts)
	if !errors.Is(err, ErrNotAdopted) || res.Oracle.RunID != "" {
		t.Fatalf("err %v oracle run %q; want ErrNotAdopted before any oracle run", err, res.Oracle.RunID)
	}
	if len(res.Validations) != 1 || res.Validations[0].Reused || res.Validations[0].Run.Verdict != VerdictFailed {
		t.Fatalf("validations %+v; want a fresh failed replay", res.Validations)
	}
}
