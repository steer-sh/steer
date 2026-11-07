package plugindist

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const (
	auditGuide   = "skills/steer-report/audit.md"
	readingGuide = "skills/steer-report/reading.md"
)

func readSkillFile(t *testing.T, rel string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(raw)
}

var showCommand = regexp.MustCompile("steer audit show [A-Z_]+[^`\n]*")

// A reading pass is bounded by command flags: the queue's --limit is each reader's
// case cap, and every show of a case carries the one per-case allowance.
func TestReadingPassBoundsAreCommandFlags(t *testing.T) {
	audit, reading := readSkillFile(t, auditGuide), readSkillFile(t, readingGuide)
	for _, r := range []struct{ reader, limit string }{{"subagent", "12"}, {"inline", "3"}} {
		queue := "steer audit queue --json --limit " + r.limit + "\n"
		if !strings.Contains(reading, queue) {
			t.Errorf("%s does not list the %s reader's cases with %q", readingGuide, r.reader, strings.TrimSpace(queue))
		}
		brief := "reader `" + r.reader + "`, with a case cap of " + r.limit + "."
		if !strings.Contains(strings.Join(strings.Fields(audit), " "), brief) {
			t.Errorf("%s does not start the %s reader with %q", auditGuide, r.reader, brief)
		}
	}
	shows := 0
	for _, f := range []struct{ name, text string }{{auditGuide, audit}, {readingGuide, reading}} {
		for _, cmd := range showCommand.FindAllString(f.text, -1) {
			shows++
			if !strings.Contains(cmd, " --max-bytes 8000") {
				t.Errorf("%s shows a case without the 8000-byte allowance: %q", f.name, cmd)
			}
		}
		for _, total := range []string{"24,000", "subtract"} {
			if strings.Contains(f.text, total) {
				t.Errorf("%s still keeps a running byte total (%q); the per-case allowance bounds a pass", f.name, total)
			}
		}
	}
	if shows == 0 {
		t.Errorf("neither %s nor %s shows a case", auditGuide, readingGuide)
	}
}

// The reader brief returns the account that reading.md's last section describes.
func TestReadingGuideEndsOnTheAccount(t *testing.T) {
	reading := readSkillFile(t, readingGuide)
	headings := regexp.MustCompile(`(?m)^## .+$`).FindAllString(reading, -1)
	if len(headings) == 0 || headings[len(headings)-1] != "## What the pass hands back" {
		t.Errorf("%s's last section is %q, but the brief in %s returns the account as that file's last section",
			readingGuide, headings, auditGuide)
	}
}
