package report

import (
	"testing"

	"github.com/steer-sh/steer-cli/internal/transcript"
)

func TestShellRunsProgramSeparatesRunnersFromReaders(t *testing.T) {
	cases := []struct {
		cmd  string
		want bool
	}{
		{"go test ./... 2>&1 | head -40", true},
		{"./check.sh 2>&1 | tail -30", true},
		{"cd ~/.claude && bash check.sh", true},
		{"timeout 60 npm run lint", true},
		{"CI=1 make test", true},
		{"sudo docker build .", true},
		{"npx --no-install eslint --max-warnings 0 src", true},
		{"cd cli && python3 -c \"import json\"", true},
		{"cat internal/hookstore/hookstore.go", false},
		{"sed -n '1,80p' internal/report/scan.go", false},
		{"grep -n \"^def \" tests/skill_test.py", false},
		{"ls -la && echo done", false},
		{"git add -A && git commit -F - <<'EOF'\nfeat: go test now passes\nEOF", false},
		{"cat > lib/scorm.test.ts <<'GO'\ngo test ./...\nGO", false},
		{"sudo -u root go test ./...", true},
		{"xargs -I {} pytest {}", true},
		{"/bin/cat internal/hookstore/hookstore.go", false},
		{"/usr/bin/grep -n failed cmd/root.go", false},
		{"/Users/jeremy/.claude/hooks/plan-review.py < /tmp/p.json", true},
		{"echo 'a << b'\ncat notes.md", false},
		{"sudo cat scripts/deploy.sh", false},
		{"timeout 10 cat test.sh", false},
		{"grep \"foo | go test\" build.log", false},
		{"rg \"error; pytest\" docs/", false},
		{"sudo -E go test ./...", true},
		{"nice -n 10 pytest tests/", true},
		{"env -i cat notes.md", false},
		{"sudo -E cat deploy.sh", false},
		{"env -i cat test.py", false},
		{"nice -n 10 cat test.sh", false},
		{"cat {a,b}/deploy.sh", false},
		{"TEST_FILTER=\"test auth\" pytest", true},
		{"CFLAGS=\"-O2 -g\" make test", true},
		{"", false},
	}
	for _, c := range cases {
		if got := shellRunsProgram(c.cmd); got != c.want {
			t.Errorf("shellRunsProgram(%q) = %v; want %v", c.cmd, got, c.want)
		}
	}
}

func TestFailureMatchSkipsAQuotedString(t *testing.T) {
	cases := []struct {
		name string
		text string
		want bool
	}{
		{"source string", "\treturn fmt.Errorf(\"failed to create hooks directory: %w\", err)", false},
		{"prose in backticks", "ACCEPTED | a reviewer citing `run FAILED here` in a note", false},
		{"go test header", "--- FAIL: TestJSONContractIsStable (0.00s)", true},
		{"indented lint line", "/src/a.ts\n  67:34  error  Unexpected any  @typescript-eslint/no-explicit-any", true},
		{"quoted line then a real one", "log(\"a FAILED run\")\nTraceback (most recent call last):", true},
		{"contraction inside backticks", "error in `can't_find_pkg`: npm ERR! build failed", true},
		{"GNU quoting before the match", "make: *** No rule to make target `test'. Stop.\nnpm ERR! build failed", true},
		{"contraction before the match", "couldn't connect to the daemon: npm ERR! build failed", true},
	}
	for _, c := range cases {
		if _, ok := failureMatch(c.text); ok != c.want {
			t.Errorf("%s: failureMatch = %v; want %v", c.name, ok, c.want)
		}
	}
}

func TestClassifierIgnoresAFailureAReadCommandPrinted(t *testing.T) {
	c := NewClassifier()
	c.Classify(transcript.Event{Kind: transcript.KindToolCall, ToolUseID: "t1",
		Tool: "Bash", Target: "cat internal/hookstore/hookstore.go", Shell: true})
	_, ok := c.Classify(transcript.Event{Kind: transcript.KindToolResult, ToolUseID: "t1",
		Text: "\treturn fmt.Errorf(\"failed to create hooks directory: %w\", err)"})
	if ok {
		t.Error("a file a read command printed is not a tool failure")
	}

	c.Classify(transcript.Event{Kind: transcript.KindToolCall, ToolUseID: "t2",
		Tool: "Bash", Target: "go test ./... 2>&1 | head -40", Shell: true})
	sig, ok := c.Classify(transcript.Event{Kind: transcript.KindToolResult, ToolUseID: "t2",
		Text: "--- FAIL: TestJSONContractIsStable (0.00s)\n    status_test.go:31: key set moved"})
	if !ok || sig.Kind != kindFailure {
		t.Fatalf("a failing test run is a tool failure: %+v ok=%v", sig, ok)
	}
	if sig.Key != "--- FAIL: TestJSONContractIsStable (0.00s)" {
		t.Errorf("key = %q", sig.Key)
	}
}
