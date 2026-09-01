package report

import (
	"strings"
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

func TestTracebackKeysOnTheExceptionNotTheOpener(t *testing.T) {
	plain := "Traceback (most recent call last):\n" +
		"  File \"<stdin>\", line 3, in <module>\n" +
		"    s = open(p).read()\n" +
		"FileNotFoundError: [Errno 2] No such file or directory: 'cmd/commit_gate.go'"
	if got := tracebackException(plain); got != "FileNotFoundError: [Errno 2] No such file or directory: 'cmd/commit_gate.go'" {
		t.Errorf("plain traceback: %q", got)
	}

	// The outermost exception is reported last, so a chain keys on the second.
	chained := plain + "\n\nDuring handling of the above exception, another exception occurred:\n\n" +
		"Traceback (most recent call last):\n" +
		"  File \"<stdin>\", line 5, in <module>\n" +
		"KeyError: 'subject'"
	if got := tracebackException(chained); got != "KeyError: 'subject'" {
		t.Errorf("chained traceback: %q", got)
	}

	// A traceback whose exception line never arrived keeps the opener.
	if got := tracebackException("Traceback (most recent call last):\n  File \"x.py\", line 1"); got != "" {
		t.Errorf("truncated traceback: %q", got)
	}

	// A traceback indented as a whole reads the same as a bare one. The frames
	// still sit deeper than the opener, so the runner's own banner below the
	// block is not mistaken for the exception.
	indented := "  Traceback (most recent call last):\n" +
		"    File \"x.py\", line 1, in <module>\n" +
		"  ValueError: bad input\n" +
		"FAILED tests/test_x.py"
	if got := tracebackException(indented); got != "ValueError: bad input" {
		t.Errorf("indented traceback: %q", got)
	}

	// A traceback cut off before its exception keeps the opener rather than a
	// runner's banner below it, whatever depth the banner sits at. A banner is
	// not exception-shaped: a dash opens one, a lowercase name another, and a
	// third carries no colon at all.
	for _, banner := range []string{
		"  Traceback (most recent call last):\n    File \"x.py\", line 1\n--- FAIL: TestFoo (0.01s)",
		"Traceback (most recent call last):\n  File \"x.py\", line 1\n--- FAIL: TestFoo (0.01s)",
		"Traceback (most recent call last):\n  File \"x.py\", line 1\nmake: *** [test] Error 1",
		"Traceback (most recent call last):\n  File \"x.py\", line 1\nFAILED tests/test_x.py",
		"Traceback (most recent call last):\n  File \"x.py\", line 1\nexit status 1",
	} {
		if got := tracebackException(banner); got != "" {
			t.Errorf("runner banner read as an exception: %q", got)
		}
	}

	// A dotted exception type is still an exception.
	dotted := "Traceback (most recent call last):\n  File \"x.py\", line 1\n" +
		"mitmproxy.exceptions.AddonManagerError: handler not callable"
	if got := tracebackException(dotted); got != "mitmproxy.exceptions.AddonManagerError: handler not callable" {
		t.Errorf("dotted exception: %q", got)
	}

	// A second, unrelated crash in the same output is not this signal's cause:
	// with no chain note between them, the first traceback's exception stands.
	unrelated := plain + "\nrunning the next step\n" +
		"Traceback (most recent call last):\n  File \"y.py\", line 2\nKeyError: 'other'"
	if got := tracebackException(unrelated); got != "FileNotFoundError: [Errno 2] No such file or directory: 'cmd/commit_gate.go'" {
		t.Errorf("unrelated second crash claimed the key: %q", got)
	}

	// The same holds when the FIRST traceback was killed before its exception.
	// It yielded nothing, and a later unrelated crash still does not name it.
	killedThenOther := "Traceback (most recent call last):\n  File \"x.py\", line 1\n" +
		"Traceback (most recent call last):\n  File \"y.py\", line 2\nKeyError: 'other'"
	if got := tracebackException(killedThenOther); got != "" {
		t.Errorf("second crash named a killed first traceback: %q", got)
	}

	// A line shallower than the opener ends the block, so later output at the
	// opener's own depth is not read as the traceback's exception.
	leftBlock := "  Traceback (most recent call last):\n" +
		"    File \"x.py\", line 1\n" +
		"--- FAIL: TestFoo (0.01s)\n" +
		"  ValueError: from somewhere else entirely"
	if got := tracebackException(leftBlock); got != "" {
		t.Errorf("captured past the end of the block: %q", got)
	}

	// A runner's status marker at the opener's own depth is not an exception:
	// an exception type is CamelCase, and a compiler diagnostic's dotted head
	// names a file extension rather than a module.
	for _, banner := range []string{
		"Traceback (most recent call last):\n  File \"x.py\", line 1\nFAIL: test_case (tests.TestClass)",
		"Traceback (most recent call last):\n  File \"x.py\", line 1\nERROR: test_case",
		"Traceback (most recent call last):\n  File \"x.py\", line 1\nFAILED",
		"Traceback (most recent call last):\n  File \"x.py\", line 1\ntest_foo.py:10: error",
		"Traceback (most recent call last):\n  File \"x.py\", line 1\nbuild.ninja:1: error",
		"Traceback (most recent call last):\n  File \"x.py\", line 1\nlogger.ERROR: connection failed",
		"Traceback (most recent call last):\n  File \"x.py\", line 1\nrunner.FAIL: test_x",
		"Traceback (most recent call last):\n  File \"x.py\", line 1\nTestFoo.C:10: error",
	} {
		if got := tracebackException(banner); got != "" {
			t.Errorf("runner marker read as an exception: %q", got)
		}
	}

	// A lower-case standard-library exception is qualified by its module, which
	// a runner's banner never is.
	for _, want := range []string{
		"re.error: bad escape at position 0",
		"socket.gaierror: [Errno 8] nodename nor servname provided",
		"socket.herror: host not found",
		"ftplib.error_perm: 550 no such file",
		"_InternalError: failed",
		"_ssl._SSLError: certificate verify failed",
	} {
		text := "Traceback (most recent call last):\n  File \"x.py\", line 1\n" + want
		if got := tracebackException(text); got != want {
			t.Errorf("exception %q read as %q", want, got)
		}
	}

	// A chain cut off before its outer exception keeps the inner one, which
	// still names a cause. The opener never does.
	cut := plain + "\n\nDuring handling of the above exception, another exception occurred:\n\n" +
		"Traceback (most recent call last):\n" +
		"  File \"<stdin>\", line 5, in <module>"
	if got := tracebackException(cut); got != "FileNotFoundError: [Errno 2] No such file or directory: 'cmd/commit_gate.go'" {
		t.Errorf("cut chain: %q", got)
	}

	c := NewClassifier()
	c.Classify(transcript.Event{Kind: transcript.KindToolCall, ToolUseID: "t1",
		Tool: "Bash", Target: "python3 - <<'PY'", Shell: true})
	sig, ok := c.Classify(transcript.Event{Kind: transcript.KindToolResult, ToolUseID: "t1", Text: plain})
	if !ok || sig.Kind != kindFailure {
		t.Fatalf("a crashing script is a tool failure: %+v ok=%v", sig, ok)
	}
	if strings.HasPrefix(sig.Key, tracebackOpener) {
		t.Errorf("key still the opener: %q", sig.Key)
	}
	if !strings.HasPrefix(sig.Key, "FileNotFoundError:") {
		t.Errorf("key = %q", sig.Key)
	}

	// matchLine trims, so an INDENTED opener reaches the substitution too. A
	// go test result carrying a subprocess's traceback is the observed shape.
	c2 := NewClassifier()
	c2.Classify(transcript.Event{Kind: transcript.KindToolCall, ToolUseID: "t2",
		Tool: "Bash", Target: "go test ./...", Shell: true})
	sig, ok = c2.Classify(transcript.Event{Kind: transcript.KindToolResult, ToolUseID: "t2",
		Text: "  Traceback (most recent call last):\n" +
			"    File \"h.py\", line 9, in <module>\n" +
			"  FileNotFoundError: [Errno 2] No such file or directory: 'backfill.py'\n" +
			"--- FAIL: TestReplayBackfill (0.06s)"})
	if !ok || !strings.HasPrefix(sig.Key, "FileNotFoundError:") {
		t.Errorf("indented opener through Classify: %q ok=%v", sig.Key, ok)
	}
}
