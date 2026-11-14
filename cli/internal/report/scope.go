package report

import (
	"fmt"
	"os"
	"time"

	"github.com/steer-sh/steer-cli/internal/checks"
	"github.com/steer-sh/steer-cli/internal/transcript"
)

// Scope selects which sessions a transcript scan reads. Every scan builds its
// filter here, so a digest and the session corpus never select differently.
type Scope int

const (
	// ScopeAll reads every project on this machine. It is the default: the
	// strongest fix candidates are habits that repeat across projects.
	ScopeAll Scope = iota
	// ScopeProject reads the sessions a harness recorded under the working
	// directory itself, and no other directory of the same repository.
	ScopeProject
	// ScopeRepo reads the sessions launched anywhere inside the working
	// directory's checkout. A sibling worktree, a nested repository and a
	// submodule each have a root of their own and stay out.
	ScopeRepo
)

// ScopeOf maps the two command flags to a scope. Both flags set at once is the
// caller's error to refuse; this returns the narrower scope.
func ScopeOf(repoOnly, projectOnly bool) Scope {
	switch {
	case repoOnly:
		return ScopeRepo
	case projectOnly:
		return ScopeProject
	default:
		return ScopeAll
	}
}

// RootResolver returns a function that maps a launch directory to its canonical
// checkout root, and "" when the directory is in no repository. It caches per
// directory, because a scan asks for the same directory once per session.
//
// The root passes through RepoIdentity, the same canonical form the check tier
// stores, so a symlinked alias of a checkout resolves to the checkout.
func RootResolver() func(string) string {
	cache := map[string]string{}
	return func(cwd string) string {
		if cwd == "" {
			return ""
		}
		if root, ok := cache[cwd]; ok {
			return root
		}
		root, err := checks.RepoRootOf(cwd)
		if err == nil {
			root, err = checks.RepoIdentity(root)
		}
		if err != nil {
			root = ""
		}
		cache[cwd] = root
		return root
	}
}

// Filter builds the transcript filter for this scope over its own resolver.
func (s Scope) Filter(since time.Time) (transcript.Filter, error) {
	return s.FilterWith(since, RootResolver())
}

// FilterWith builds the filter over a resolver the caller owns, so a scan that
// also resolves roots of its own shares one cache. ScopeRepo fails outside a
// repository: an unresolved root would select nothing, which reads as an empty
// machine rather than as the wrong question.
func (s Scope) FilterWith(since time.Time, resolve func(string) string) (transcript.Filter, error) {
	f := transcript.Filter{Since: since}
	switch s {
	case ScopeAll:
		f.AllProjects = true
		return f, nil
	case ScopeProject:
		paths, err := ProjectPaths()
		if err != nil {
			return f, err
		}
		f.Projects = paths
		return f, nil
	case ScopeRepo:
		cwd, err := os.Getwd()
		if err != nil {
			return f, err
		}
		root := resolve(cwd)
		if root == "" {
			return f, fmt.Errorf("the working directory is in no git repository: %s", cwd)
		}
		f.RepoRoot = root
		f.ResolveRoot = resolve
		return f, nil
	}
	return f, fmt.Errorf("unknown scope %d", int(s))
}
