package reviewers

import (
	"context"
	"encoding/json"
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
		"--model", "gemini-3.7-flash-high", "--output-format", "json", "-p", "PROMPT",
	})
}

// The gate grants the reviewer no permissions of its own. Auto-approval turned the
// reviewer into an explorer that answered on none of three runs, so the argv must
// never carry it again.
func TestAgyGrantsTheReviewerNoPermissions(t *testing.T) {
	binary, argvFile := stubBinary(t, `{"response":"ok","status":"SUCCESS"}`)
	reviewer := NewAgy("", "")
	reviewer.Binary = binary
	reviewer.Review(context.Background(), "PROMPT", t.TempDir())
	for _, arg := range recordedArgv(t, argvFile) {
		if arg == "--dangerously-skip-permissions" {
			t.Fatalf("argv auto-approves the reviewer's tool calls")
		}
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
		"exec", "--skip-git-repo-check", "--ephemeral", "--sandbox", "read-only", "--disable", "hooks", "--disable", "apps",
		"--model", "gpt-reviewer",
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
		"exec", "--skip-git-repo-check", "--ephemeral", "--sandbox", "read-only", "--disable", "hooks", "--disable", "apps",
		"-c", "model_reasoning_effort=low", "-",
	})
}

// A review session that loads the operator's hooks runs their turn-end check.sh
// inside the review, which holds the review past its deadline in an adopted repository.
// The codex_apps connectors reach outside services the sandbox does not bind.
func TestCodexRunsWithoutOperatorHooksInAReadOnlySandbox(t *testing.T) {
	binary, argvFile := stubBinary(t, "findings")
	reviewer := NewCodex("", "")
	reviewer.Binary = binary
	reviewer.Review(context.Background(), "PROMPT", t.TempDir())
	argv := strings.Join(recordedArgv(t, argvFile), " ")
	for _, want := range []string{"--disable hooks", "--disable apps", "--sandbox read-only"} {
		if !strings.Contains(argv, want) {
			t.Fatalf("argv = %q; want %q", argv, want)
		}
	}
}

func TestClaudeCodeRendersModelAndEffort(t *testing.T) {
	binary, argvFile := stubBinary(t, `{"result":"ok","is_error":false}`)
	reviewer := NewClaudeCode("sonnet", "xhigh")
	reviewer.Binary = binary
	reviewer.UserSettings = filepath.Join(t.TempDir(), "absent.json")
	if run := reviewer.Review(context.Background(), "PROMPT", t.TempDir()); run.Response != "ok" {
		t.Fatalf("Review = %#v; want the parsed result", run)
	}
	assertArgv(t, recordedArgv(t, argvFile), []string{
		"-p", "--output-format", "json", "--no-session-persistence", "--safe-mode", "--tools", "Read,Grep,Glob", "--restricted",
		"--model", "sonnet", "--effort", "xhigh",
	})
}

func TestClaudeCodeOmitsAnUnsetModelAndEffort(t *testing.T) {
	binary, argvFile := stubBinary(t, `{"result":"ok","is_error":false}`)
	reviewer := NewClaudeCode("", "")
	reviewer.Binary = binary
	reviewer.UserSettings = filepath.Join(t.TempDir(), "absent.json")
	reviewer.Review(context.Background(), "PROMPT", t.TempDir())
	assertArgv(t, recordedArgv(t, argvFile), []string{
		"-p", "--output-format", "json", "--no-session-persistence", "--safe-mode", "--tools", "Read,Grep,Glob", "--restricted",
	})
}

// The review session loads none of the operator's hooks, its only tools read files, and
// those reads stay inside the working directory.
func TestClaudeCodeRunsWithoutOperatorHooksAndWithReadOnlyTools(t *testing.T) {
	binary, argvFile := stubBinary(t, `{"result":"ok","is_error":false}`)
	reviewer := NewClaudeCode("", "")
	reviewer.Binary = binary
	reviewer.UserSettings = filepath.Join(t.TempDir(), "absent.json")
	reviewer.Review(context.Background(), "PROMPT", t.TempDir())
	argv := strings.Join(recordedArgv(t, argvFile), " ")
	for _, want := range []string{"--safe-mode", "--tools Read,Grep,Glob", "--restricted"} {
		if !strings.Contains(argv, want) {
			t.Fatalf("argv = %q; want %q", argv, want)
		}
	}
}

// --restricted skips the user's settings file, so its sign-in keys reach the review
// through a --settings file, and nothing else in it does.
func TestClaudeCodeForwardsOnlyTheSignInSettings(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stub is a shell script")
	}
	dir := t.TempDir()
	user := filepath.Join(dir, "settings.json")
	body := `{"env":{"CLAUDE_CODE_USE_BEDROCK":"1"},"apiKeyHelper":"/bin/key",` +
		`"permissions":{"allow":["Read(//**)"]},"model":"haiku"}`
	if err := os.WriteFile(user, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	copied := filepath.Join(dir, "forwarded.json")
	binary := filepath.Join(dir, "stub")
	script := "#!/bin/sh\nwhile [ $# -gt 0 ]; do\n  if [ \"$1\" = --settings ]; then cp \"$2\" " + copied +
		"; ls -l \"$2\" > " + copied + ".mode; echo \"$2\" > " + copied + ".path; fi\n  shift\ndone\n" +
		"echo '{\"result\":\"ok\",\"is_error\":false}'\n"
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	reviewer := NewClaudeCode("", "")
	reviewer.Binary = binary
	reviewer.UserSettings = user
	reviewer.Review(context.Background(), "PROMPT", t.TempDir())

	data, err := os.ReadFile(copied)
	if err != nil {
		t.Fatalf("no --settings file reached the reviewer: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got["apiKeyHelper"] != "/bin/key" || got["env"] == nil {
		t.Fatalf("forwarded settings = %s; want only env and apiKeyHelper", data)
	}
	if mode, _ := os.ReadFile(copied + ".mode"); !strings.HasPrefix(string(mode), "-rw-------") {
		t.Fatalf("forwarded settings mode = %q; want readable by the user only", mode)
	}
	path, _ := os.ReadFile(copied + ".path")
	if _, err := os.Stat(strings.TrimSpace(string(path))); !os.IsNotExist(err) {
		t.Fatalf("forwarded settings %s outlived the review: %v", path, err)
	}
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
