package mine

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/gate"
	"github.com/steer-sh/steer-cli/internal/testhome"
)

func writeAddendumStore(t *testing.T, dir, id, text string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"hooks":{},"review_addendum":{"` + id + `":{"text":"` + text + `","text_hash":"stale"}}}`
	if err := os.WriteFile(filepath.Join(dir, "steer-manifest.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
}

const gitCommitVerb = "co" + "mmit"

// commitProject makes dir a repository whose HEAD holds everything in it.
func commitProject(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}, {gitCommitVerb, "-qm", "init"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s failed: %v: %s", strings.Join(args, " "), err, out)
		}
	}
}

// registerAddendumFixture is a ledger whose reviewer raised a rejected finding twelve times.
func registerAddendumFixture(t *testing.T) (stateDir, ledger string) {
	t.Helper()
	var rows []gate.Row
	ts := 1000
	for i := 0; i < 12; i++ {
		rows = append(rows, row(ts, "deny"),
			concerns(row(ts+1, "retry"), "round", cv("style nit on widget names", "REJECTED")))
		ts += 100
	}
	return writeLedger(t, rows)
}

func addendumOpts(ledger string) RegisterOpts {
	return RegisterOpts{
		Prose: testProse, ScopeAll: true, Ledger: ledger,
		Class: "widget style nit", Match: "style nit", Kind: "false-positive",
		Tier: "review", Target: "steer-manifest.json", AddendumID: "no-style",
	}
}

func TestAReviewTierBindsItsAddendumEntry(t *testing.T) {
	home := t.TempDir()
	testhome.Set(t, home)
	t.Chdir(t.TempDir())
	writeAddendumStore(t, filepath.Join(home, ".steer", "hooks"), "no-style", "style is not a finding")
	stateDir, ledger := registerAddendumFixture(t)

	if err := Register(io.Discard, stateDir, addendumOpts(ledger)); err != nil {
		t.Fatal(err)
	}
	recorded, _ := Load(stateDir)
	check(t, "the id is stored", str(recorded[0]["addendum_id"]), "no-style")
	check(t, "the text is frozen", str(recorded[0]["addendum"]), "style is not a finding")
	check(t, "the scope is frozen", str(recorded[0]["addendum_scope"]), "global")
	if h := str(recorded[0]["addendum_hash"]); h != claude.HashRule("style is not a finding") {
		t.Fatalf("addendum_hash = %q, want the store's own digest recomputed over the text", h)
	}
}

func TestAReviewTierNeedsAnAddendumIDAndAFalsePositiveClass(t *testing.T) {
	home := t.TempDir()
	testhome.Set(t, home)
	t.Chdir(t.TempDir())
	writeAddendumStore(t, filepath.Join(home, ".steer", "hooks"), "no-style", "style is not a finding")
	stateDir, ledger := registerAddendumFixture(t)

	noID := addendumOpts(ledger)
	noID.AddendumID = ""
	if err := Register(io.Discard, stateDir, noID); err == nil || !strings.Contains(err.Error(), "--addendum-id") {
		t.Fatalf("err = %v", err)
	}
	defect := addendumOpts(ledger)
	defect.Kind = "defect"
	defect.Force = true
	if err := Register(io.Discard, stateDir, defect); err == nil || !strings.Contains(err.Error(), "false-positive") {
		t.Fatalf("err = %v", err)
	}
	unknown := addendumOpts(ledger)
	unknown.AddendumID = "no-such-entry"
	if err := Register(io.Discard, stateDir, unknown); err == nil || !strings.Contains(err.Error(), "no review addendum entry") {
		t.Fatalf("err = %v", err)
	}
}

func TestAReviewTierRefusesAnAddendumIDInBothStores(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	testhome.Set(t, home)
	t.Chdir(project)
	writeAddendumStore(t, filepath.Join(home, ".steer", "hooks"), "no-style", "the global wording")
	writeAddendumStore(t, filepath.Join(project, ".steer", "hooks"), "no-style", "the project wording")
	commitProject(t, project)
	stateDir, ledger := registerAddendumFixture(t)

	if err := Register(io.Discard, stateDir, addendumOpts(ledger)); err == nil ||
		!strings.Contains(err.Error(), "--addendum-scope") {
		t.Fatalf("err = %v", err)
	}
	scoped := addendumOpts(ledger)
	scoped.AddendumScope = "project"
	if err := Register(io.Discard, stateDir, scoped); err != nil {
		t.Fatal(err)
	}
	recorded, _ := Load(stateDir)
	check(t, "the named store answers", str(recorded[0]["addendum"]), "the project wording")
}

// The gate reads project entries from HEAD, so a record binds a project entry only
// once the text it freezes is the text the reviewer gets.
func TestAReviewTierRefusesAProjectEntryTheGateDoesNotReadYet(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	testhome.Set(t, home)
	t.Chdir(project)
	store := filepath.Join(project, ".steer", "hooks")
	writeAddendumStore(t, store, "no-style", "style is not a finding")
	stateDir, ledger := registerAddendumFixture(t)

	if err := Register(io.Discard, stateDir, addendumOpts(ledger)); err == nil ||
		!strings.Contains(err.Error(), "not at HEAD") {
		t.Fatalf("an entry only on disk: err = %v", err)
	}
	commitProject(t, project)
	writeAddendumStore(t, store, "no-style", "style is never a finding")
	if err := Register(io.Discard, stateDir, addendumOpts(ledger)); err == nil ||
		!strings.Contains(err.Error(), "differs from HEAD") {
		t.Fatalf("a reworded entry: err = %v", err)
	}
	writeAddendumStore(t, store, "no-style", "style  is not\\n a finding")
	if err := Register(io.Discard, stateDir, addendumOpts(ledger)); err != nil {
		t.Fatalf("an entry whose normalized text is at HEAD: %v", err)
	}
	recorded, _ := Load(stateDir)
	check(t, "the scope is frozen", str(recorded[0]["addendum_scope"]), "project")
}
