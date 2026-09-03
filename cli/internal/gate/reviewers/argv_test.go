package reviewers

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// stubBinary writes an executable that records the argv it was called with, one
// element per line, and prints reply on stdout. Each reviewer parses a different
// reply, so the caller supplies its own.
func stubBinary(t *testing.T, reply string) (binary, argvFile string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the stub is a shell script")
	}
	dir := t.TempDir()
	binary = filepath.Join(dir, "stub")
	argvFile = filepath.Join(dir, "argv")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argvFile + "\ncat <<'STUBEOF'\n" + reply + "\nSTUBEOF\n"
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return binary, argvFile
}

func recordedArgv(t *testing.T, argvFile string) []string {
	t.Helper()
	data, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
}

func assertArgv(t *testing.T, got, want []string) {
	t.Helper()
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("argv = %q; want %q", got, want)
	}
}

func TestAgyRendersModelAndEffort(t *testing.T) {
	binary, argvFile := stubBinary(t, `{"response":"ok","status":"SUCCESS"}`)
	reviewer := NewAgy("gemini-3.7-flash-high", "high")
	reviewer.Binary = binary
	if run := reviewer.Review(context.Background(), "PROMPT", t.TempDir()); run.Status != "SUCCESS" {
		t.Fatalf("Review = %#v; want a parsed SUCCESS", run)
	}
	assertArgv(t, recordedArgv(t, argvFile), []string{
		"--sandbox", "--dangerously-skip-permissions",
		"--model", "gemini-3.7-flash-high", "--effort", "high",
		"--output-format", "json", "-p", "PROMPT",
	})
}

func TestAgyOmitsAnUnsetEffort(t *testing.T) {
	binary, argvFile := stubBinary(t, `{"response":"ok","status":"SUCCESS"}`)
	reviewer := NewAgy("gemini-3.7-flash-high", "")
	reviewer.Binary = binary
	reviewer.Review(context.Background(), "PROMPT", t.TempDir())
	assertArgv(t, recordedArgv(t, argvFile), []string{
		"--sandbox", "--dangerously-skip-permissions",
		"--model", "gemini-3.7-flash-high", "--output-format", "json", "-p", "PROMPT",
	})
}

// The sandbox boundary is what makes the auto-approval safe. A build that renders one
// without the other hands a reviewer unrestricted tools, so the pair is pinned.
func TestAgyNeverAutoApprovesOutsideTheSandbox(t *testing.T) {
	binary, argvFile := stubBinary(t, `{"response":"ok","status":"SUCCESS"}`)
	reviewer := NewAgy("", "")
	reviewer.Binary = binary
	reviewer.Review(context.Background(), "PROMPT", t.TempDir())
	argv := recordedArgv(t, argvFile)
	var sandboxed, approved bool
	for _, arg := range argv {
		switch arg {
		case "--sandbox":
			sandboxed = true
		case "--dangerously-skip-permissions":
			approved = true
		}
	}
	if approved && !sandboxed {
		t.Fatalf("argv = %q; auto-approval without the sandbox", argv)
	}
	if !approved || !sandboxed {
		t.Fatalf("argv = %q; want both the sandbox and the auto-approval", argv)
	}
}

// agy's own print-timeout must sit above the panel's deadline, or agy stops first and
// the panel's wall clock stops being the one that decides.
func TestAgyGivesItsOwnClockRoomAboveTheDeadline(t *testing.T) {
	binary, argvFile := stubBinary(t, `{"response":"ok","status":"SUCCESS"}`)
	reviewer := NewAgy("", "")
	reviewer.Binary = binary
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
	defer cancel()
	reviewer.Review(ctx, "PROMPT", t.TempDir())
	argv := recordedArgv(t, argvFile)
	value := ""
	for i, arg := range argv {
		if arg == "--print-timeout" && i+1 < len(argv) {
			value = argv[i+1]
		}
	}
	if value == "" {
		t.Fatalf("argv = %q; want a --print-timeout", argv)
	}
	budget, err := time.ParseDuration(value)
	if err != nil {
		t.Fatalf("ParseDuration(%q): %v", value, err)
	}
	if budget <= 100*time.Second {
		t.Fatalf("--print-timeout = %s; want more than the 100s deadline", budget)
	}
}

// A context with no deadline names no wall clock, so agy keeps its own default.
func TestAgyOmitsPrintTimeoutWithoutADeadline(t *testing.T) {
	binary, argvFile := stubBinary(t, `{"response":"ok","status":"SUCCESS"}`)
	reviewer := NewAgy("", "")
	reviewer.Binary = binary
	reviewer.Review(context.Background(), "PROMPT", t.TempDir())
	for _, arg := range recordedArgv(t, argvFile) {
		if arg == "--print-timeout" {
			t.Fatalf("argv carries --print-timeout with no deadline")
		}
	}
}

func TestCodexRendersEffortAsAConfigOverride(t *testing.T) {
	binary, argvFile := stubBinary(t, "findings")
	reviewer := NewCodex("gpt-reviewer", "high")
	reviewer.Binary = binary
	if run := reviewer.Review(context.Background(), "PROMPT", t.TempDir()); run.Response != "findings" {
		t.Fatalf("Review = %#v; want the stdout text", run)
	}
	assertArgv(t, recordedArgv(t, argvFile), []string{
		"exec", "--skip-git-repo-check", "--model", "gpt-reviewer",
		"-c", "model_reasoning_effort=high", "-",
	})
}

// An unset model must reach the CLI as no --model at all, never as another provider's
// pin: the agy default names nothing on Codex.
func TestCodexOmitsAnUnsetModel(t *testing.T) {
	binary, argvFile := stubBinary(t, "findings")
	reviewer := NewCodex("", "low")
	reviewer.Binary = binary
	reviewer.Review(context.Background(), "PROMPT", t.TempDir())
	assertArgv(t, recordedArgv(t, argvFile), []string{
		"exec", "--skip-git-repo-check", "-c", "model_reasoning_effort=low", "-",
	})
}

func TestClaudeCodeRendersModelAndEffort(t *testing.T) {
	binary, argvFile := stubBinary(t, `{"result":"ok","is_error":false}`)
	reviewer := NewClaudeCode("sonnet", "xhigh")
	reviewer.Binary = binary
	if run := reviewer.Review(context.Background(), "PROMPT", t.TempDir()); run.Response != "ok" {
		t.Fatalf("Review = %#v; want the parsed result", run)
	}
	assertArgv(t, recordedArgv(t, argvFile), []string{
		"-p", "--output-format", "json", "--model", "sonnet", "--effort", "xhigh",
	})
}

func TestClaudeCodeOmitsAnUnsetModelAndEffort(t *testing.T) {
	binary, argvFile := stubBinary(t, `{"result":"ok","is_error":false}`)
	reviewer := NewClaudeCode("", "")
	reviewer.Binary = binary
	reviewer.Review(context.Background(), "PROMPT", t.TempDir())
	assertArgv(t, recordedArgv(t, argvFile), []string{"-p", "--output-format", "json"})
}

func TestEffortWarningNamesOnlyADocumentedSet(t *testing.T) {
	cases := []struct {
		provider, effort string
		want             bool
	}{
		{"agy", "high", false},
		{"agy", "xhigh", true},
		{"", "max", true},
		{"claude-code", "max", false},
		{"claude", "xhigh", false},
		{"codex", "anything", false},
		{"agy", "", false},
	}
	for _, c := range cases {
		got := EffortWarning(c.provider, c.effort) != ""
		if got != c.want {
			t.Errorf("EffortWarning(%q, %q) warned = %v; want %v", c.provider, c.effort, got, c.want)
		}
	}
}

// An empty provider is the default reviewer. The warning must still name it, or the
// line reads "is not one of 's documented levels".
func TestEffortWarningNamesTheResolvedProvider(t *testing.T) {
	got := EffortWarning("", "max")
	if !strings.Contains(got, `"agy"`) && !strings.Contains(got, "agy's") {
		t.Fatalf("EffortWarning = %q; want it to name agy", got)
	}
}
