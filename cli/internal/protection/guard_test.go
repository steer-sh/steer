package protection

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// guardRuntime is a home holding the guard in its global store, and a repository
// beside it.
type guardRuntime struct {
	home, codex, repo, hookPath string
}

func setupGuardRuntime(t *testing.T) guardRuntime {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	rt := guardRuntime{
		home:  filepath.Join(root, "home"),
		codex: filepath.Join(root, "codex-home"),
		repo:  filepath.Join(root, "repo"),
	}
	store := filepath.Join(rt.home, ".steer", "hooks")
	for _, dir := range []string{store, rt.codex, filepath.Join(rt.repo, ".git"), filepath.Join(rt.repo, "src")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	rt.hookPath = filepath.Join(store, HookName+".py")
	if err := os.WriteFile(rt.hookPath, Desired().Content, 0o755); err != nil {
		t.Fatal(err)
	}
	return rt
}

// run sends one payload to the guard from cwd and reports whether it blocked.
func (rt guardRuntime) run(t *testing.T, cwd string, payload map[string]any) (bool, string) {
	t.Helper()
	payload["cwd"] = cwd
	input, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("python3", rt.hookPath)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), "HOME="+rt.home, "CODEX_HOME="+rt.codex)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	if err != nil {
		t.Fatalf("guard failed: %v\n%s", err, stderr.String())
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return false, ""
	}
	var out struct {
		Decision   string `json:"decision"`
		StopReason string `json:"stopReason"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("guard output %q: %v", raw, err)
	}
	return out.Decision == "block", out.StopReason
}

func bash(command string) map[string]any {
	return map[string]any{"tool_name": "Bash", "tool_input": map[string]any{"command": command}}
}

func edit(path string) map[string]any {
	return map[string]any{"tool_name": "Edit", "tool_input": map[string]any{"file_path": path, "new_string": "{}"}}
}

// Every protected file is matched by its full path, whether the agent names it
// absolutely, through ~ or relative to its working directory in the repository,
// and a file that only shares a protected file's name is the agent's to change.
// Steer's own files are closed to the Edit tool too; a harness configuration file
// is closed to a shell write, whose block sends the agent to Edit or Write.
func TestSelfProtectionBlocksSteerStateByFullPath(t *testing.T) {
	rt := setupGuardRuntime(t)
	store := filepath.Join(rt.home, ".steer", "hooks")
	steerFiles := []string{
		filepath.Join(store, "steer-hooks.py"),
		filepath.Join(store, "steer-manifest.json"),
		filepath.Join(store, "steer-audit.jsonl"),
		filepath.Join(store, HookName+".py"),
		filepath.Join(rt.home, ".steer", "config.json"),
		filepath.Join(rt.repo, ".steer", "hooks", "steer-manifest.json"),
		filepath.Join(rt.repo, ".claude", "hooks", "steer-hooks.py"),
		filepath.Join(rt.repo, "steer-team.lock.json"),
	}
	for _, path := range steerFiles {
		t.Run(path, func(t *testing.T) {
			if blocked, reason := rt.run(t, rt.repo, edit(path)); !blocked || !strings.Contains(reason, "ask the user to run steer ") {
				t.Errorf("an Edit of %s was allowed or names no steer command: %q", path, reason)
			}
			if blocked, _ := rt.run(t, rt.repo, bash("echo x > "+path)); !blocked {
				t.Errorf("a redirection into %s was allowed", path)
			}
		})
	}
	harnessFiles := []string{
		filepath.Join(rt.home, ".claude", "settings.json"),
		filepath.Join(rt.codex, "hooks.json"),
		filepath.Join(rt.codex, "config.toml"),
		filepath.Join(rt.repo, ".claude", "settings.json"),
		filepath.Join(rt.repo, ".claude", "settings.local.json"),
		filepath.Join(rt.repo, ".codex", "hooks.json"),
	}
	for _, path := range harnessFiles {
		t.Run(path, func(t *testing.T) {
			blocked, reason := rt.run(t, rt.repo, bash("jq '.x = 1' /tmp/in.json > "+path))
			if !blocked || !strings.Contains(reason, "Edit or Write tool") {
				t.Errorf("a shell write to %s was allowed or names no retry: %q", path, reason)
			}
		})
	}
	relative := []struct{ cwd, command string }{
		{rt.repo, "rm .claude/settings.json"},
		{filepath.Join(rt.repo, "src"), "sed -i 's/a/b/' ../steer-team.lock.json"},
		{rt.repo, "echo done; rm .steer/hooks/steer-hooks.py && steer list"},
		{rt.repo, "cp /dev/null ~/.steer/config.json"},
		{rt.repo, "cat ~/.steer/config.json && sudo rm -f .claude/settings.json"},
		{rt.repo, "mv .codex/hooks.json /tmp/hooks.json"},
		{rt.repo, "FOO=1 sed -i '' 's/a/b/' steer-team.lock.json"},
		{rt.repo, "dd if=/dev/null of=.steer/hooks/steer-manifest.json"},
		{rt.repo, "echo x | tee -a .claude/settings.json"},
		{rt.repo, "true;rm .claude/settings.json"},
		{rt.repo, "rm -rf ~/.steer/hooks"},
		{rt.repo, "rm -rf .steer"},
		{rt.repo, "mv .claude /tmp/old-claude"},
		{rt.repo, "cp /tmp/settings.json .claude/"},
		{rt.repo, "echo '{}' > \"steer-team.lock.json\""},
	}
	for _, tc := range relative {
		if blocked, _ := rt.run(t, tc.cwd, bash(tc.command)); !blocked {
			t.Errorf("%q from %s was allowed", tc.command, tc.cwd)
		}
	}
	if blocked, _ := rt.run(t, filepath.Join(rt.repo, "src"), edit("../steer-team.lock.json")); !blocked {
		t.Error("an Edit of the team lockfile by a relative path was allowed")
	}

	namesakes := []map[string]any{
		edit(filepath.Join(rt.repo, ".vscode", "settings.json")),
		edit(filepath.Join(rt.repo, "src", "hooks.json")),
		edit(filepath.Join(rt.repo, "src", "steer-manifest.json")),
		bash("rm settings.json config.json hooks.json"),
		bash("echo x > .vscode/settings.json"),
		bash("cat ~/.steer/config.json .claude/settings.json"),
		bash("cp ~/.steer/config.json /tmp/steer-config-backup"),
		bash("cat ~/.steer/config.json && touch notes.txt"),
		bash("grep x .claude/settings.json > /tmp/out 2>&1"),
		bash("diff .codex/hooks.json /tmp/hooks.json; rm -f /tmp/hooks.json"),
		bash("grep '>' .claude/settings.json"),
		bash("echo '>' ~/.steer/config.json"),
		bash("rm -rf src/build"),
		bash("cp README.md .claude/"),
		bash("chmod -R u+w ."),
	}
	for _, payload := range namesakes {
		if blocked, reason := rt.run(t, rt.repo, payload); blocked {
			t.Errorf("%v was blocked: %s", payload["tool_input"], reason)
		}
	}
}

func TestSelfProtectionReadsEverySteerInvocationPastFlagsAndPunctuation(t *testing.T) {
	rt := setupGuardRuntime(t)
	tests := []struct {
		name    string
		command string
		blocked bool
	}{
		{"turning the setting off", "steer config set protection.self off", true},
		{"clearing the setting", "steer config set protection.self ''", true},
		{"the setting after a root flag", "steer --harness codex config set protection.self off", true},
		{"the setting in a second invocation", "steer config show && steer config set protection.self off", true},
		{"removing the guard", "steer remove steer-self-protection", true},
		{"removing the guard by its file", "steer remove --scope global ~/.steer/hooks/steer-self-protection.py", true},
		{"removing the guard after a value flag", "steer remove --reason cleanup steer-self-protection; true", true},
		{"removing the guard after a redirection", "steer remove 2>/dev/null steer-self-protection", true},
		{"removing the guard in a subshell", "(steer remove steer-self-protection)", true},
		{"removing the guard by a path to steer", "steer list && ./bin/steer remove steer-self-protection", true},
		{"registering over the guard", "steer register --rule x ./steer-self-protection.py", true},
		{"disabling the guard as a preset", "steer --scope global preset disable steer-self-protection", true},
		{"an unbalanced quote later on", "steer remove steer-self-protection; echo \"", true},
		{"another setting", "steer config set reviewer.effort high", false},
		{"reading the setting", "steer config get protection.self", false},
		{"the key as another setting's value", "steer config set reviewer.model protection.self", false},
		{"removing another guard", "steer remove --reason steer-self-protection user-custom", false},
		{"disabling an orphan preset", "steer preset disable sql-injection-prevention", false},
		{"pruning orphans", "steer preset sync --prune-orphans", false},
		{"a rule naming the removal", "steer register --rule \"steer remove steer-self-protection\" ./user-custom.py", false},
		{"the name in the next command", "steer remove --force; echo steer-self-protection", false},
		{"team commands", "steer team apply --force && steer team verify --ci", false},
		{"a directory named steer", "cd steer && steer list", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			blocked, reason := rt.run(t, rt.repo, bash(tc.command))
			if blocked != tc.blocked {
				t.Fatalf("blocked = %v, want %v (reason %q)", blocked, tc.blocked, reason)
			}
		})
	}
}

// The reason names what was blocked, never a command the agent could run instead.
func TestSelfProtectionReasonLeavesTheChangeToTheUser(t *testing.T) {
	rt := setupGuardRuntime(t)
	_, reason := rt.run(t, rt.repo, bash("steer config set protection.self off"))
	if !strings.Contains(reason, "Only the user") || strings.Contains(reason, OffCommand) {
		t.Fatalf("reason = %q", reason)
	}
}

// writeFile writes content at path, creating its directory.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func write(path, content string) map[string]any {
	return map[string]any{"tool_name": "Write", "tool_input": map[string]any{"file_path": path, "content": content}}
}

func editText(path, old, new string) map[string]any {
	return map[string]any{"tool_name": "Edit", "tool_input": map[string]any{"file_path": path, "old_string": old, "new_string": new}}
}

// A harness configuration file stays the agent's to edit: an Edit or Write that
// keeps Steer's entries passes, and one that removes one, sets disableAllHooks,
// deletes the file, or turns a Steer hook off in Codex's config.toml is blocked.
func TestSelfProtectionGuardsSteersEntriesInHarnessFiles(t *testing.T) {
	rt := setupGuardRuntime(t)
	settings := filepath.Join(rt.home, ".claude", "settings.json")
	master := filepath.Join(rt.home, ".steer", "hooks", "steer-hooks.py")
	gate := filepath.Join(rt.home, ".steer", "bin", "steer") + " commit-gate hook --harness claude-code"
	original := `{
  "permissions": {
    "allow": [
      "Bash(ls:*)"
    ]
  },
  "hooks": {
    "PreToolUse": [
      {"matcher": "*", "hooks": [{"type": "command", "command": "python3 ` + master + `"}]},
      {"matcher": "Bash", "hooks": [{"type": "command", "command": "` + gate + `"}]},
      {"matcher": "Bash", "hooks": [{"type": "command", "command": "my-own-hook.sh"}]}
    ]
  }
}
`
	writeFile(t, settings, original)
	withoutGate := strings.Replace(original, `
      {"matcher": "Bash", "hooks": [{"type": "command", "command": "`+gate+`"}]},`, "", 1)
	withoutOwn := strings.Replace(original, `,
      {"matcher": "Bash", "hooks": [{"type": "command", "command": "my-own-hook.sh"}]}`, "", 1)
	local := filepath.Join(rt.repo, ".claude", "settings.local.json")
	writeFile(t, local, "{\n  \"env\": {}\n}\n")

	codexHooks := filepath.Join(rt.codex, "hooks.json")
	writeFile(t, codexHooks, `{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"python3 `+master+` --harness codex"}]}]}}`)
	config := filepath.Join(rt.codex, "config.toml")
	section := `[hooks.state."` + codexHooks + `:pre_tool_use:0:0"]`
	writeFile(t, config, "model = \"m\"\n\n"+section+"\ntrusted_hash = \"sha256:abc\"\n")

	cases := []struct {
		name    string
		payload map[string]any
		blocked bool
	}{
		{"a permission added by Edit", editText(settings, `"Bash(ls:*)"`, `"Bash(ls:*)",
      "Bash(git status:*)"`), false},
		{"the agent's own hook removed by Write", write(settings, withoutOwn), false},
		{"a Write that keeps Steer's entries", write(settings, strings.Replace(original, "Bash(ls:*)", "Bash(pwd)", 1)), false},
		{"a Steer hook removed by Edit", editText(settings, `
      {"matcher": "Bash", "hooks": [{"type": "command", "command": "`+gate+`"}]},`, ""), true},
		{"the master's entry cut down by Edit", editText(settings, "python3 "+master, "true"), true},
		{"a Write that drops Steer's entry", write(settings, withoutGate), true},
		{"a Write the file cannot parse that drops Steer's entry", write(settings, "{ broken"), true},
		{"a Write the file cannot parse that keeps Steer's entries", write(settings, original+"\n,"), false},
		{"disableAllHooks in settings.local.json", editText(local, `"env": {}`, `"env": {},
  "disableAllHooks": true`), true},
		{"disableAllHooks written to settings.json", write(settings, strings.Replace(original, "{\n", "{\n  \"disableAllHooks\": true,\n", 1)), true},
		{"the file deleted by a patch", map[string]any{"tool_name": "Edit", "tool_input": map[string]any{
			"file_path": settings, "steer_operation": "delete", "new_string": ""}}, true},
		{"a Codex model change", editText(config, `model = "m"`, `model = "n"`), false},
		{"Codex hooks turned off", editText(config, `model = "m"`, "model = \"m\"\n\n[features]\nhooks = false"), true},
		{"Codex's approval of Steer's hook removed", editText(config, `trusted_hash = "sha256:abc"`, ""), true},
		{"Steer's Codex hook disabled", editText(config, `trusted_hash = "sha256:abc"`, "trusted_hash = \"sha256:abc\"\nenabled = false"), true},
		{"Steer's Codex entry removed", editText(codexHooks, "python3 "+master+" --harness codex", "true"), true},
		{"a Steer hook moved to another matcher", editText(settings, `{"matcher": "Bash", "hooks": [{"type": "command", "command": "`+gate,
			`{"matcher": "NoSuchTool", "hooks": [{"type": "command", "command": "`+gate), true},
		{"Codex hooks off in an inline table", write(config, "features = { hooks = false }\n"+section+"\ntrusted_hash = \"sha256:abc\"\n"), true},
		{"Codex hooks off under a quoted table", write(config, "[\"features\"]\n\"hooks\" = false\n"+section+"\ntrusted_hash = \"sha256:abc\"\n"), true},
		{"a patch hunk adding hooks = false out of context", editText(config, "", "hooks = false"), true},
		{"Codex hooks off with a comment and literal quotes", write(config, "['features']\n'hooks' = false # off\n"+section+"\ntrusted_hash = \"sha256:abc\"\n"), true},
		{"Codex hooks off by a quoted dotted key", write(config, "features.\"hooks\" = false\n"+section+"\ntrusted_hash = \"sha256:abc\"\n"), true},
		{"a Steer handler's type changed", editText(settings, `"type": "command", "command": "`+gate, `"type": "prompt", "command": "`+gate), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			blocked, reason := rt.run(t, rt.repo, tc.payload)
			if blocked != tc.blocked {
				t.Fatalf("blocked = %v, want %v (reason %q)", blocked, tc.blocked, reason)
			}
		})
	}
}
