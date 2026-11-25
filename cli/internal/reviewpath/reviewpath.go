// Package reviewpath reads the path patterns of a reviewed rule. A pattern is a
// repository-relative slash path in one of four forms: `dir/` (every path under dir),
// `*.ext` (every path whose name ends in .ext), `dir/*.ext` (both), or one exact path.
package reviewpath

import (
	"fmt"
	"strings"
)

// Validate refuses a pattern outside the grammar, saying which form it broke.
func Validate(pattern string) error {
	p := strings.TrimSpace(pattern)
	switch {
	case p == "":
		return fmt.Errorf("a review path cannot be empty")
	case strings.Contains(p, `\`):
		return fmt.Errorf("review path %q: use / as the separator", pattern)
	case strings.HasPrefix(p, "/"):
		return fmt.Errorf("review path %q: name a path relative to the repository root", pattern)
	}
	dir, name := split(p)
	if dir != "" {
		for _, part := range strings.Split(strings.TrimSuffix(dir, "/"), "/") {
			if part == "" || part == "." || part == ".." {
				return fmt.Errorf("review path %q: a directory part is empty, . or ..", pattern)
			}
			if strings.Contains(part, "*") {
				return fmt.Errorf("review path %q: only the file name may hold *, as *.<ext>", pattern)
			}
		}
	}
	if strings.Contains(name, "*") {
		ext := strings.TrimPrefix(name, "*.")
		if ext == name || ext == "" || strings.Contains(ext, "*") {
			return fmt.Errorf("review path %q: a file-name pattern is *.<ext>, such as *.go", pattern)
		}
	}
	if name == "." || name == ".." {
		return fmt.Errorf("review path %q: a directory part is empty, . or ..", pattern)
	}
	return nil
}

// Match reports whether a repository-relative slash path falls under a pattern. An
// invalid pattern matches nothing.
func Match(pattern, path string) bool {
	p := strings.TrimSpace(pattern)
	if Validate(p) != nil {
		return false
	}
	dir, name := split(p)
	if !strings.HasPrefix(path, dir) {
		return false
	}
	rest := path[len(dir):]
	switch {
	case name == "":
		return rest != ""
	case strings.HasPrefix(name, "*."):
		suffix := name[1:]
		return strings.HasSuffix(rest, suffix) && !strings.HasSuffix(rest, "/"+suffix) && rest != suffix
	default:
		return rest == name
	}
}

// MatchAny reports whether any changed path falls under any pattern.
func MatchAny(patterns, paths []string) bool {
	for _, pattern := range patterns {
		for _, path := range paths {
			if Match(pattern, path) {
				return true
			}
		}
	}
	return false
}

// split cuts a pattern into its directory, ending in "/" or empty, and its file name,
// empty for a directory pattern.
func split(p string) (dir, name string) {
	i := strings.LastIndex(p, "/")
	return p[:i+1], p[i+1:]
}
