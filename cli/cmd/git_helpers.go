package cmd

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

func runGit(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(output))
		if msg == "" {
			return "", fmt.Errorf("git %s failed: %w", strings.Join(args, " "), err)
		}
		return "", fmt.Errorf("git %s failed: %s", strings.Join(args, " "), msg)
	}
	return string(output), nil
}

func gitTopLevel() (string, error) {
	out, err := runGit("rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("not inside a git repository: %w", err)
	}
	return strings.TrimSpace(out), nil
}

func gitChangedFiles(args ...string) ([]string, error) {
	out, err := runGit(args...)
	if err != nil {
		return nil, err
	}

	seen := map[string]struct{}{}
	paths := make([]string, 0)
	for _, line := range strings.Split(out, "\n") {
		path := strings.TrimSpace(line)
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}

	sort.Strings(paths)
	return paths, nil
}

// gitHooksDir is the hooks directory git itself would run, created if absent.
//
// It is resolved with `rev-parse --git-path` rather than joined onto the work tree,
// because a linked worktree and a submodule carry a `.git` FILE: joining `.git/hooks`
// onto the toplevel there fails with ENOTDIR, and core.hooksPath moves the directory
// out of `.git` altogether.
func gitHooksDir() (string, error) {
	out, err := runGit("rev-parse", "--git-path", "hooks")
	if err != nil {
		return "", err
	}
	dir := strings.TrimSpace(out)
	if dir == "" {
		return "", fmt.Errorf("git could not resolve the hooks directory")
	}
	// A relative answer is relative to the CURRENT directory, not to the work-tree
	// root: from cli/ the same repository answers `../.git/hooks`. Joining it onto the
	// toplevel would climb out of the repository once per level of nesting.
	if !filepath.IsAbs(dir) {
		abs, err := filepath.Abs(dir)
		if err != nil {
			return "", err
		}
		dir = abs
	}
	return dir, nil
}
