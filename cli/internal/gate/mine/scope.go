package mine

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/steer-sh/steer-cli/internal/harness"
)

// The scope schema versions this build understands. v1 carries repo and path; v2 adds
// the harness predicate; v3 adds the exact repository root. A scope is written at the
// lowest version that can express it, so a scope naming no harness stays v1 and an
// older binary still reads it.
const (
	ScopeVersionV1 = 1
	ScopeVersionV2 = 2
	ScopeVersionV3 = 3

	// ScopeVersion is the newest version this build writes.
	ScopeVersion = ScopeVersionV3
)

// Scope is a record's frozen eligibility rule over harness, repo and path metadata. All
// is exclusive with the predicates; every predicate present combines with AND.
type Scope struct {
	All     bool
	Harness []string
	Repo    *regexp.Regexp
	// RepoRoot is one exact repository root. It is the predicate a repository-bound
	// fix is scoped by: two checkouts sharing a basename are different repositories,
	// and only one of them runs the fix.
	RepoRoot string
	Path     *regexp.Regexp
	raw      map[string]any
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
	version := numInt(raw["version"])
	if version < ScopeVersionV1 || version > ScopeVersionV3 {
		return nil, true, fmt.Errorf("scope version %d is not %d, %d or %d — this binary cannot score it",
			version, ScopeVersionV1, ScopeVersionV2, ScopeVersionV3)
	}
	s := &Scope{raw: raw}
	if truthy(raw["all"]) {
		s.All = true
		return s, true, nil
	}
	for _, item := range asList(raw["harness"]) {
		if id := str(item); id != "" {
			s.Harness = appendUnique(s.Harness, id)
		}
	}
	if expr := str(raw["repo"]); expr != "" {
		re, err := compilePattern(expr)
		if err != nil {
			return nil, true, fmt.Errorf("scope repo regex %s: %w", pyRepr(expr), err)
		}
		s.Repo = re
	}
	if root := str(raw["repo_root"]); root != "" {
		s.RepoRoot = root
	}
	if expr := str(raw["path"]); expr != "" {
		re, err := compilePattern(expr)
		if err != nil {
			return nil, true, fmt.Errorf("scope path regex %s: %w", pyRepr(expr), err)
		}
		s.Path = re
	}
	if s.Repo == nil && s.Path == nil && s.RepoRoot == "" && len(s.Harness) == 0 {
		return nil, true, fmt.Errorf("scope has neither all, harness, repo, repo_root, nor path")
	}
	return s, true, nil
}

// BuildScope assembles and validates the record's stored scope from the flags. Exactly
// one of scopeAll or (harness|repo|repo-root|path) may be set. The harness list names
// exact harness ids, not a regex: a harness is a closed set, so a typo is a write-time
// error rather than a predicate that silently matches nothing. repoRoot is one exact
// repository root rather than a regex, for the same reason.
func BuildScope(scopeAll bool, harnesses []string, repoExpr, repoRoot, pathExpr string) (map[string]any, error) {
	hasPredicate := repoExpr != "" || repoRoot != "" || pathExpr != "" || len(harnesses) > 0
	if scopeAll && hasPredicate {
		return nil, fmt.Errorf("--scope-all is exclusive with " +
			"--scope-harness/--scope-repo/--scope-repo-root/--scope-path")
	}
	if !scopeAll && !hasPredicate {
		return nil, fmt.Errorf("a scope is required: pass --scope-all, or at least one of " +
			"--scope-harness/--scope-repo/--scope-repo-root/--scope-path")
	}
	if scopeAll {
		return map[string]any{"version": ScopeVersionV1, "all": true}, nil
	}
	// v1 expresses repo and path, so a scope naming no harness is written as v1 and an
	// older binary keeps scoring it. Only a new predicate costs compatibility.
	version := ScopeVersionV1
	if len(harnesses) > 0 {
		version = ScopeVersionV2
	}
	if repoRoot != "" {
		version = ScopeVersionV3
	}
	out := map[string]any{"version": version}
	if len(harnesses) > 0 {
		ids, err := normalizeHarnesses(harnesses)
		if err != nil {
			return nil, err
		}
		list := make([]any, len(ids))
		for i, id := range ids {
			list[i] = id
		}
		out["harness"] = list
	}
	if repoExpr != "" {
		if _, err := compilePattern(repoExpr); err != nil {
			return nil, fmt.Errorf("bad --scope-repo regex %s: %w", pyRepr(repoExpr), err)
		}
		out["repo"] = repoExpr
	}
	if repoRoot != "" {
		root, err := normalizeRepoRoot(repoRoot)
		if err != nil {
			return nil, err
		}
		out["repo_root"] = root
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

// Subject is the metadata a scope reads about one denominator entry. A chain and a
// transcript session both reduce to it, so one predicate serves both corpora and no
// scope rule is written twice. Scope reads this and never the evidence text, which
// would select on the outcome.
type Subject struct {
	Harness      string
	HarnessKnown bool
	Repo         string
	RepoKnown    bool
	// RepoRoot is the whole repository root the unit's evidence recorded, in whatever
	// form its corpus stores it. The root predicate compares normalized keys, so a
	// ledger row's absolute path and a transcript's munged project directory name the
	// same repository.
	RepoRoot      string
	RepoRootKnown bool
	Paths         []string
	PathsKnown    bool
}

// Subject reduces a chain to the metadata a scope reads.
func (ch Chain) Subject() Subject {
	return Subject{
		Harness:       ch.Harness,
		HarnessKnown:  ch.HarnessKnown,
		Repo:          ch.Repo,
		RepoKnown:     ch.RepoKnown,
		RepoRoot:      ch.RepoRoot,
		RepoRootKnown: ch.RepoRootKnown,
		Paths:         ch.Paths,
		PathsKnown:    ch.PathsKnown,
	}
}

// Classify places one chain against the scope.
func (s *Scope) Classify(ch Chain) Eligibility { return s.ClassifySubject(ch.Subject()) }

// ClassifySubject places one subject against the scope. A definite non-match on any
// predicate excludes; an undecidable predicate with no definite non-match is Unknown.
func (s *Scope) ClassifySubject(sub Subject) Eligibility {
	if s.All {
		return Eligible
	}
	hrn := predMatch
	if len(s.Harness) > 0 {
		switch {
		case !sub.HarnessKnown:
			hrn = predUnknown
		case containsString(s.Harness, sub.Harness):
			hrn = predMatch
		default:
			hrn = predNoMatch
		}
	}
	repo := predMatch
	if s.Repo != nil {
		switch {
		case !sub.RepoKnown:
			repo = predUnknown
		case s.Repo.MatchString(sub.Repo):
			repo = predMatch
		default:
			repo = predNoMatch
		}
	}
	root := predMatch
	if s.RepoRoot != "" {
		switch {
		case !sub.RepoRootKnown:
			root = predUnknown
		case sameRepoRoot(s.RepoRoot, sub.RepoRoot):
			root = predMatch
		default:
			root = predNoMatch
		}
	}
	path := predMatch
	if s.Path != nil {
		switch {
		case !sub.PathsKnown:
			path = predUnknown
		case matchesAnyPath(s.Path, sub.Paths):
			path = predMatch
		default:
			path = predNoMatch
		}
	}
	if hrn == predNoMatch || repo == predNoMatch || root == predNoMatch || path == predNoMatch {
		return Ineligible
	}
	if hrn == predUnknown || repo == predUnknown || root == predUnknown || path == predUnknown {
		return Unknown
	}
	return Eligible
}

// normalizeRepoRoot validates and canonicalizes a root a writer typed. It must be
// absolute, because a relative path names a different repository from every other
// directory; trailing separators are dropped. Symlinks are resolved, because the
// evidence side stamps checks.RepoIdentity, and an alias of a checkout must select
// the same rows as the path git reports. A root this machine cannot resolve keeps
// its typed form: a scope may name a repository that is not checked out here. A
// root that resolves to a filesystem root is refused, because trimming its
// separator leaves the empty root that reads as no root predicate at all.
func normalizeRepoRoot(root string) (string, error) {
	trimmed := trimRoot(root)
	if trimmed == "" {
		return "", fmt.Errorf("--scope-repo-root names no path")
	}
	if !filepath.IsAbs(root) {
		return "", fmt.Errorf("--scope-repo-root %s is not an absolute repository root",
			pyRepr(root))
	}
	// EvalSymlinks splits on the platform separator and a backslash is a legal
	// character in a POSIX name, so it reads the typed path, never trimRoot's form.
	resolved, err := filepath.EvalSymlinks(strings.TrimSpace(root))
	if err != nil {
		return trimmed, nil
	}
	out := trimRoot(resolved)
	if out != "" && out != trimRoot(filepath.VolumeName(resolved)) {
		return out, nil
	}
	return "", fmt.Errorf("--scope-repo-root %s resolves to a filesystem root, "+
		"which is not a repository", pyRepr(root))
}

// trimRoot puts one path in the stored form: separators are slashes and no trailing
// separator remains. Both sides of normalizeRepoRoot pass through it, so a resolved
// root and an unresolved one are stored the same way.
func trimRoot(s string) string {
	return strings.TrimRight(strings.ReplaceAll(strings.TrimSpace(s), "\\", "/"), "/")
}

// sameRepoRoot compares two repository roots through the project key both corpora can
// produce. A ledger row stores the absolute root; a transcript stores the harness's
// munged directory name for it, and no path survives that unchanged.
func sameRepoRoot(a, b string) bool {
	return projectKey(a) != "" && projectKey(a) == projectKey(b)
}

// projectKey is transcript.NormalizeProject's pre-image, repeated here because nothing
// under gate may import a transcript adapter. scope_test.go pins the two together. It
// is idempotent, so a key normalized once compares equal to its own path.
func projectKey(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range strings.TrimRight(strings.ReplaceAll(s, "\\", "/"), "/") {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	return b.String()
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
// UnknownScope and UnknownEvidence break Unknown down by cause. UnknownEvidence is
// Pending + LedgerMissed + LedgerOverdue: a ledger that may still arrive, one the
// resolver closed as a miss, and one the resolver has not closed past the grace window.
type Coverage struct {
	Total           int `json:"total"`
	Eligible        int `json:"eligible"`
	Ineligible      int `json:"ineligible"`
	Unknown         int `json:"unknown"`
	UnknownScope    int `json:"unknown_scope"`
	UnknownEvidence int `json:"unknown_evidence"`
	Pending         int `json:"pending"`
	LedgerMissed    int `json:"ledger_missed"`
	LedgerOverdue   int `json:"ledger_overdue"`
	chains          []Chain
}

// EligibleChains is the subset that counts toward incidence.
func (c Coverage) EligibleChains() []Chain { return c.chains }

// Cover classifies every chain in a window against the scope. A nil scope admits every
// chain, so a caller that has not branched on scope presence gets the whole window.
func Cover(chains []Chain, scope *Scope) Coverage {
	return CoverEvidence(chains, scope, 0)
}

// CoverEvidence classifies against the scope and then against adjudication
// completeness. A scope-eligible chain whose findings are pending or missing moves to
// Unknown: it neither counts as a clean miss nor shrinks the class's measured rate.
// now is the clock the grace window is read against; 0 skips the evidence pass, for a
// caller asking about scope selectivity alone.
func CoverEvidence(chains []Chain, scope *Scope, now float64) Coverage {
	cov := Coverage{Total: len(chains)}
	for _, ch := range chains {
		e := Eligible
		if scope != nil {
			e = scope.Classify(ch)
		}
		if e == Eligible && now > 0 {
			switch ChainEvidence(ch, now) {
			case EvidencePending:
				e = Unknown
				cov.Pending++
				cov.UnknownEvidence++
			case EvidenceMissed:
				e = Unknown
				cov.LedgerMissed++
				cov.UnknownEvidence++
			case EvidenceOverdue:
				e = Unknown
				cov.LedgerOverdue++
				cov.UnknownEvidence++
			}
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
	cov.UnknownScope = cov.Unknown - cov.UnknownEvidence
	return cov
}

// CoverCorpus classifies the whole corpus against the scope the given predicates
// describe, so an author sees the eligible/ineligible/unknown split before committing.
// It runs the evidence pass the writers run, so the preview and the baseline the write
// freezes count one set of chains. Empty predicates preview an all-scope. A bad regex
// is returned as an error.
func CoverCorpus(stateDir, ledger string, harnesses []string, repoExpr, repoRoot, pathExpr string) (Coverage, error) {
	rows, err := LoadRows(stateDir, ledger)
	if err != nil {
		return Coverage{}, err
	}
	chains := BuildChains(rows, false)
	all := len(harnesses) == 0 && repoExpr == "" && repoRoot == "" && pathExpr == ""
	m, err := BuildScope(all, harnesses, repoExpr, repoRoot, pathExpr)
	if err != nil {
		return Coverage{}, err
	}
	scope, _, err := ParseScope(Record{"scope": m})
	if err != nil {
		return Coverage{}, err
	}
	return CoverEvidence(chains, scope, float64(now())), nil
}

// CoverCorpusSessions classifies the session corpus against the scope the predicates
// describe, the session engine's analogue of CoverCorpus.
func CoverCorpusSessions(units []SessionUnit, harnesses []string, repoExpr, repoRoot string) (SessionCoverage, error) {
	all := len(harnesses) == 0 && repoExpr == "" && repoRoot == ""
	m, err := BuildScope(all, harnesses, repoExpr, repoRoot, "")
	if err != nil {
		return SessionCoverage{}, err
	}
	scope, _, err := ParseScope(Record{"scope": m})
	if err != nil {
		return SessionCoverage{}, err
	}
	return CoverSessions(units, scope, 0), nil
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

// normalizeHarnesses validates each id against the registry and returns them sorted and
// deduplicated, so one scope has one stored form whatever order the flags arrived in.
func normalizeHarnesses(names []string) ([]string, error) {
	var out []string
	for _, name := range names {
		for _, part := range strings.Split(name, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			id, err := harness.Parse(part)
			if err != nil {
				return nil, fmt.Errorf("bad --scope-harness %s: %w", pyRepr(part), err)
			}
			out = appendUnique(out, string(id))
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("--scope-harness names no harness")
	}
	sort.Strings(out)
	return out, nil
}

func containsString(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
