package gate

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// reviewFindingRe is the complete four-line finding contract from the review prompt.
// Field labels and allowed severities are case-insensitive because capitalization does
// not change the finding; missing, reordered or empty fields do. A severity-shaped
// line alone is prose, not a completed review finding.
var reviewFindingRe = regexp.MustCompile(`(?im)^[ \t]*\[\d+\][ \t]*SEVERITY:[ \t]*(BLOCKING|SUBSTANTIVE|NITPICK)[ \t]*\r?\n` +
	`[ \t]+FILE:[ \t]*([^ \t\r\n][^\r\n]*?)[ \t]*\r?\n` +
	`[ \t]+CLAIM:[ \t]*([^ \t\r\n][^\r\n]*?)[ \t]*\r?\n` +
	`[ \t]+FAILS WHEN:[ \t]*([^ \t\r\n][^\r\n]*?)[ \t]*\r?$`)

// The loose count: any SEVERITY line, indexed or not. It rides the row for
// measurement and NEVER drives a decision — it also matches `SEVERITY: NONE` and
// rubric echoes, and a false count denies a clean commit.
var severityLooseRe = regexp.MustCompile(`(?im)^[ \t]*(?:\[\d+\][ \t]*)?SEVERITY:[ \t]*([A-Za-z]+)`)

// CountFindings is the decision count: complete findings with an indexed allowed
// severity and all three required fields.
func CountFindings(text string) int {
	return len(reviewFindingRe.FindAllString(text, -1))
}

// The wrappers a reviewer puts around the clean reply: markdown emphasis, code
// ticks, quotes, a closing period. Both readers below strip the same set, so a
// decorated reply cannot be clean to one of them and a finding to the other.
const cleanReplyTrim = "*`_.!\"' \t\n\r"

// IsNoFindings reports whether a reply OPENS with the clean answer the review prompt
// asks for. Loose on purpose: it decides only whether a reply reaches the composed
// review, where a false negative would deny a clean commit over a reviewer's closing
// remark.
func IsNoFindings(text string) bool {
	return strings.HasPrefix(cleanReplyCore(text), "NO FINDINGS")
}

// IsCleanReply reports whether a reply is the clean answer and NOTHING else. Strict
// where IsNoFindings is loose, because it decides whether a FAILED run counts as a
// review at all: "NO FINDINGS could be determined, the tool was denied" opens with
// the protocol's words and is a failure report, and counting it would inflate the
// answered-reviewer count that the ungated branch reads.
func IsCleanReply(text string) bool {
	return cleanReplyCore(text) == "NO FINDINGS"
}

func cleanReplyCore(text string) string {
	return strings.Trim(strings.ToUpper(text), cleanReplyTrim)
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
	var findings [][]string
	for _, review := range reviews {
		parsed := reviewFindingRe.FindAllStringSubmatch(review, -1)
		findings = append(findings, parsed...)
		for _, m := range parsed {
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
	for _, finding := range findings {
		cited := CleanPath(finding[2])
		if !IsPath(cited) {
			continue
		}
		files = append(files, cited)
		if !changedSet[cited] && !basenames[path.Base(cited)] {
			cross++
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

// ReviewFinding is one parsed finding block from a reviewer's reply.
type ReviewFinding struct {
	Index     int
	Severity  string
	File      string
	Claim     string
	FailsWhen string
}

// findingOpenRe matches the line that OPENS a finding: an index in brackets and
// the severity label. It takes any severity word, so a block whose severity is
// not one of the three is read and refused rather than skipped.
var findingOpenRe = regexp.MustCompile(`(?i)^[ \t]*\[(\d+)\][ \t]*SEVERITY:[ \t]*(.*)$`)

// findingFieldRe reads one indented field line into its label and its value.
var findingFieldRe = regexp.MustCompile(`(?i)^[ \t]+(FILE|CLAIM|FAILS WHEN):[ \t]*(.*)$`)

// findingFieldOrder is the field sequence the review prompt fixes.
var findingFieldOrder = []string{"FILE", "CLAIM", "FAILS WHEN"}

// ParseFindings reads a reviewer's reply into its findings.
//
// It reads the same four-line block reviewFindingRe counts, and differs in what
// it does with a block it cannot read: CountFindings skips one, because an
// unreadable block must never deny a commit, and ParseFindings returns an error
// naming the line it stopped on. A caller narrows a candidate set by what a
// finding names, so a block read wrongly is a wrong label downstream.
//
// An unindexed severity line is prose and is passed over.
func ParseFindings(text string) ([]ReviewFinding, error) {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	var out []ReviewFinding
	for i := 0; i < len(lines); i++ {
		open := findingOpenRe.FindStringSubmatch(lines[i])
		if open == nil {
			continue
		}
		index, err := strconv.Atoi(open[1])
		if err != nil {
			return nil, fmt.Errorf("line %d: %q: the finding index is not a number", i+1, strings.TrimSpace(lines[i]))
		}
		severity := strings.ToUpper(strings.TrimSpace(open[2]))
		switch severity {
		case "BLOCKING", "SUBSTANTIVE", "NITPICK":
		default:
			return nil, fmt.Errorf("line %d: %q: severity must be BLOCKING, SUBSTANTIVE or NITPICK", i+1, strings.TrimSpace(lines[i]))
		}
		finding := ReviewFinding{Index: index, Severity: severity}
		for n, label := range findingFieldOrder {
			row := i + 1 + n
			if row >= len(lines) {
				return nil, fmt.Errorf("line %d: finding [%d] ends before its %s line", row+1, index, label)
			}
			field := findingFieldRe.FindStringSubmatch(lines[row])
			if field == nil || !strings.EqualFold(strings.TrimSpace(field[1]), label) {
				return nil, fmt.Errorf("line %d: %q: finding [%d] expects an indented %s line here", row+1, strings.TrimSpace(lines[row]), index, label)
			}
			value := strings.TrimSpace(field[2])
			if value == "" {
				return nil, fmt.Errorf("line %d: finding [%d] carries an empty %s", row+1, index, label)
			}
			switch label {
			case "FILE":
				finding.File = value
			case "CLAIM":
				finding.Claim = value
			case "FAILS WHEN":
				finding.FailsWhen = value
			}
		}
		out = append(out, finding)
		i += len(findingFieldOrder)
	}
	return out, nil
}
