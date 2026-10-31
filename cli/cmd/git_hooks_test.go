package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// runGitHooksInstall runs the install command from dir with default flags.
func runGitHooksInstall(t *testing.T, dir string) error {
	t.Helper()
	t.Chdir(dir)
	gitHooksInstallForce, gitHooksInstallIncludeGlobal = false, false
	return gitHooksInstallCmd.RunE(gitHooksInstallCmd, nil)
}

func TestGitHooksInstall_GitRunsTheInstalledHook(t *testing.T) {
	cases := []struct {
		name string
		// setup configures the repository and returns the directory to install from and
		// the directory the hook must land in.
		setup func(t *testing.T, repo string) (from, hooks string)
	}{
		{"plain", func(t *testing.T, repo string) (string, string) {
			return repo, filepath.Join(repo, ".git", "hooks")
		}},
		{"plain from a subdirectory", func(t *testing.T, repo string) (string, string) {
			return filepath.Join(repo, "a", "b"), filepath.Join(repo, ".git", "hooks")
		}},
		{"relative hooksPath", func(t *testing.T, repo string) (string, string) {
			gitIn(t, repo, "config", "core.hooksPath", ".githooks")
			return repo, filepath.Join(repo, ".githooks")
		}},
		{"relative hooksPath from a subdirectory", func(t *testing.T, repo string) (string, string) {
			gitIn(t, repo, "config", "core.hooksPath", ".githooks")
			return filepath.Join(repo, "a", "b"), filepath.Join(repo, ".githooks")
		}},
		{"absolute hooksPath outside the repository", func(t *testing.T, repo string) (string, string) {
			shared := filepath.Join(filepath.Dir(repo), "shared-hooks")
			gitIn(t, repo, "config", "core.hooksPath", shared)
			return repo, shared
		}},
		{"hooksPath ending in a space", func(t *testing.T, repo string) (string, string) {
			gitIn(t, repo, "config", "core.hooksPath", ".githooks ")
			return repo, filepath.Join(repo, ".githooks ")
		}},
		{"hooksPath ending in a newline", func(t *testing.T, repo string) (string, string) {
			gitIn(t, repo, "config", "core.hooksPath", ".githooks\n")
			return repo, filepath.Join(repo, ".githooks\n")
		}},
		{"git tracing to stderr", func(t *testing.T, repo string) (string, string) {
			t.Setenv("GIT_TRACE", "1")
			return repo, filepath.Join(repo, ".git", "hooks")
		}},
		{"linked worktree", func(t *testing.T, repo string) (string, string) {
			worktree := filepath.Join(filepath.Dir(repo), "linked")
			gitIn(t, repo, "worktree", "add", "-q", "-b", "linked", worktree)
			return worktree, filepath.Join(repo, ".git", "hooks")
		}},
		{"linked worktree with relative hooksPath", func(t *testing.T, repo string) (string, string) {
			worktree := filepath.Join(filepath.Dir(repo), "linked")
			gitIn(t, repo, "worktree", "add", "-q", "-b", "linked", worktree)
			gitIn(t, repo, "config", "core.hooksPath", ".githooks")
			return worktree, filepath.Join(worktree, ".githooks")
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := hooksScratchRepo(t)
			from, hooks := tc.setup(t, repo)
			if err := runGitHooksInstall(t, from); err != nil {
				t.Fatalf("install: %v", err)
			}
			raw, err := os.ReadFile(filepath.Join(hooks, "pre-commit"))
			if err != nil {
				t.Fatalf("hook is not in %s: %v", hooks, err)
			}
			if !strings.Contains(string(raw), steerPreCommitMarker) {
				t.Fatalf("hook lacks the steer marker:\n%s", raw)
			}

			// A stub steer records the call, which proves git itself ran the hook.
			stubDir := t.TempDir()
			ran := filepath.Join(stubDir, "ran")
			stub := "#!/bin/sh\necho \"$*\" > '" + ran + "'\n"
			if err := os.WriteFile(filepath.Join(stubDir, "steer"), []byte(stub), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", stubDir+string(os.PathListSeparator)+os.Getenv("PATH"))
			gitIn(t, from, "commit", "-q", "--allow-empty", "-m", "probe")
			got, err := os.ReadFile(ran)
			if err != nil {
				t.Fatalf("git did not run the hook: %v", err)
			}
			if !strings.Contains(string(got), "verify --staged --scope project") {
				t.Fatalf("hook ran %q", got)
			}
		})
	}
}

func TestGitHooksInstall_RefusesAForeignHookInASharedHooksPath(t *testing.T) {
	repo := hooksScratchRepo(t)
	shared := filepath.Join(filepath.Dir(repo), "shared-hooks")
	if err := os.MkdirAll(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	foreign := "#!/bin/sh\n# another tool's hook\n"
	path := filepath.Join(shared, "pre-commit")
	if err := os.WriteFile(path, []byte(foreign), 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "config", "core.hooksPath", shared)

	err := runGitHooksInstall(t, repo)
	if err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("install must refuse naming %s, got: %v", path, err)
	}
	raw, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(raw) != foreign {
		t.Fatalf("foreign hook was changed:\n%s", raw)
	}
}

func TestGitHooksInstall_RefusesInsideTheGitDirectory(t *testing.T) {
	repo := hooksScratchRepo(t)
	gitIn(t, repo, "config", "core.hooksPath", ".githooks")

	if err := runGitHooksInstall(t, filepath.Join(repo, ".git")); err == nil {
		t.Fatal("install must refuse outside a work tree")
	}
	if _, err := os.Stat(filepath.Join(repo, ".git", ".githooks")); !os.IsNotExist(err) {
		t.Fatalf("install created a hooks directory under .git: %v", err)
	}
}

func TestBuildSteerPreCommitScript_ProjectScopeDefault(t *testing.T) {
	script := buildSteerPreCommitScript(false)
	if !strings.Contains(script, steerPreCommitMarker) {
		t.Fatalf("expected marker %q in script", steerPreCommitMarker)
	}
	if !strings.Contains(script, "steer verify --staged --scope project") {
		t.Fatalf("expected project verify command in script")
	}
	if strings.Contains(script, "--include-global") {
		t.Fatalf("did not expect --include-global in default script")
	}
}

func TestBuildSteerPreCommitScript_WithGlobal(t *testing.T) {
	script := buildSteerPreCommitScript(true)
	if !strings.Contains(script, "steer verify --staged --scope project --include-global") {
		t.Fatalf("expected include-global verify command in script")
	}
}
