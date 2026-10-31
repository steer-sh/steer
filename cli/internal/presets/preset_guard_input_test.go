package presets

import (
	"bytes"
	"encoding/json"
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

func TestCommandInjectionGuardForgetsAProofWhenTheNameIsRebound(t *testing.T) {
	source := hookSource(t, "python-command-injection.py")
	dir := withPresetProjectTempDir(t)
	const quoted = "import shlex, subprocess\nx = shlex.quote(a)\n"
	const literal = "import shlex, subprocess\nx = \"ls\"\n"
	const sink = "subprocess.run(f\"ls {x}\", shell=True)\n"

	tests := []struct {
		name    string
		code    string
		blocked bool
	}{
		{"proven name stays safe", quoted + sink, false},
		{"literal name stays safe", literal + sink, false},
		{"plain reassignment", quoted + "x = input()\n" + sink, true},
		{"literal reassigned", literal + "x = input()\n" + sink, true},
		{"chained reassignment", quoted + "y = x = input()\n" + sink, true},
		{"annotated reassignment", quoted + "x: str = input()\n" + sink, true},
		{"tuple target", quoted + "x, y = input(), 1\n" + sink, true},
		{"starred target", quoted + "y, *x = input()\n" + sink, true},
		{"for target", quoted + "for x in input():\n    pass\n" + sink, true},
		{"with target", quoted + "with f() as x:\n    pass\n" + sink, true},
		{"walrus target", quoted + "print(x := input())\n" + sink, true},
		{"function parameter", quoted + "def f(x):\n    " + sink, true},
		{"deleted name", quoted + "del x\n" + sink, true},
		{"augmented with input", quoted + "x += input()\n" + sink, true},
		{"augmented literal with input", literal + "x += input()\n" + sink, true},
		{"percent-formatted with a static string", literal + "x %= \"y\"\n" + sink, true},
		{"reassigned list in a join", "import shlex, subprocess\nq = [shlex.quote(a) for a in b]\nq = input()\nsubprocess.run(f\"ls {' '.join(q)}\", shell=True)\n", true},
		{"call in an augmented subscript target", "import subprocess\na[subprocess.run(input(), shell=True)] += \"x\"\n", true},
		{"walrus inside a value judged after it binds", quoted + "y = f\"{shlex.quote(x := input())} {x}\"\nsubprocess.run(y, shell=True)\n", true},
		{"walrus in an augmented target binds before the value", quoted + "a[(x := input())] += subprocess.check_output(f\"ls {x}\", shell=True)\n", true},
		{"except target", quoted + "try:\n    pass\nexcept Exception as x:\n    " + sink, true},
		{"augmented with a static string", quoted + "x += \"'\"\n" + sink, true},
		{"from-import target", quoted + "from sys import argv as x\n" + sink, true},
		{"import target", quoted + "import x.y\n" + sink, true},
		{"import of another name keeps the proof", quoted + "import sys\n" + sink, false},
		{"reassigned to another proof", quoted + "x = input()\nx = shlex.quote(x)\n" + sink, false},
		{"rebuilt from its own proof", quoted + "x = f\"ls {x}\"\nsubprocess.run(x, shell=True)\n", false},
		{"call in the value reads the old proof", quoted + "x = subprocess.run(f\"ls {x}\", shell=True)\n", false},
		{"call in an augmented value reads the old proof", quoted + "x += subprocess.check_output(f\"ls {x}\", shell=True)\n", false},
		{"call in a walrus value reads the old proof", quoted + "print(x := subprocess.run(f\"ls {x}\", shell=True))\n", false},
		{"call in the value sees no proof the assignment makes", "import shlex, subprocess\nx = shlex.quote(subprocess.check_output(f\"ls {x}\", shell=True))\n", true},
		{"leading program name still trusted", "import shlex, subprocess\nq = [shlex.quote(a) for a in b]\ndef run(command):\n    subprocess.run(f\"{command} {' '.join(q)}\", shell=True)\n", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := map[string]any{"file_path": "a.py", "content": tt.code}
			if got := runGuardPayload(t, source, dir, "Write", input); got != tt.blocked {
				t.Fatalf("blocked = %v, want %v", got, tt.blocked)
			}
		})
	}
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
	for _, name := range []string{"postinstall-script-prevention", "python-command-injection"} {
		if got := manifest.Hooks[name].HookVersion; got != "factory-hook-v2" {
			t.Fatalf("%s manifest hook version = %q, want factory-hook-v2", name, got)
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
