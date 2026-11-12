package cmd

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// initRepoAt makes dir a git repository with no commits.
func initRepoAt(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init %s: %v\n%s", dir, err, out)
	}
	physical, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return physical
}

// runRoot runs the root command with args, as the binary would, and returns its
// stdout. The working directory and the flag values the run set are put back
// afterwards, as a new process would start.
func runRoot(t *testing.T, args ...string) (string, error) {
	t.Helper()
	t.Cleanup(func() { rootCmd.SetArgs(nil) })
	start, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	pwd := os.Getenv("PWD")
	defer func() {
		_ = os.Chdir(start)
		_ = os.Setenv("PWD", pwd)
		workDir = ""
		resetFlags(rootCmd)
	}()
	rootCmd.SetArgs(args)
	out := captureStdout(t, func() { err = Execute() })
	return out, err
}

func resetFlags(c *cobra.Command) {
	reset := func(f *pflag.Flag) {
		if f.Changed {
			_ = f.Value.Set(f.DefValue)
			if s, ok := f.Value.(pflag.SliceValue); ok {
				_ = s.Replace(nil)
			}
			f.Changed = false
		}
	}
	c.PersistentFlags().VisitAll(reset)
	c.Flags().VisitAll(reset)
	for _, sub := range c.Commands() {
		resetFlags(sub)
	}
}

// A project-scope command run with -C from one repository reads and writes the
// named repository's store, instruction document and check tier, and leaves the
// repository it started in untouched.
func TestDirectoryFlagRunsProjectCommandsInTheNamedRepository(t *testing.T) {
	home := fakeHomes(t)
	t.Setenv("PWD", os.Getenv("PWD"))
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repoA := initRepoAt(t, cwd)
	repoB := initRepoAt(t, filepath.Join(t.TempDir(), "b"))

	if _, err := runRoot(t, "-C", repoB, "rules", "add", "plain-words", "--rule", "write plain words | not jargon"); err != nil {
		t.Fatalf("rules add: %v", err)
	}
	if _, err := runRoot(t, "-C", repoB, "check", "init"); err != nil {
		t.Fatalf("check init: %v", err)
	}

	manifest, err := os.ReadFile(filepath.Join(repoB, ".steer", "hooks", "steer-manifest.json"))
	if err != nil || !strings.Contains(string(manifest), "plain-words") {
		t.Errorf("B's manifest does not hold the rule (%v):\n%s", err, manifest)
	}
	doc, err := os.ReadFile(filepath.Join(repoB, "CLAUDE.md"))
	if err != nil || !strings.Contains(string(doc), "write plain words") {
		t.Errorf("B's CLAUDE.md does not carry the rule (%v):\n%s", err, doc)
	}
	if _, err := os.Stat(filepath.Join(repoB, "check.sh")); err != nil {
		t.Errorf("check init wrote no check.sh in B: %v", err)
	}
	if entries, err := os.ReadDir(repoA); err != nil || len(entries) != 1 || entries[0].Name() != ".git" {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("A holds %v (%v); want only .git", names, err)
	}

	out, err := runRoot(t, "-C", repoB, "status", "--json")
	if err != nil {
		t.Fatalf("status --json: %v", err)
	}
	var status struct {
		Checks struct {
			Repo     string `json:"repo"`
			Manifest bool   `json:"manifest"`
		} `json:"checks"`
		Rules []struct {
			ID    string `json:"id"`
			Scope string `json:"scope"`
		} `json:"rules"`
	}
	if err := json.Unmarshal([]byte(out), &status); err != nil {
		t.Fatalf("status --json: %v\n%s", err, out)
	}
	if status.Checks.Repo != repoB || !status.Checks.Manifest {
		t.Errorf("status checks = %+v, want B with its manifest", status.Checks)
	}
	if !slices.ContainsFunc(status.Rules, func(r struct {
		ID    string `json:"id"`
		Scope string `json:"scope"`
	}) bool {
		return r.ID == "plain-words" && r.Scope == "project"
	}) {
		t.Errorf("status rules = %+v, want B's project rule", status.Rules)
	}

	out, err = runRoot(t, "-C", repoB, "uncommitted", "--json")
	if err != nil {
		t.Fatalf("uncommitted: %v", err)
	}
	var changed uncommittedReport
	if err := json.Unmarshal([]byte(out), &changed); err != nil {
		t.Fatalf("uncommitted --json: %v\n%s", err, out)
	}
	for _, want := range []string{"CLAUDE.md", "check.sh", ".steer/hooks/steer-manifest.json"} {
		if changed.Repo != repoB || !slices.Contains(changed.Paths, want) {
			t.Errorf("uncommitted in B = %+v, want %s", changed, want)
		}
	}
	out, err = runRoot(t, "uncommitted", "--json")
	if err != nil {
		t.Fatalf("uncommitted in A: %v", err)
	}
	if err := json.Unmarshal([]byte(out), &changed); err != nil || changed.Repo != repoA || len(changed.Paths) != 0 {
		t.Errorf("uncommitted in A = %+v (%v), want none", changed, err)
	}
}

// -C refuses a path that is missing or is not a directory, before the command runs.
func TestDirectoryFlagRefusesAPathThatIsNoDirectory(t *testing.T) {
	fakeHomes(t)
	t.Setenv("PWD", os.Getenv("PWD"))
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{
		filepath.Join(t.TempDir(), "missing"): "no such directory",
		file:                                  "not a directory",
	} {
		if _, err := runRoot(t, "-C", path, "uncommitted"); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("-C %s: err = %v, want %q", path, err, want)
		}
	}
}

// uncommitted names only the files Steer writes, and fails outside a repository.
func TestUncommittedListsOnlySteerFiles(t *testing.T) {
	fakeHomes(t)
	repo := initRepoAt(t, filepath.Join(t.TempDir(), "r"))
	for _, rel := range []string{"AGENTS.md", ".codex/hooks.json", ".claude/settings.json", ".claude/commands/x.md", "src/main.go", ".steer/checks/cases/a.json"} {
		p := filepath.Join(repo, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := steerOwnedChanges(repo)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{".claude/settings.json", ".codex/hooks.json", ".steer/checks/cases/a.json", "AGENTS.md"}
	if !slices.Equal(got.Paths, want) {
		t.Errorf("paths = %v, want %v", got.Paths, want)
	}
	if _, err := steerOwnedChanges(t.TempDir()); err == nil {
		t.Error("a directory outside any repository was read as a checkout")
	}
}
