package report

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/steer-sh/steer-cli/internal/transcript"
)

func initRepo(t *testing.T) (dir, resolved string) {
	t.Helper()
	dir = t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "init").CombinedOutput(); err != nil {
		t.Skipf("git init unavailable: %s", out)
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return dir, resolved
}

// A launch directory resolves to the canonical checkout root, the same form the
// check tier stores, so a symlinked alias and a subdirectory agree.
func TestRootResolverCanonicalizes(t *testing.T) {
	repo, resolved := initRepo(t)
	sub := filepath.Join(repo, "cli")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(repo, alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	resolve := RootResolver()
	for _, dir := range []string{repo, sub, alias} {
		if got := resolve(dir); got != resolved {
			t.Errorf("resolve(%s) = %q; want %q", dir, got, resolved)
		}
	}
	if got := resolve(""); got != "" {
		t.Errorf("resolve(no directory) = %q; want empty", got)
	}
	if got := resolve(t.TempDir()); got != "" {
		t.Errorf("resolve(outside a repository) = %q; want empty", got)
	}
}

// A repository scope carries the resolved root and the resolver that matches a
// session against it.
func TestRepositoryScopeFilterCarriesTheResolvedRoot(t *testing.T) {
	repo, resolved := initRepo(t)
	sub := filepath.Join(repo, "cli")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(sub)

	f, err := ScopeRepo.Filter(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if f.RepoRoot != resolved {
		t.Errorf("RepoRoot = %q; want %q", f.RepoRoot, resolved)
	}
	if !f.ScopesRepo() {
		t.Error("a repository scope must report ScopesRepo")
	}
	keep := f.Selector()
	if !keep(transcript.Session{Cwd: sub}) {
		t.Error("a session launched in a subdirectory of the checkout must be kept")
	}
	if keep(transcript.Session{Cwd: t.TempDir()}) {
		t.Error("a session launched outside the checkout must be dropped")
	}
	if keep(transcript.Session{}) {
		t.Error("a session with no launch directory must be dropped")
	}
}

// A repository scope outside a repository is an error: an unresolved root would
// select nothing, which reads as an empty machine.
func TestRepositoryScopeOutsideARepositoryFails(t *testing.T) {
	t.Chdir(t.TempDir())
	if _, err := ScopeRepo.Filter(time.Now()); err == nil {
		t.Fatal("a repository scope outside a repository must fail")
	}
}

// The scopes are exclusive, and the narrower one wins.
func TestScopeOf(t *testing.T) {
	cases := []struct {
		repoOnly, projectOnly bool
		want                  Scope
	}{
		{false, false, ScopeAll},
		{false, true, ScopeProject},
		{true, false, ScopeRepo},
		{true, true, ScopeRepo},
	}
	for _, c := range cases {
		if got := ScopeOf(c.repoOnly, c.projectOnly); got != c.want {
			t.Errorf("ScopeOf(%v, %v) = %v; want %v", c.repoOnly, c.projectOnly, got, c.want)
		}
	}
}

// A project scope reads the working directory and its resolved alias, and keeps
// no other directory of the same repository.
func TestProjectScopeFilterKeepsOnlyTheWorkingDirectory(t *testing.T) {
	repo, _ := initRepo(t)
	sub := filepath.Join(repo, "cli")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(sub)

	f, err := ScopeProject.Filter(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	keep := f.Selector()
	if !keep(transcript.Session{Project: sub}) {
		t.Error("a session of the working directory must be kept")
	}
	if keep(transcript.Session{Project: repo}) {
		t.Error("a project scope must not reach the repository root")
	}
}
