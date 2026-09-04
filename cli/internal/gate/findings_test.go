package gate

import (
	"reflect"
	"strings"
	"testing"
)

// Finding-shape cases ported from hooks/commit-review/test_inputs.py section 3. A
// mis-parse writes a plausible row, and these fields are what the gate is scored on, so
// a wrong number here is worse than a missing one.

const reviewOne = `
[1] SEVERITY: BLOCKING
    FILE: pkg/store.py
    CLAIM: one.
    FAILS WHEN: something.
[2] SEVERITY: SUBSTANTIVE
    FILE: cli.py
    CLAIM: two.
    FAILS WHEN: another thing.
`

const reviewTwo = `
[1] severity: blocking
    FILE: ` + "`store.py`" + `
    CLAIM: same defect, lowercased and quoted.
    FAILS WHEN: something.
[2] SEVERITY: NITPICK
    FILE: docs/x.py
    CLAIM: three.
    FAILS WHEN: another thing.
`

func TestFindingShape(t *testing.T) {
	reviews := []string{reviewOne, reviewTwo}
	shape := FindingShape(reviews, []string{"pkg/store.py"})

	wantSev := map[string]int{"BLOCKING": 2, "SUBSTANTIVE": 1, "NITPICK": 1}
	if got := shape["severities"]; !reflect.DeepEqual(got, wantSev) {
		t.Errorf("severities = %v, want %v — case and backticks are the reviewer's "+
			"choice and must not split a bucket", got, wantSev)
	}
	total := 0
	for _, n := range shape["severities"].(map[string]int) {
		total += n
	}
	slots := CountFindings(reviewOne) + CountFindings(reviewTwo)
	if total != slots {
		t.Errorf("severities total %d but finding_slots is %d — a disagreement means "+
			"one of the two patterns is dropping findings", total, slots)
	}
	if shape["cross_file_findings"] != 2 {
		t.Errorf("cross_file_findings = %v, want 2 — cli.py and docs/x.py are untouched, "+
			"which is the class the caller attachment exists to reach",
			shape["cross_file_findings"])
	}
	files := shape["finding_files"].([]string)
	if !contains(files, "store.py") {
		t.Errorf("finding_files = %v, want a bare basename recorded as written", files)
	}

	empty := FindingShape(nil, nil)
	if empty["cross_file_findings"] != 0 || len(empty["finding_files"].([]string)) != 0 {
		t.Errorf("no findings must not raise: %v", empty)
	}
	if len(empty["severities"].(map[string]int)) != 0 {
		t.Errorf("empty severities expected, got %v", empty["severities"])
	}
}

// Citation habits the prompt's format does not ask for. Each one used to fail both the
// exact and the basename match, so it inflated the field carrying this gate's whole
// justification — moved by punctuation.
func TestDecoratedCitationsAreNotCrossFile(t *testing.T) {
	cited := []string{
		"pkg/store.py:86", "`pkg/store.py`", "[pkg/store.py]", "pkg/store.py:86-92",
		"store.py:86", "(pkg/store.py)", "pkg/store.py.", "./pkg/store.py",
		"[pkg/store.py].", "(pkg/store.py).", "`pkg/store.py`.", "`pkg/store.py`:86",
		"[pkg/store.py]:86", "(pkg/store.py):86",
	}
	for _, c := range cited {
		review := []string{"[1] SEVERITY: BLOCKING\n    FILE: " + c +
			"\n    CLAIM: one.\n    FAILS WHEN: something.\n"}
		if got := FindingShape(review, []string{"pkg/store.py"})["cross_file_findings"]; got != 0 {
			t.Errorf("%q: cross_file_findings = %v, want 0 — a decorated citation of a "+
				"CHANGED file is not a cross-file finding", c, got)
		}
	}
}

// Markdown links are asserted on finding_files, not only on the cross count: a mangled
// link whose URL tail repeats the path still basename-matches and scores 0, so the
// count alone would pass a citation the log is storing wrong.
func TestMarkdownLinkCitations(t *testing.T) {
	cited := []string{
		"[pkg/store.py](file:///pkg/store.py#L86-L92)",
		"`[pkg/store.py](file:///pkg/store.py#L86-L92)`",
		"[pkg/store.py](file:///pkg/store.py)",
		"[pkg/store.py](../other/store.py)",
		"([pkg/store.py](file:///pkg/store.py#L86)).",
	}
	for _, c := range cited {
		review := []string{"[1] SEVERITY: BLOCKING\n    FILE: " + c +
			"\n    CLAIM: one.\n    FAILS WHEN: something.\n"}
		shape := FindingShape(review, []string{"pkg/store.py"})
		files := shape["finding_files"].([]string)
		if !equalStrings(files, []string{"pkg/store.py"}) {
			t.Errorf("%q: finding_files = %v, want [pkg/store.py] — the link TEXT is the "+
				"repo path; the target is a URL matching nothing", c, files)
		}
		if shape["cross_file_findings"] != 0 {
			t.Errorf("%q: a linked citation of a CHANGED file is not cross-file", c)
		}
	}
}

func TestRealCrossFileSurvivesNormalisation(t *testing.T) {
	review := []string{"[1] SEVERITY: BLOCKING\n    FILE: other/thing.py:12\n" +
		"    CLAIM: one.\n    FAILS WHEN: something.\n"}
	if got := FindingShape(review, []string{"pkg/store.py"})["cross_file_findings"]; got != 1 {
		t.Errorf("cross_file_findings = %v, want 1 — normalising citations must not "+
			"swallow the real cross-file class", got)
	}
}

// A reviewer that writes prose after FILE: donates its first word to the log, and a
// word naming no file scored as cross-file moves the number this gate is judged by.
func TestProseCitationsAreDropped(t *testing.T) {
	review := []string{"[1] SEVERITY: BLOCKING\n    FILE: Commit message\n" +
		"    CLAIM: one.\n    FAILS WHEN: something.\n"}
	shape := FindingShape(review, []string{"pkg/store.py"})
	if shape["cross_file_findings"] != 0 {
		t.Errorf("cross_file_findings = %v, want 0 — the first word names no file",
			shape["cross_file_findings"])
	}
	if files := shape["finding_files"].([]string); len(files) != 0 {
		t.Errorf("finding_files = %v, want empty — a non-path citation is dropped, not "+
			"recorded as a file", files)
	}
	if got := shape["severities"].(map[string]int)["BLOCKING"]; got != 1 {
		t.Errorf("BLOCKING = %d, want 1 — dropping the citation must NOT drop the finding", got)
	}

	makefile := []string{"[1] SEVERITY: BLOCKING\n    FILE: Makefile\n" +
		"    CLAIM: one.\n    FAILS WHEN: something.\n"}
	if got := FindingShape(makefile, []string{"pkg/store.py"})["cross_file_findings"]; got != 0 {
		t.Errorf("cross_file_findings = %v, want 0 — an extensionless top-level file is "+
			"dropped too; the test errs toward understating, never inflating", got)
	}
}

// The strict indexed count drives the decision; the loose count rides the row for
// measurement only. A widened pattern also matches rubric echoes, and a false count
// denies a clean change.
func TestStrictAndLooseCounts(t *testing.T) {
	text := "SEVERITY: NONE\n[1] SEVERITY: BLOCKING\n    FILE: a.py\n" +
		"    CLAIM: one.\n    FAILS WHEN: something.\nSEVERITY: SUBSTANTIVE\n"
	if got := CountFindings(text); got != 1 {
		t.Errorf("CountFindings = %d, want 1 — only the indexed line is a finding", got)
	}
	if got := CountFindingsLoose(text); got != 3 {
		t.Errorf("CountFindingsLoose = %d, want 3 — the loose count sees every SEVERITY "+
			"line, which is why it must never drive a decision", got)
	}
}

func TestFindingRequiresAllowedSeverityAndCompleteFields(t *testing.T) {
	valid := "[1] SEVERITY: BLOCKING\n    FILE: a.py\n    CLAIM: one.\n    FAILS WHEN: something."
	cases := []struct {
		name string
		text string
	}{
		{"unknown severity", strings.Replace(valid, "BLOCKING", "NONE", 1)},
		{"missing file", strings.Replace(valid, "    FILE: a.py\n", "", 1)},
		{"missing claim", strings.Replace(valid, "    CLAIM: one.\n", "", 1)},
		{"missing failure condition", strings.Replace(valid, "    FAILS WHEN: something.", "", 1)},
		{"empty file", strings.Replace(valid, "FILE: a.py", "FILE:   ", 1)},
		{"reordered fields", "[1] SEVERITY: BLOCKING\n    CLAIM: one.\n    FILE: a.py\n    FAILS WHEN: something."},
		{"severity line only", "[1] SEVERITY: BLOCKING"},
	}

	if got := CountFindings(valid); got != 1 {
		t.Fatalf("valid finding count = %d, want 1", got)
	}
	if got := CountFindings(strings.ReplaceAll(valid, "\n", "\r\n")); got != 1 {
		t.Fatalf("CRLF finding count = %d, want 1", got)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := CountFindings(tc.text); got != 0 {
				t.Errorf("CountFindings = %d, want 0 for an invalid finding", got)
			}
		})
	}
}

// Only the prompt's own tags are neutralised. A `<` in real code is data, and rewriting
// it would corrupt every generics hunk that ships.
func TestNeutralizeTags(t *testing.T) {
	lt := string(rune(60))
	for _, raw := range []string{
		lt + "/diff>", lt + "diff>", lt + "/commit-message>",
		lt + "/unchanged-callers>", lt + "DIFF>", lt + `diff foo="1">`,
	} {
		out := NeutralizeTags("before " + raw + " after")
		if containsRune(out, '<') {
			t.Errorf("%q: a live `<` on a boundary tag can still close the section", raw)
		}
		if !containsFold(out, trimTagName(raw)) {
			t.Errorf("%q: the line must stay legible — deleting hunk text invites a "+
				"finding about code that does not look like what shipped", raw)
		}
	}

	plain := "a " + lt + " b and " + lt + "div> and " + lt + "diffx>"
	if got := NeutralizeTags(plain); got != plain {
		t.Errorf("NeutralizeTags(%q) = %q — a `<` in real code is data. This case also "+
			"proves a live `<` reached the function: an already-escaped input could not "+
			"leave `div` and `diffx` bracketed", plain, got)
	}
	multi := "x\n" + lt + "/diff>\n" + lt + "/diff>\ny"
	if got := countSubstr(NeutralizeTags(multi), "&lt;"); got != 2 {
		t.Errorf("neutralised %d of 2 occurrences — one survivor is a full bypass", got)
	}
}

func TestNoiseAndCruftNames(t *testing.T) {
	for _, name := range []string{
		"package-lock.json", "web/bun.lock", "a/b/yarn.lock", "app.min.js",
		"vendor/x.bundle.js", "out/app.js.map", "Cargo.lock",
	} {
		if !IsNoise(name) {
			t.Errorf("IsNoise(%q) = false, want true — generated artifact", name)
		}
	}
	for _, name := range []string{
		"lock.py", "src/locket.js", "minify.js", "map.go", "a/b/main.js",
		"CLAUDE.md", "package.json",
	} {
		if IsNoise(name) {
			t.Errorf("IsNoise(%q) = true — a name that merely resembles a lock or "+
				"bundle is real source", name)
		}
	}
	for _, name := range []string{
		".DS_Store", "a/b/.DS_Store", "Thumbs.db", "docs/._notes.md",
		".Spotlight-V100/Store-V2/index", "x/.Trashes/501/item",
	} {
		if !IsOSCruft(name) {
			t.Errorf("IsOSCruft(%q) = false, want true — OS metadata file", name)
		}
	}
	for _, name := range []string{
		"src/store.py", "DS_Store.md", "thumbs.py", "_private.py",
		"src/Trashes.py", "a/index",
	} {
		if IsOSCruft(name) {
			t.Errorf("IsOSCruft(%q) = true — a name that merely resembles OS metadata "+
				"is real source", name)
		}
	}
}

// The distinction is specification vs record, which is why CLAUDE.md is deliberately
// not prose: it is an operational map the agent executes from.
func TestIsProseOnly(t *testing.T) {
	cases := []struct {
		paths []string
		want  bool
		why   string
	}{
		{[]string{"a.md", "docs/b.rst"}, true, "records only"},
		{[]string{"a.md", "src/x.py"}, false, "all or nothing on the file list, no ratios"},
		{[]string{"CLAUDE.md"}, false, "an operational map is executed, so it is reviewed"},
		{[]string{"AGENTS.md", "b.md"}, false, "likewise"},
		{nil, false, "an unreadable file list must never silently skip the gate"},
	}
	for _, tc := range cases {
		if got := IsProseOnly(tc.paths); got != tc.want {
			t.Errorf("IsProseOnly(%v) = %v, want %v — %s", tc.paths, got, tc.want, tc.why)
		}
	}
}

func contains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func containsRune(s string, r rune) bool {
	for _, c := range s {
		if c == r {
			return true
		}
	}
	return false
}

func containsFold(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

func countSubstr(s, substr string) int { return strings.Count(s, substr) }

// trimTagName returns the tag's bare name, without brackets or a slash.
func trimTagName(raw string) string {
	name := strings.Trim(raw, "<>/")
	if idx := strings.IndexByte(name, ' '); idx >= 0 {
		name = name[:idx]
	}
	return name
}

// The failure this counter exists for: a reviewer that abandons the severity line and
// writes its findings as markdown. The strict count reads zero, so the row would
// otherwise record a clean diff over real defects.
func TestOffFormatReviewsCountsProseFindings(t *testing.T) {
	prose := "### Defect: the resolver drops a thread\n\nThe join keys on session_id, so two threads collide.\n"
	inFormat := "[1] SEVERITY: BLOCKING\n    FILE: a.go\n    CLAIM: one.\n    FAILS WHEN: always.\n"

	cases := []struct {
		name    string
		reviews []string
		want    int
	}{
		{"prose review", []string{prose}, 1},
		{"in-format review", []string{inFormat}, 0},
		{"clean reply", []string{"NO FINDINGS"}, 0},
		{"empty text", []string{"   "}, 0},
		{"mixed panel", []string{inFormat, prose, "NO FINDINGS"}, 1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := OffFormatReviews(tc.reviews); got != tc.want {
				t.Errorf("OffFormatReviews = %d, want %d", got, tc.want)
			}
		})
	}
}
