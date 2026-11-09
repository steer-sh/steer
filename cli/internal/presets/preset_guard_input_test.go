package presets

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runGuardPayload runs a guard or master on one payload. A non-zero exit fails
// the test, so a guard that raises cannot read as an allow.
func runGuardPayload(t *testing.T, path, dir, tool string, input map[string]any) bool {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"tool_name": tool, "tool_input": input})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("python3", path)
	cmd.Dir = dir
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("running %s: %v\n%s", path, err, stderr.String())
	}
	if len(output) == 0 {
		return false
	}
	var reply struct {
		Decision           string `json:"decision"`
		HookSpecificOutput struct {
			PermissionDecision string `json:"permissionDecision"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(output, &reply); err != nil {
		t.Fatalf("parsing reply %q: %v", output, err)
	}
	return reply.Decision == "block" || reply.HookSpecificOutput.PermissionDecision == "deny"
}

const (
	reboundQuoted  = "import shlex, subprocess\nx = shlex.quote(a)\n"
	reboundLiteral = "import shlex, subprocess\nx = \"ls\"\n"
	reboundSink    = "subprocess.run(f\"ls {x}\", shell=True)\n"
)

// reboundProofCases is what the generation prompt's reference visitor must decide.
var reboundProofCases = []struct {
	name    string
	code    string
	blocked bool
}{
	{"proven name stays safe", reboundQuoted + reboundSink, false},
	{"literal name stays safe", reboundLiteral + reboundSink, false},
	{"plain reassignment", reboundQuoted + "x = input()\n" + reboundSink, true},
	{"literal reassigned", reboundLiteral + "x = input()\n" + reboundSink, true},
	{"chained reassignment", reboundQuoted + "y = x = input()\n" + reboundSink, true},
	{"annotated reassignment", reboundQuoted + "x: str = input()\n" + reboundSink, true},
	{"tuple target", reboundQuoted + "x, y = input(), 1\n" + reboundSink, true},
	{"starred target", reboundQuoted + "y, *x = input()\n" + reboundSink, true},
	{"for target", reboundQuoted + "for x in input():\n    pass\n" + reboundSink, true},
	{"with target", reboundQuoted + "with f() as x:\n    pass\n" + reboundSink, true},
	{"walrus target", reboundQuoted + "print(x := input())\n" + reboundSink, true},
	{"function parameter", reboundQuoted + "def f(x):\n    " + reboundSink, true},
	{"deleted name", reboundQuoted + "del x\n" + reboundSink, true},
	{"augmented with input", reboundQuoted + "x += input()\n" + reboundSink, true},
	{"augmented literal with input", reboundLiteral + "x += input()\n" + reboundSink, true},
	{"percent-formatted with a static string", reboundLiteral + "x %= \"y\"\n" + reboundSink, true},
	{"reassigned list in a join", "import shlex, subprocess\nq = [shlex.quote(a) for a in b]\nq = input()\nsubprocess.run(f\"ls {' '.join(q)}\", shell=True)\n", true},
	{"call in an augmented subscript target", "import subprocess\na[subprocess.run(input(), shell=True)] += \"x\"\n", true},
	{"walrus inside a value judged after it binds", reboundQuoted + "y = f\"{shlex.quote(x := input())} {x}\"\nsubprocess.run(y, shell=True)\n", true},
	{"walrus in an augmented target binds before the value", reboundQuoted + "a[(x := input())] += subprocess.check_output(f\"ls {x}\", shell=True)\n", true},
	{"except target", reboundQuoted + "try:\n    pass\nexcept Exception as x:\n    " + reboundSink, true},
	{"augmented with a static string", reboundQuoted + "x += \"'\"\n" + reboundSink, true},
	{"from-import target", reboundQuoted + "from sys import argv as x\n" + reboundSink, true},
	{"import target", reboundQuoted + "import x.y\n" + reboundSink, true},
	{"function name", reboundQuoted + "def x():\n    pass\n" + reboundSink, true},
	{"class name", reboundQuoted + "class x:\n    pass\n" + reboundSink, true},
	{"annotation runs after the rebinding", reboundQuoted + "x: subprocess.run(f\"ls {x}\", shell=True) = input()\n", true},
	{"call in a bare annotated subscript target", "import subprocess\na[subprocess.run(input(), shell=True)]: str\n", true},
	{"function body cannot restore a proof for its name", reboundQuoted + "def x():\n    x = shlex.quote(a)\n" + reboundSink, true},
	{"function default reads the old proof", reboundQuoted + "def x(b=subprocess.run(f\"ls {x}\", shell=True)):\n    pass\n", false},
	{"bare annotation keeps the proof", reboundQuoted + "x: str\n" + reboundSink, false},
	{"call in an annotated value reads the old proof", reboundQuoted + "x: str = subprocess.run(f\"ls {x}\", shell=True)\n", false},
	{"import of another name keeps the proof", reboundQuoted + "import sys\n" + reboundSink, false},
	{"reassigned to another proof", reboundQuoted + "x = input()\nx = shlex.quote(x)\n" + reboundSink, false},
	{"rebuilt from its own proof", reboundQuoted + "x = f\"ls {x}\"\nsubprocess.run(x, shell=True)\n", false},
	{"call in the value reads the old proof", reboundQuoted + "x = subprocess.run(f\"ls {x}\", shell=True)\n", false},
	{"call in an augmented value reads the old proof", reboundQuoted + "x += subprocess.check_output(f\"ls {x}\", shell=True)\n", false},
	{"call in a walrus value reads the old proof", reboundQuoted + "print(x := subprocess.run(f\"ls {x}\", shell=True))\n", false},
	{"call in the value sees no proof the assignment makes", "import shlex, subprocess\nx = shlex.quote(subprocess.check_output(f\"ls {x}\", shell=True))\n", true},
	{"leading program name still trusted", "import shlex, subprocess\nq = [shlex.quote(a) for a in b]\ndef run(command):\n    subprocess.run(f\"{command} {' '.join(q)}\", shell=True)\n", false},
	{"subprocess.run with shell", "import subprocess\nsubprocess.run(input(), shell=True)\n", true},
	{"subprocess.Popen with shell", "import subprocess\nsubprocess.Popen(input(), shell=True)\n", true},
	{"subprocess.check_output with shell", "import subprocess\nsubprocess.check_output(input(), shell=True)\n", true},
	{"subprocess.check_call with shell", "import subprocess\nsubprocess.check_call(input(), shell=True)\n", true},
	{"subprocess.call with shell", "import subprocess\nsubprocess.call(input(), shell=True)\n", true},
	{"subprocess.run args keyword with shell", "import subprocess\nsubprocess.run(args=input(), shell=True)\n", true},
	{"subprocess.Popen args keyword with shell", "import subprocess\nsubprocess.Popen(args=input(), shell=True)\n", true},
	{"subprocess.check_output args keyword with shell", "import subprocess\nsubprocess.check_output(args=input(), shell=True)\n", true},
	{"subprocess.check_call args keyword with shell", "import subprocess\nsubprocess.check_call(args=input(), shell=True)\n", true},
	{"subprocess.call args keyword with shell", "import subprocess\nsubprocess.call(shell=True, args=input())\n", true},
	{"os.system command keyword", "import os\nos.system(command=input())\n", true},
	{"args keyword with a literal command", "import subprocess\nsubprocess.call(args=\"ls -l\", shell=True)\n", false},
	{"args keyword with a proven name", reboundQuoted + "subprocess.run(args=f\"ls {x}\", shell=True)\n", false},
	{"args keyword list without shell", "import subprocess\nsubprocess.run(args=[\"ls\", input()])\n", false},
	{"keyword before an args keyword rebinds the name", reboundQuoted + "subprocess.run(cwd=(x := input()), args=f\"ls {x}\", shell=True)\n", true},
	{"keyword after an args keyword reads the old proof", reboundQuoted + "subprocess.run(args=f\"ls {x}\", shell=True, cwd=(x := input()))\n", false},
	{"os.system command keyword literal", "import os\nos.system(command=\"ls\")\n", false},
}

// reboundMatchCases need match syntax, which Python before 3.10 cannot parse.
var reboundMatchCases = []struct {
	name string
	code string
}{
	{"match capture", reboundQuoted + "match input():\n    case x:\n        " + reboundSink},
	{"match star capture", reboundQuoted + "match input():\n    case [*x]:\n        " + reboundSink},
	{"match mapping rest", reboundQuoted + "match input():\n    case {**x}:\n        " + reboundSink},
}

func runReboundProofCases(t *testing.T, guard, dir string) {
	t.Helper()
	check := func(t *testing.T, code string, blocked bool) {
		t.Helper()
		input := map[string]any{"file_path": "a.py", "content": code}
		if got := runGuardPayload(t, guard, dir, "Write", input); got != blocked {
			t.Fatalf("blocked = %v, want %v", got, blocked)
		}
	}
	for _, tt := range reboundProofCases {
		t.Run(tt.name, func(t *testing.T) { check(t, tt.code, tt.blocked) })
	}
	hasMatch := exec.Command("python3", "-c", "import sys; sys.exit(sys.version_info < (3, 10))").Run() == nil
	for _, tt := range reboundMatchCases {
		t.Run(tt.name, func(t *testing.T) {
			if !hasMatch {
				t.Skip("python3 predates match syntax")
			}
			check(t, tt.code, true)
		})
	}
}

// promptVisitorDriver makes the extracted reference visitor a guard: any
// violation is a block, and an exception is a non-zero exit.
const promptVisitorDriver = `
data = json.load(sys.stdin)
visitor = CommandInjectionVisitor()
visitor.visit(ast.parse(data["tool_input"]["content"]))
if visitor.violations:
    print(json.dumps({"decision": "block"}))
`

// writePromptVisitorGuard writes the fenced visitor under the prompt's
// "Reference AST Visitor" heading into dir as a runnable guard.
func writePromptVisitorGuard(t *testing.T, promptPath, dir string) string {
	t.Helper()
	raw, err := os.ReadFile(promptPath)
	if err != nil {
		t.Fatal(err)
	}
	_, section, found := strings.Cut(string(raw), "### Reference AST Visitor\n")
	if !found {
		t.Fatalf("%s has no Reference AST Visitor section", promptPath)
	}
	_, fenced, found := strings.Cut(section, "```python\n")
	if !found {
		t.Fatalf("%s has no python fence under the section", promptPath)
	}
	visitor, _, found := strings.Cut(fenced, "\n```")
	if !found || !strings.Contains(visitor, "class CommandInjectionVisitor") {
		t.Fatalf("%s: the fence holds no closed CommandInjectionVisitor", promptPath)
	}
	guard := filepath.Join(dir, "prompt_visitor.py")
	script := "import ast, json, sys\n" + visitor + "\n" + promptVisitorDriver
	if err := os.WriteFile(guard, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	return guard
}

func TestPythonSecurityPromptVisitorForgetsAProofWhenTheNameIsRebound(t *testing.T) {
	prompt, err := filepath.Abs(filepath.Join("..", "..", "..", "skills", "steer", "prompts", "python-security.md"))
	if err != nil {
		t.Fatal(err)
	}
	dir := withPresetProjectTempDir(t)
	runReboundProofCases(t, writePromptVisitorGuard(t, prompt, t.TempDir()), dir)
}
