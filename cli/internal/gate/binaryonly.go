package gate

import (
	"os"
	"strings"
)

// IsBinaryOnly reports whether raw holds nothing but content changes to existing
// binary files, naming each of paths exactly once. Each stanza must be exactly
// `diff --git a/P b/P`, `index <id>..<id> 100644|100755` and
// `Binary files a/P and b/P differ`; any other line or a missing path returns false.
func IsBinaryOnly(raw string, paths []string) bool {
	if raw == "" || len(paths) == 0 || !strings.HasSuffix(raw, "\n") {
		return false
	}
	want := map[string]bool{}
	for _, p := range paths {
		want[p] = true
	}
	lines := strings.Split(strings.TrimSuffix(raw, "\n"), "\n")
	if len(lines)%3 != 0 {
		return false
	}
	seen := map[string]bool{}
	for i := 0; i < len(lines); i += 3 {
		path, ok := stanzaPath(lines[i])
		if !ok || !want[path] || seen[path] {
			return false
		}
		if !binaryIndexLine(lines[i+1]) || lines[i+2] != "Binary files a/"+path+" and b/"+path+" differ" {
			return false
		}
		seen[path] = true
	}
	return len(seen) == len(want)
}

// stanzaPath reads P from `diff --git a/P b/P`. The two halves must be the same path,
// which fixes P's length without splitting on a space the path may contain.
func stanzaPath(line string) (string, bool) {
	rest, ok := strings.CutPrefix(line, "diff --git a/")
	if !ok || len(rest) < 4 || (len(rest)-3)%2 != 0 {
		return "", false
	}
	n := (len(rest) - 3) / 2
	if rest[n:n+3] != " b/" || rest[:n] != rest[n+3:] {
		return "", false
	}
	return rest[:n], true
}

// binaryIndexLine accepts `index <id>..<id> <mode>` for a regular file whose mode
// did not change.
func binaryIndexLine(line string) bool {
	rest, ok := strings.CutPrefix(line, "index ")
	if !ok {
		return false
	}
	ids, mode, ok := strings.Cut(rest, " ")
	if !ok || (mode != "100644" && mode != "100755") {
		return false
	}
	from, to, ok := strings.Cut(ids, "..")
	return ok && isObjectID(from) && isObjectID(to)
}

func isObjectID(s string) bool {
	if s == "" || strings.Trim(s, "0") == "" {
		return false
	}
	for _, c := range s {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}

// externalDiffConfigured reports whether git may hand a diff to an external program,
// whose output can mimic any stanza IsBinaryOnly accepts. A failed read counts as yes.
func externalDiffConfigured(cwd string) bool {
	if _, set := os.LookupEnv("GIT_EXTERNAL_DIFF"); set {
		return true
	}
	_, code, err := runGitAllowingExit1(cwd, "config", "--get-regexp", `^diff\.(external|.+\.command)$`)
	return err != nil || code != 1
}
