package cmd

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

func runGit(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	// Stderr stays out of the result: GIT_TRACE and warnings write there on success.
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return "", fmt.Errorf("git %s failed: %w", strings.Join(args, " "), err)
		}
		return "", fmt.Errorf("git %s failed: %s", strings.Join(args, " "), msg)
	}
	return stdout.String(), nil
}

func gitTopLevel() (string, error) {
	out, err := runGit("rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("not inside a git repository: %w", err)
	}
	return strings.TrimSpace(out), nil
}

// gitChangedFiles runs a git name list and returns its paths verbatim, sorted and
// without duplicates. args must include -z and --name-only, which names a rename by
// its destination alone. A path that is not valid UTF-8 is an error: the guard
// payload is JSON, which would hand a guard a different name.
func gitChangedFiles(args ...string) ([]string, error) {
	out, err := runGit(args...)
	if err != nil {
		return nil, err
	}

	seen := map[string]struct{}{}
	paths := make([]string, 0)
	for _, path := range strings.Split(out, "\x00") {
		if path == "" {
			continue
		}
		if !utf8.ValidString(path) {
			return nil, fmt.Errorf("cannot verify path %q: it is not valid UTF-8", path)
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

// gitHooksDir is the hooks directory git runs for the current work tree. It may not
// exist yet, and it refuses outside a work tree.
//
// It is resolved with `rev-parse --git-path` rather than joined onto the work tree,
// because a linked worktree and a submodule carry a `.git` FILE and core.hooksPath
// moves the directory out of `.git` altogether. Git runs a pre-commit hook from the
// work-tree root and resolves a relative core.hooksPath there. From a work-tree
// subdirectory git answers relative to the current directory with that root
// accounted for; outside the work tree, inside `.git` included, it answers relative
// to the git directory, so that is refused.
func gitHooksDir() (string, error) {
	// --show-toplevel is no test here: with GIT_WORK_TREE set it answers from anywhere.
	inside, err := runGit("rev-parse", "--is-inside-work-tree")
	if err != nil {
		return "", fmt.Errorf("not inside a git repository: %w", err)
	}
	if strings.TrimSpace(inside) != "true" {
		return "", fmt.Errorf("run this from the repository's work tree, where git runs its hooks")
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	out, err := runGit("rev-parse", "--git-path", "hooks")
	if err != nil {
		return "", err
	}
	// Only the final newline is git's: the rest is the directory's name, whitespace included.
	dir := strings.TrimSuffix(out, "\n")
	if dir == "" {
		return "", fmt.Errorf("git could not resolve the hooks directory")
	}
	return resolvePath(cwd, dir)
}

// resolvePath joins p onto base the way the filesystem walks it: a ".." after a
// symbolic link climbs from the link's target, where filepath.Join would drop the
// link and its ".." together. Components that no ".." follows keep the names p
// gives them. A ".." after a component that is not an existing directory is an
// error, because the filesystem cannot walk through it either.
func resolvePath(base, p string) (string, error) {
	p = filepath.FromSlash(p)
	cur := base
	if filepath.IsAbs(p) {
		vol := filepath.VolumeName(p)
		cur = vol + string(filepath.Separator)
		p = p[len(vol):]
	}
	for _, part := range strings.Split(p, string(filepath.Separator)) {
		switch part {
		case "", ".":
		case "..":
			real, err := filepath.EvalSymlinks(cur)
			if err == nil {
				var info os.FileInfo
				if info, err = os.Stat(real); err == nil && !info.IsDir() {
					err = fmt.Errorf("not a directory")
				}
			}
			if err != nil {
				return "", fmt.Errorf("cannot resolve %q: %s: %w", p, cur, err)
			}
			cur = filepath.Dir(real)
		default:
			cur = filepath.Join(cur, part)
		}
	}
	return cur, nil
}
