package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/steer-sh/steer-cli/internal/testhome"
)

// gitIn runs git in dir with an author identity and returns its output.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t.invalid"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return string(out)
}

// hooksScratchRepo isolates git's environment and returns a new repository with a
// directory a/b inside it.
func hooksScratchRepo(t *testing.T) string {
	t.Helper()
	testhome.Set(t, t.TempDir())
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for _, name := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE"} {
		t.Setenv(name, "")
		os.Unsetenv(name)
	}
	// macOS temp paths are symlinks; git reports the resolved form.
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(base, "repo")
	if err := os.MkdirAll(filepath.Join(repo, "a", "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "init", "-q")
	gitIn(t, repo, "commit", "-q", "--allow-empty", "-m", "base")
	return repo
}

// linkedHooksPath points core.hooksPath at link/../hooks, where link is a symlink to
// real/sub: git reaches real/hooks, and a lexical join reaches <repo>/hooks.
func linkedHooksPath(t *testing.T, repo string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(repo, "real", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("real", "sub"), filepath.Join(repo, "link")); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "config", "core.hooksPath", "link/../hooks")
}

// pins existing behaviour
func TestGitHooksDir_FollowsTheSymlinkGitFollows(t *testing.T) {
	repo := hooksScratchRepo(t)
	linkedHooksPath(t, repo)
	t.Chdir(repo)
	dir, err := gitHooksDir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(repo, "real", "hooks"); dir != want {
		t.Fatalf("gitHooksDir() = %q, want %q, where git runs hooks", dir, want)
	}
}

func TestGitHooksDir_RefusesClimbingOutOfAMissingDirectory(t *testing.T) {
	repo := hooksScratchRepo(t)
	gitIn(t, repo, "config", "core.hooksPath", "missing/../hooks")
	t.Chdir(repo)
	if dir, err := gitHooksDir(); err == nil {
		t.Fatalf("gitHooksDir() = %q; git cannot walk through a missing directory", dir)
	}
}

func TestGitHooksDir_RefusesOutsideTheWorkTreeItNames(t *testing.T) {
	repo := hooksScratchRepo(t)
	gitIn(t, repo, "config", "core.hooksPath", ".githooks")
	t.Setenv("GIT_DIR", filepath.Join(repo, ".git"))
	t.Setenv("GIT_WORK_TREE", repo)
	t.Chdir(t.TempDir())
	if dir, err := gitHooksDir(); err == nil {
		t.Fatalf("gitHooksDir() = %q from outside the work tree", dir)
	}
}
