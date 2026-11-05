package gate

import (
	"os"
	"path/filepath"
	"testing"
)

// stubGit puts a git on PATH that answers `rev-parse --show-toplevel` with answer and
// fails every other subcommand. It fixes what git reports, so the assertion is about
// what Toplevel does with that answer rather than about git's own resolution policy.
func stubGit(t *testing.T, answer string) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\nfor a in \"$@\"; do\n  if [ \"$a\" = \"--show-toplevel\" ]; then\n    echo " + answer + "\n    exit 0\n  fi\ndone\nexit 1\n"
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil {
		t.Fatalf("write stub git: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// aliasedRoot makes a directory, a symlink that names it and a subdirectory under it.
// The subdirectory is the call argument and the alias is git's answer, so canonicalizing
// the argument instead of the answer reaches neither expected value.
func aliasedRoot(t *testing.T) (alias, canonical, sub string) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve temp dir: %v", err)
	}
	canonical = filepath.Join(base, "real")
	sub = filepath.Join(canonical, "pkg")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("make repo dirs: %v", err)
	}
	alias = filepath.Join(base, "alias")
	if err := os.Symlink(canonical, alias); err != nil {
		t.Fatalf("make alias: %v", err)
	}
	return alias, canonical, sub
}

func TestToplevelResolvesGitsAnswer(t *testing.T) {
	alias, canonical, sub := aliasedRoot(t)
	stubGit(t, alias)

	if got := Toplevel(sub); got != canonical {
		t.Errorf("Toplevel(%q) = %q, want the canonical form %q of git's answer %q",
			sub, got, canonical, alias)
	}
}
