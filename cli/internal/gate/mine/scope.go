package mine

import (
	"fmt"
	"regexp"
	"strings"
)

// ScopeVersion is the only scope schema this build reads or writes.
const ScopeVersion = 1

// Scope is a record's frozen eligibility rule over repo and path metadata. All is
// exclusive with Repo/Path; both predicates present combine with AND.
type Scope struct {
	All  bool
	Repo *regexp.Regexp
	Path *regexp.Regexp
	raw  map[string]any
}

// Eligibility is one chain's standing against a scope. Unknown means the chain's
// metadata is missing, so it withholds rather than shrinking the window.
type Eligibility int

const (
	Eligible Eligibility = iota
	Ineligible
	Unknown
)

// ParseScope reads a record's stored scope. The bool is false when the record carries
// no scope (a legacy record); a present-but-malformed scope is an error.
func ParseScope(record Record) (*Scope, bool, error) {
	raw, ok := record["scope"].(map[string]any)
	if !ok {
		return nil, false, nil
	}
	if v := numInt(raw["version"]); v != ScopeVersion {
		return nil, true, fmt.Errorf("scope version %d is not %d — this binary cannot score it", v, ScopeVersion)
	}
	s := &Scope{raw: raw}
	if truthy(raw["all"]) {
		s.All = true
		return s, true, nil
	}
	if expr := str(raw["repo"]); expr != "" {
		re, err := compilePattern(expr)
		if err != nil {
			return nil, true, fmt.Errorf("scope repo regex %s: %w", pyRepr(expr), err)
		}
		s.Repo = re
	}
	if expr := str(raw["path"]); expr != "" {
		re, err := compilePattern(expr)
		if err != nil {
			return nil, true, fmt.Errorf("scope path regex %s: %w", pyRepr(expr), err)
		}
		s.Path = re
	}
	if s.Repo == nil && s.Path == nil {
		return nil, true, fmt.Errorf("scope has neither all, repo, nor path")
	}
	return s, true, nil
}

// BuildScope assembles and validates the record's stored scope from the flags. Exactly
// one of scopeAll or (repo|path) may be set.
func BuildScope(scopeAll bool, repoExpr, pathExpr string) (map[string]any, error) {
	hasPredicate := repoExpr != "" || pathExpr != ""
	if scopeAll && hasPredicate {
		return nil, fmt.Errorf("--scope-all is exclusive with --scope-repo/--scope-path")
	}
	if !scopeAll && !hasPredicate {
		return nil, fmt.Errorf("a scope is required: pass --scope-all, or at least one of --scope-repo/--scope-path")
	}
	if scopeAll {
		return map[string]any{"version": ScopeVersion, "all": true}, nil
	}
	out := map[string]any{"version": ScopeVersion}
	if repoExpr != "" {
		if _, err := compilePattern(repoExpr); err != nil {
			return nil, fmt.Errorf("bad --scope-repo regex %s: %w", pyRepr(repoExpr), err)
		}
		out["repo"] = repoExpr
	}
	if pathExpr != "" {
		if _, err := compilePattern(pathExpr); err != nil {
			return nil, fmt.Errorf("bad --scope-path regex %s: %w", pyRepr(pathExpr), err)
		}
		out["path"] = pathExpr
	}
	return out, nil
}

// predResult is one predicate's standing: satisfied, definitely failed, or undecidable.
type predResult int

const (
	predMatch predResult = iota
	predNoMatch
	predUnknown
)

// Classify places one chain against the scope. A definite non-match on either predicate
// excludes; an undecidable predicate with no definite non-match is Unknown.
func (s *Scope) Classify(ch Chain) Eligibility {
	if s.All {
		return Eligible
	}
	repo := predMatch
	if s.Repo != nil {
		switch {
		case !ch.RepoKnown:
			repo = predUnknown
		case s.Repo.MatchString(ch.Repo):
			repo = predMatch
		default:
			repo = predNoMatch
		}
	}
	path := predMatch
	if s.Path != nil {
		switch {
		case !ch.PathsKnown:
			path = predUnknown
		case matchesAnyPath(s.Path, ch.Paths):
			path = predMatch
		default:
			path = predNoMatch
		}
	}
	if repo == predNoMatch || path == predNoMatch {
		return Ineligible
	}
	if repo == predUnknown || path == predUnknown {
		return Unknown
	}
	return Eligible
}

func matchesAnyPath(re *regexp.Regexp, paths []string) bool {
	for _, p := range paths {
		if re.MatchString(p) {
			return true
		}
	}
	return false
}

// Coverage is a window's chains split by standing: Total == Eligible+Ineligible+Unknown.
type Coverage struct {
	Total      int `json:"total"`
	Eligible   int `json:"eligible"`
	Ineligible int `json:"ineligible"`
	Unknown    int `json:"unknown"`
	chains     []Chain
}

// EligibleChains is the subset that counts toward incidence.
func (c Coverage) EligibleChains() []Chain { return c.chains }

// Cover classifies every chain in a window against the scope. A nil scope admits every
// chain, so a caller that has not branched on scope presence gets the whole window.
func Cover(chains []Chain, scope *Scope) Coverage {
	cov := Coverage{Total: len(chains)}
	for _, ch := range chains {
		e := Eligible
		if scope != nil {
			e = scope.Classify(ch)
		}
		switch e {
		case Eligible:
			cov.Eligible++
			cov.chains = append(cov.chains, ch)
		case Ineligible:
			cov.Ineligible++
		case Unknown:
			cov.Unknown++
		}
	}
	return cov
}

// CoverCorpus classifies the whole corpus against the scope the given predicates
// describe, so an author sees the eligible/ineligible/unknown split before committing.
// Empty predicates preview an all-scope. A bad regex is returned as an error.
func CoverCorpus(stateDir, ledger, repoExpr, pathExpr string) (Coverage, error) {
	rows, err := LoadRows(stateDir, ledger)
	if err != nil {
		return Coverage{}, err
	}
	chains := BuildChains(rows, false)
	m := map[string]any{"version": ScopeVersion}
	if repoExpr == "" && pathExpr == "" {
		m["all"] = true
	} else {
		if repoExpr != "" {
			m["repo"] = repoExpr
		}
		if pathExpr != "" {
			m["path"] = pathExpr
		}
	}
	scope, _, err := ParseScope(Record{"scope": m})
	if err != nil {
		return Coverage{}, err
	}
	return Cover(chains, scope), nil
}

// pathsFromDiff recovers changed paths from a saved diff sidecar, for chains whose rows
// predate the changed_paths stamp. It keeps the real path from each +++/--- header; a
// rename thus contributes both names, which is harmless to a path regex.
func pathsFromDiff(text string) []string {
	var out []string
	seen := map[string]bool{}
	// The marker fixes which git prefix to strip: `--- ` always carries a/, `+++ `
	// always b/. Stripping both would eat a real leading a/ or b/ from the path itself.
	add := func(marker, prefix, line string) {
		rest := strings.TrimSpace(strings.TrimPrefix(line, marker))
		if rest == "" || rest == "/dev/null" {
			return
		}
		rest = strings.ReplaceAll(strings.TrimPrefix(rest, prefix), "\\", "/")
		if rest == "" || seen[rest] {
			return
		}
		seen[rest] = true
		out = append(out, rest)
	}
	// Only the ---/+++ lines in a file's header name paths; the identical prefixes on a
	// hunk body ("-- comment" removed, "++ x" added) do not. A header sits between the
	// file's `diff --git` and its first `@@`, so gate on being outside a hunk.
	inHunk := false
	for _, line := range strings.Split(text, "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			inHunk = false
		case strings.HasPrefix(line, "@@ "):
			inHunk = true
		case !inHunk && strings.HasPrefix(line, "+++ "):
			add("+++ ", "b/", line)
		case !inHunk && strings.HasPrefix(line, "--- "):
			add("--- ", "a/", line)
		}
	}
	return out
}
