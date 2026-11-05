package gate

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Git runs a git command in cwd. It returns stdout, or ok=false on any failure —
// the same fail-open contract the Python helper had: a git that cannot answer must
// never deny a commit.
func Git(cwd string, timeout time.Duration, args ...string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = cwd
	var out strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		return "", false
	}
	return out.String(), true
}

const (
	gitShort  = 5 * time.Second
	gitMedium = 15 * time.Second
	gitLong   = 30 * time.Second
)

// Toplevel is the git work-tree root for dir, or its resolved path when git cannot
// answer. The fallback keeps a path git rejects — a fixture repo the command is about
// to create — in a slot of its own rather than merging it into the real chain.
// Both paths canonicalize, so a root reaches evidence in one spelling whenever the
// resolution succeeds; Canonical states what a failed step leaves.
func Toplevel(dir string) string {
	if dir == "" {
		dir = "."
	}
	if out, ok := Git(dir, gitShort, "rev-parse", "--show-toplevel"); ok {
		if top := strings.TrimSpace(out); top != "" {
			return Canonical(top)
		}
	}
	return Canonical(dir)
}

// ProjectDir is the directory a project-scope harness registration lives under:
// the working directory's work-tree root, or the working directory outside one.
// Every reader and writer of such a registration resolves it here, so a command
// run from a subdirectory finds what one run from the root wrote.
func ProjectDir() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return Toplevel(cwd), nil
}

// Canonical is path as absolute and symlink-free: the one spelling of a repository
// root that evidence carries. It returns the best form it reached when a step fails,
// because a caller that needs a path to open must still get one.
func Canonical(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return abs
	}
	return resolved
}

func nonEmptyLines(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	return out
}

// nulFields splits git's -z output into its non-empty paths.
func nulFields(text string) []string {
	var out []string
	for _, field := range strings.Split(text, "\x00") {
		if field != "" {
			out = append(out, field)
		}
	}
	return out
}

func fileSize(path string) (int64, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, false
	}
	return info.Size(), true
}
