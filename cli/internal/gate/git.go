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
func Toplevel(dir string) string {
	if dir == "" {
		dir = "."
	}
	if out, ok := Git(dir, gitShort, "rev-parse", "--show-toplevel"); ok {
		if top := strings.TrimSpace(out); top != "" {
			return top
		}
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return dir
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

func fileSize(path string) (int64, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, false
	}
	return info.Size(), true
}
