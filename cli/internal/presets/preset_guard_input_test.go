package presets

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/testhome"
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

func hookSource(t *testing.T, name string) string {
	t.Helper()
	source, err := filepath.Abs(filepath.Join("hooks", name))
	if err != nil {
		t.Fatal(err)
	}
	return source
}

func TestPostinstallGuardReachesRawTextCheckForEveryContentShape(t *testing.T) {
	source := hookSource(t, "postinstall-script-prevention.py")
	dir := withPresetProjectTempDir(t)
	withFile := func(fields map[string]any) map[string]any {
		fields["file_path"] = "package.json"
		return fields
	}

	tests := []struct {
		name    string
		tool    string
		input   map[string]any
		blocked bool
	}{
		{"edit renames a key", "Edit", withFile(map[string]any{"new_string": `"postinstall"`}), true},
		{"edit decodes to a list", "Edit", withFile(map[string]any{"new_string": `["postinstall"]`}), true},
		{"edit decodes to a list without the word", "Edit", withFile(map[string]any{"new_string": `["build"]`}), false},
		{"edit decodes to a number", "Edit", withFile(map[string]any{"new_string": `1`}), false},
		{"edit nests past the parser's depth", "Edit", withFile(map[string]any{"new_string": strings.Repeat("[", 200000) + `"postinstall"`}), true},
		{"write with null scripts", "Write", withFile(map[string]any{"content": `{"scripts": null}`}), false},
		{"write with null scripts and the word", "Write", withFile(map[string]any{"content": `{"scripts": null, "postinstall": "x"}`}), true},
		{"write with list scripts", "Write", withFile(map[string]any{"content": `{"scripts": ["postinstall"]}`}), true},
		{"write with numeric scripts", "Write", withFile(map[string]any{"content": `{"scripts": 1}`}), false},
		{"write with an escaped key", "Write", withFile(map[string]any{"content": `{"scripts": {"post\u0069nstall": "x"}}`}), true},
		{"write with safe scripts", "Write", withFile(map[string]any{"content": `{"scripts": {"build": "x"}}`}), false},
		{"non-string new_string falls through to content", "Edit", withFile(map[string]any{"new_string": []string{"a"}, "content": `"postinstall"`}), true},
		{"multiedit skips non-dict entries", "MultiEdit", withFile(map[string]any{"edits": []any{"postinstall", 1, nil, map[string]any{"new_string": `"postinstall": "x"`}}}), true},
		{"multiedit with only non-dict entries", "MultiEdit", withFile(map[string]any{"edits": []any{"postinstall", 1}}), false},
		{"multiedit skips a non-string new_string", "MultiEdit", withFile(map[string]any{"edits": []any{map[string]any{"new_string": 1}, map[string]any{"new_string": `"postinstall"`}}}), true},
		{"multiedit with edits not a list", "MultiEdit", withFile(map[string]any{"edits": "postinstall"}), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := runGuardPayload(t, source, dir, tt.tool, tt.input); got != tt.blocked {
				t.Fatalf("blocked = %v, want %v", got, tt.blocked)
			}
		})
	}
}

const (
	reboundQuoted  = "import shlex, subprocess\nx = shlex.quote(a)\n"
	reboundLiteral = "import shlex, subprocess\nx = \"ls\"\n"
	reboundSink    = "subprocess.run(f\"ls {x}\", shell=True)\n"
)

// reboundProofCases is what the preset visitor and the generation prompt's
// reference visitor must both decide.
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

func TestCommandInjectionGuardForgetsAProofWhenTheNameIsRebound(t *testing.T) {
	source := hookSource(t, "python-command-injection.py")
	runReboundProofCases(t, source, withPresetProjectTempDir(t))
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

func TestReworkedPresetGuardsInstallAtNewVersionIntoMaster(t *testing.T) {
	dir := withPresetProjectTempDir(t)
	testhome.Set(t, dir)
	catalog, err := LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"postinstall-script-prevention", "python-command-injection"} {
		item, ok := catalog.Find(name)
		if !ok {
			t.Fatalf("%s missing from catalog", name)
		}
		if _, err := EnablePreset(claude.ProjectScope, catalog, item, false); err != nil {
			t.Fatal(err)
		}
	}
	manifest, err := claude.LoadManifest(claude.ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"postinstall-script-prevention": "factory-hook-v2",
		"python-command-injection":      "factory-hook-v3",
	} {
		if got := manifest.Hooks[name].HookVersion; got != want {
			t.Fatalf("%s manifest hook version = %q, want %s", name, got, want)
		}
	}

	master := filepath.Join(dir, ".steer", "hooks", "steer-hooks.py")
	cases := []struct {
		name    string
		tool    string
		input   map[string]any
		blocked bool
	}{
		{"bare key", "Edit", map[string]any{"file_path": "package.json", "new_string": `"postinstall"`}, true},
		{"null scripts", "Write", map[string]any{"file_path": "package.json", "content": `{"scripts": null}`}, false},
		{"multiedit keeps dict entries", "MultiEdit", map[string]any{"file_path": "package.json", "edits": []any{"x", map[string]any{"new_string": `"postinstall"`}}}, true},
		{"rebound proof", "Write", map[string]any{"file_path": "a.py", "content": "import shlex, subprocess\nx = shlex.quote(a)\nx = input()\nsubprocess.run(f\"ls {x}\", shell=True)\n"}, true},
		{"kept proof", "Write", map[string]any{"file_path": "a.py", "content": "import shlex, subprocess\nx = shlex.quote(a)\nsubprocess.run(f\"ls {x}\", shell=True)\n"}, false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := runGuardPayload(t, master, dir, tt.tool, tt.input); got != tt.blocked {
				t.Fatalf("blocked = %v, want %v", got, tt.blocked)
			}
		})
	}
}
