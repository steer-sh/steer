package status

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/claude"
)

// TestARefusedStoreIsNotAnEmptyStore pins the reading a fail-soft reader cannot
// give: a manifest the reader refuses and a machine with no guards produce the
// same empty inventory, while the refused store's guards still run.
func TestARefusedStoreIsNotAnEmptyStore(t *testing.T) {
	dir := t.TempDir()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })

	if err := claude.EnsureDirectories(claude.ProjectScope); err != nil {
		t.Fatal(err)
	}
	manifestPath, err := claude.GetManifestFile(claude.ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, []byte(`{"hooks":{"x":{"file":"../../victim"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	hooks, problems := collectHooks(claude.ProjectScope)
	if len(hooks) != 0 {
		t.Fatalf("a refused store listed %d guards", len(hooks))
	}
	if len(problems) != 1 || problems[0].Scope != claude.ProjectScope {
		t.Fatalf("store problems = %+v", problems)
	}
	if !strings.Contains(problems[0].Error, filepath.Base(manifestPath)) {
		t.Fatalf("the problem does not name the file to edit: %q", problems[0].Error)
	}

	if _, ruleProblems := collectRules(); len(ruleProblems) == 0 {
		t.Fatal("the rule reader reported a refused store as a store with no rules")
	}
}
