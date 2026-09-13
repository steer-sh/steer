package mine

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/gate"
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
	t.Setenv("HOME", home)
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
	t.Setenv("HOME", home)
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
	t.Setenv("HOME", home)
	t.Chdir(project)
	writeAddendumStore(t, filepath.Join(home, ".steer", "hooks"), "no-style", "the global wording")
	writeAddendumStore(t, filepath.Join(project, ".steer", "hooks"), "no-style", "the project wording")
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
