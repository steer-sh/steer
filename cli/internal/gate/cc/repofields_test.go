package cc

import (
	"os"
	"path/filepath"
	"testing"
)

// stubGit puts a git on PATH that answers `rev-parse --show-toplevel` with answer and
// fails every other subcommand. It fixes what git reports, so the assertion is about
// what RepoFields stamps rather than about git's own resolution policy.
func stubGit(t *testing.T, answer string) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\nfor a in \"$@\"; do\n  if [ \"$a\" = \"--show-toplevel\" ]; then\n    echo " + answer + "\n    exit 0\n  fi\ndone\nexit 1\n"
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil {
		t.Fatalf("write stub git: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestRepoFieldsStampsTheCanonicalRoot(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve temp dir: %v", err)
	}
	canonical := filepath.Join(base, "real")
	sub := filepath.Join(canonical, "pkg")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("make repo dirs: %v", err)
	}
	alias := filepath.Join(base, "alias")
	if err := os.Symlink(canonical, alias); err != nil {
		t.Fatalf("make alias: %v", err)
	}
	stubGit(t, alias)

	// The cwd is the subdirectory, so canonicalizing the argument instead of git's
	// answer stamps the subdirectory and fails.
	fields := RepoFields(sub)
	if got := fields["repo"]; got != canonical {
		t.Errorf("RepoFields(%q)[\"repo\"] = %v, want the canonical form %q of git's answer %q",
			sub, got, canonical, alias)
	}
}

// A toplevel that trims to nothing names no repository, so the row carries no repo at
// all rather than a canonicalized empty path, which resolves to the process cwd.
func TestRepoFieldsOmitsRepoWhenGitNamesAnEmptyToplevel(t *testing.T) {
	dir := t.TempDir()
	stubGit(t, "")

	fields := RepoFields(dir)
	if _, ok := fields["repo"]; ok {
		t.Errorf("RepoFields stamped repo = %v where git named an empty toplevel", fields["repo"])
	}
}
