package gate

import (
	"strings"
	"testing"
)

// A verdict that carries a qualifier inside the bold marker keeps its verdict and
// its concern; before, `**ACCEPTED, not fixed**` dropped the whole line.
func TestLedgerLineReadsAQualifiedVerdict(t *testing.T) {
	cases := []struct {
		line      string
		verdict   string
		concern   string
		qualifier string
	}{
		{"- **ACCEPTED, not fixed** — off by one in the loop bound", "ACCEPTED", "off by one in the loop bound", "not fixed"},
		{"- **ACCEPTED (not fixed)** — off by one", "ACCEPTED", "off by one", "not fixed"},
		{"- **REJECTED; duplicate** — same claim", "REJECTED", "same claim", "duplicate"},
		{"- **ACCEPTED** — plain", "ACCEPTED", "plain", ""},
	}
	for _, tc := range cases {
		got, ok := parseLedgerLine(tc.line)
		if !ok || got.Verdict != tc.verdict || got.Concern != tc.concern || got.Qualifier != tc.qualifier {
			t.Errorf("parseLedgerLine(%q) = %+v, %v; want %s/%q/%q", tc.line, got, ok, tc.verdict, tc.concern, tc.qualifier)
		}
	}
	for _, line := range []string{
		"- **ACCEPTEDNESS** — a longer word is not a verdict",
		"- **ACCEPTED, not fixed — no closing marker",
		"- **ACCEPTED *nested* x** — emphasis inside the marker",
	} {
		if got, ok := parseLedgerLine(line); ok {
			t.Errorf("parseLedgerLine(%q) = %+v; want no verdict", line, got)
		}
	}
}

// Every verdict bullet the deny and cap templates show must parse, or an agent that
// copies the template writes a line the ledger drops.
func TestTemplateVerdictLinesParse(t *testing.T) {
	for name, text := range map[string]string{"deny": denyTemplate, "cap": capTemplate} {
		seen := map[string]bool{}
		for _, line := range strings.Split(text, "\n") {
			if !strings.HasPrefix(line, "- **ACCEPTED") && !strings.HasPrefix(line, "- **REJECTED") {
				continue
			}
			got, ok := parseLedgerLine(line)
			if !ok {
				t.Errorf("%s.md line does not parse: %q", name, line)
				continue
			}
			seen[got.Verdict+"/"+got.Qualifier] = true
		}
		if !seen["ACCEPTED/not fixed"] {
			t.Errorf("%s.md shows no form for an accepted finding left unfixed", name)
		}
	}
}

// The resolution after the arrow is kept beside the concern, and the concern, which is
// the measurement key, stays the text before the arrow.
func TestLedgerTextKeepsTheResolution(t *testing.T) {
	row := ParseLedgerText("- **ACCEPTED** — a split value hides a commit → needed by the user's request; refused at syntax.go:12\n" +
		"- **REJECTED** — no arrow on this line\n")
	concerns := row["concerns"].([]Concern)
	if len(concerns) != 2 {
		t.Fatalf("concerns = %d, want 2", len(concerns))
	}
	if got, want := concerns[0].Concern, "a split value hides a commit"; got != want {
		t.Errorf("concern = %q, want %q", got, want)
	}
	if got, want := concerns[0].Resolution, "needed by the user's request; refused at syntax.go:12"; got != want {
		t.Errorf("resolution = %q, want %q", got, want)
	}
	if got := concerns[1].Resolution; got != "" {
		t.Errorf("resolution = %q, want empty for a line without an arrow", got)
	}
}

// A fixed finding's ledger line names the requirement that needed the repair, so the
// ledger records the necessity ruling and not only the change.
func TestTemplateFixedLinesNameTheRequirement(t *testing.T) {
	for name, text := range map[string]string{"deny": denyTemplate, "cap": capTemplate} {
		found := false
		for _, line := range strings.Split(text, "\n") {
			got, ok := parseLedgerLine(line)
			if !ok || got.Verdict != "ACCEPTED" || got.Qualifier != "" {
				continue
			}
			found = true
			if !strings.Contains(line, "→ needed by ") {
				t.Errorf("%s.md fixed line does not name the requirement: %q", name, line)
			}
		}
		if !found {
			t.Errorf("%s.md shows no fixed line", name)
		}
	}
}
