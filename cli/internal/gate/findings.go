package gate

import (
	"path"
	"regexp"
	"sort"
	"strings"
)

// The indexed severity line the review prompt mandates. Case-insensitive, because a
// reviewer writing `severity: blocking` was otherwise not counted at all, and an
// uncounted finding is the one direction this log must not fail in — it flatters the
// gate.
var severityRe = regexp.MustCompile(`(?im)^[ \t]*\[\d+\][ \t]*SEVERITY:[ \t]*([A-Za-z]+)`)

// The loose count: any SEVERITY line, indexed or not. It rides the row for
// measurement and NEVER drives a decision — it also matches `SEVERITY: NONE` and
// rubric echoes, and a false count denies a clean commit.
var severityLooseRe = regexp.MustCompile(`(?im)^[ \t]*(?:\[\d+\][ \t]*)?SEVERITY:[ \t]*([A-Za-z]+)`)

var fileRe = regexp.MustCompile(`(?im)^[ \t]*FILE:[ \t]*(\S+)`)

// CountFindings is the decision count: strictly indexed severity lines.
func CountFindings(text string) int {
	return len(severityRe.FindAllString(text, -1))
}

// CountFindingsLoose is the measurement-only count. Divergence from CountFindings in
// the real corpus is evidence for changing the strict pattern; it never blocks.
func CountFindingsLoose(text string) int {
	return len(severityLooseRe.FindAllString(text, -1))
}

var (
	markdownLinkRe = regexp.MustCompile(`^[^\[]*\[([^\]]*)\]\(.*$`)
	lineNumberRe   = regexp.MustCompile(`:\d+(?:[-:]\d+)?$`)
)

const citationTrim = "`'\"“”,;()[]<> "

// CleanPath normalises a reviewer's FILE citation to a bare repo path, stripping the
// wrappers, line numbers and markdown links reviewers add: `store.py`, [store.py],
// store.py:86-92, [a.py](file:///a.py#L1).
//
// Looped, because each rule unblocks the others. Right-trim only for the trailing period,
// since `./pkg/store.py` begins with one and a two-sided trim would break it.
func CleanPath(cited string) string {
	prev := ""
	for prev != cited {
		prev = cited
		cited = markdownLinkRe.ReplaceAllString(cited, "$1")
		cited = strings.Trim(cited, citationTrim)
		cited = strings.TrimRight(cited, ".")
		cited = lineNumberRe.ReplaceAllString(cited, "")
	}
	return cited
}

// IsPath reports whether a cleaned citation names a file at all, rather than prose. A
// separator or an extension is the test, so an extensionless top-level file (`Makefile`)
// is dropped — understating the cross-file count, never inflating it.
func IsPath(cited string) bool {
	return strings.Contains(cited, "/") || strings.Contains(path.Base(cited), ".")
}

// FindingShape is the severity mix and cross-file count for the log, parsed from the raw
// reviews. The basename fallback errs toward calling a finding same-file, which
// understates the number this gate is judged by rather than inflating it.
func FindingShape(reviews []string, changed []string) Row {
	severities := map[string]int{}
	for _, review := range reviews {
		for _, m := range severityRe.FindAllStringSubmatch(review, -1) {
			severities[strings.ToUpper(m[1])]++
		}
	}
	changedSet := map[string]bool{}
	basenames := map[string]bool{}
	for _, p := range changed {
		changedSet[p] = true
		basenames[path.Base(p)] = true
	}
	var files []string
	cross := 0
	for _, review := range reviews {
		for _, m := range fileRe.FindAllStringSubmatch(review, -1) {
			cited := CleanPath(m[1])
			if !IsPath(cited) {
				continue
			}
			files = append(files, cited)
			if !changedSet[cited] && !basenames[path.Base(cited)] {
				cross++
			}
		}
	}
	unique := sortedUnique(files)
	if len(unique) > 20 {
		unique = unique[:20]
	}
	sort.Strings(unique)
	return Row{
		"severities":          severities,
		"cross_file_findings": cross,
		"finding_files":       unique,
	}
}
