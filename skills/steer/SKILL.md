---
name: steer
description: Generate Claude Code hooks from natural language rules to enforce coding standards, security policies, and best practices
user-invocable: true
prompt_version: 1
---

# Steer - Hook Generation from Natural Language Rules

When the user describes a coding rule in natural language, generate a Python hook script that enforces it in Claude Code, test it, and install it.

## Example Invocations

```
/steer never use console.log
/steer block SQL injection via f-strings in Python
/steer enforce PascalCase for React component names
/steer no TODO or FIXME comments in production code
/steer never use var in JavaScript | use const or let instead
```

## Operating Modes

### Interactive Mode (default)

Normal usage. You may ask clarifying questions, show progress, and iterate with the user.

### Headless Mode

Activated when the invocation contains `--headless`. Example:
```
/steer --headless never use console.log
```

In headless mode you MUST:
- **Never ask questions** — make autonomous best-effort decisions for any ambiguity
- **Never ask the user to do anything** — do all work yourself including directory creation, file writes, and registration
- Still generate, self-test, and iterate on the hook (the agentic loop is valuable)
- Strip `--headless` from the rule text before processing (it's a flag, not part of the rule)
- After completion, output ONLY a JSON result object as your final message (no surrounding text):

```json
{
  "hook_path": ".claude/hooks/<name>-hook.py",
  "hook_name": "<name>",
  "rule": "<the original rule text>",
  "self_test_passed": true,
  "self_test_summary": {
    "positive_pass": 2,
    "positive_fail": 0,
    "negative_pass": 2,
    "negative_fail": 0
  },
  "prompt_version": 1,
  "prompt_modules": ["simple-regex"],
  "model_used": "claude-sonnet-4-20250514"
}
```

**Metadata fields:**
- `prompt_version`: Read from the `prompt_version` field in this file's YAML frontmatter (top of SKILL.md). Do not guess.
- `prompt_modules`: List the specialist prompt filenames you read (e.g., `["simple-regex", "python-security"]`). Empty list `[]` if none.
- `model_used`: Your model name as shown in your system prompt. Report it exactly.

The test harness invokes: `claude -p "/steer --headless <rule>" --output-format json --no-session-persistence` and parses this result from the response.

## Specialist Prompts

Before generating a hook, read the appropriate specialist prompt for domain-specific patterns, examples, and critical edge cases. These contain hard-won knowledge about false positive avoidance and detection strategies.

| Rule targets... | Read | Type |
|---|---|---|
| Simple keyword/pattern blocking (console.log, TODO, var, debugger, JS/TS eval()) | [prompts/simple-regex.md](prompts/simple-regex.md) | detection |
| Python security (subprocess, os.system, SQL injection, Python eval/exec/compile, dangerous imports) | [prompts/python-security.md](prompts/python-security.md) | detection |
| Python style (naming conventions, docstrings, mutable defaults, imports) | [prompts/python-style.md](prompts/python-style.md) | detection |
| Terraform/HCL (resource config, tagging, security groups) | [prompts/terraform-hcl.md](prompts/terraform-hcl.md) | detection |
| HTML (accessibility, XSS, semantic markup) | [prompts/generic-html.md](prompts/generic-html.md) | detection |
| Shell safety & Bash commands (command blocking, chaining, exfiltration, env exposure, dangerous flags) | [prompts/shell-lint.md](prompts/shell-lint.md) | **template-override** |
| React/JSX (hooks, component patterns, data fetching) | [prompts/react-jsx.md](prompts/react-jsx.md) | detection |
| Security Layer 2 (hardcoded secrets, postinstall scripts, env enumeration) | [prompts/security-layer2.md](prompts/security-layer2.md) | detection |

### Specialist Types

- **detection** specialists provide detection logic that plugs into the standard template from Step 2. The template skeleton stays the same.
- **template-override** specialists provide their own complete hook template. When using a template-override specialist, **use their template instead of the one in Step 2**.

### Routing Precedence (Secrets and Env Rules)

Use these boundaries to avoid specialist selection ambiguity:

- **API key/token patterns in code** (`sk_live`, `AKIA`, hardcoded `api_key=...`) → `simple-regex.md`
- **Sensitive file/path references in code/config** (`id_rsa`, `.env`, `secrets.json`, `keystore`, `wallet.dat`) → `security-layer2.md`
- **Code-level env enumeration** (`Object.keys(process.env)`, `for ... in process.env`, `...process.env`) → `security-layer2.md`
- **Command-level env exposure and shell data exfiltration** (`env`, piping to network tools, dangerous shell command patterns) → `shell-lint.md`

Tie-break guidance:
- If a rule says "prevent secret exposure" or "prevent env exposure" without details, read **both** `simple-regex.md` and `security-layer2.md`.
- If the same rule also targets Bash command behavior, also read `shell-lint.md` and use its template as the base.

**If the rule doesn't clearly fit one category**, use the general protocol below without reading a specialist prompt.

**If the rule spans two categories**, read both specialist prompts. If one is a template-override, use that specialist's template as the base and incorporate detection logic from the other.

**If the rule targets both file content AND Bash commands** (e.g., "never use rm -rf" should block it in shell scripts AND in the Bash tool), generate a single combined hook that handles both tool types. Read the shell-lint specialist for the combined template and the relevant detection specialist for file-content-specific logic.

## How Claude Code Hooks Work

Hooks are Python scripts that run on tool operations. They receive JSON on stdin and can block operations by printing a JSON response to stdout.

### Hook Input (stdin)

```json
{
  "session_id": "uuid",
  "hook_event_name": "PreToolUse",
  "tool_name": "Edit",
  "tool_input": {
    "file_path": "/absolute/path/to/file.ext",
    "old_string": "original content",
    "new_string": "replacement content",
    "content": "full file content (Write tool only)",
    "command": "shell command (Bash tool only)"
  }
}
```

**Tool-specific fields:**
- **Edit**: `file_path`, `old_string`, `new_string`
- **MultiEdit**: `file_path`, plus array of edits
- **Write**: `file_path`, `content` (entire file)
- **Bash**: `command`

### Hook Output (stdout) - To Block

```json
{
  "decision": "block",
  "continue": true,
  "stopReason": "Clear explanation of what was blocked and how to fix it"
}
```

To **allow**: exit with code 0, print nothing to stdout.

## Generation Protocol

Follow these steps for every hook creation:

### Step 0: Parse the Rule

Before anything else, split the rule on `|` if present:

```
"Block f-string SQL queries | Use parameterized queries instead"
 ^--- detection_rule          ^--- suggestion (optional)
```

- **`detection_rule`** (before `|`): used for everything — classification, specialist prompt selection, detection logic, hook naming, self-tests
- **`suggestion`** (after `|`, if present): used **only** in the `stopReason` message to tell the developer what to do instead

Everything below operates on `detection_rule` only. The suggestion is carried forward solely for use in Step 2's stopReason.

### Step 1: Analyze the Rule and Read Specialist Prompt

Before writing code:
- Identify the rule category from the specialist table above (using `detection_rule`)
- **Read the appropriate specialist prompt** for domain-specific guidance
- Consider: what exact pattern should be blocked? Which tools are relevant? Which file types? What are the false positive risks?
- Look at the user's actual project files to understand what languages and patterns are in use

### Step 2: Generate the Hook Script

```python
#!/usr/bin/env python3
"""
Hook: [brief description]
Rule: [the user's rule]
Generated by steer
"""

import json
import sys
import re

def main():
    try:
        stdin_data = sys.stdin.read()
        if not stdin_data.strip():
            sys.exit(0)
        data = json.loads(stdin_data)
    except (json.JSONDecodeError, Exception):
        sys.exit(0)

    tool_name = data.get('tool_name', '')
    tool_input = data.get('tool_input', {})

    # Filter to relevant tools - exit early for others
    if tool_name not in ['Edit', 'MultiEdit', 'Write']:
        sys.exit(0)

    file_path = tool_input.get('file_path', '')

    # MultiEdit passes an array of edits, not a single new_string
    if tool_name == 'MultiEdit':
        edits = tool_input.get('edits', [])
        content_to_check = '\n'.join(e.get('new_string', '') for e in edits)
    else:
        new_string = tool_input.get('new_string', '')
        content = tool_input.get('content', '')
        content_to_check = new_string or content or ''

    if not content_to_check.strip():
        sys.exit(0)

    # [FILE TYPE FILTER] - Only check relevant files
    # [DETECTION LOGIC] - From specialist prompt guidance
    # [BLOCK IF VIOLATION]

    sys.exit(0)

if __name__ == "__main__":
    main()
```

### Step 3: Determine Scope and Save the Hook

**In interactive mode**, ask the user before saving:

> "Should this hook apply to **this project only** (saves to `.claude/hooks/`) or **all your projects** (saves to `~/.claude/hooks/`)? [default: this project]"

If the user doesn't respond or says "default", use **project-level** (`.claude/hooks/`).

**In headless mode**, default to **project-level** (`.claude/hooks/`) without asking.

After determining scope:
- Create the directory if it doesn't exist
- Write to `<scope>/hooks/<rule-name>-hook.py` using descriptive kebab-case naming
- Remember the scope for Step 4 (settings.json must match)

### Step 4: Register the Hook

Use the steer CLI to register the hook. Use `steer` from PATH if available, otherwise fall back to `${CLAUDE_PLUGIN_ROOT}/scripts/steer`. Run a **single command** — do not try one and then the other:

```bash
STEER_BIN="${CLAUDE_PLUGIN_ROOT:+${CLAUDE_PLUGIN_ROOT}/scripts/steer}"
STEER_BIN="${STEER_BIN:-steer}"
"${STEER_BIN}" register <scope>/hooks/<rule-name>-hook.py --rule "<original rule text>" --scope <project|global> \
  --source generated \
  --prompt-version "<prompt_version from SKILL.md frontmatter>" \
  --prompt-modules "<comma,separated,modules>" \
  --model "<your model name from system prompt>" \
  --self-test '{"passed":true,"positive_pass":2,"positive_fail":0,"negative_pass":2,"negative_fail":0}'
```

This atomically:
- Consolidates all individual hooks into a single `steer-hooks.py` master hook
- Registers only the master hook in settings.json (preserving non-steer hooks)
- Adds the rule to the matching CLAUDE.md under `## Steer Rules`

The `--prompt-version` value MUST come from this file's YAML frontmatter — do not guess or hallucinate a version number. For generated hooks, when `--hook-version` is omitted the CLI automatically derives it from `--prompt-version`. If other optional metadata is unavailable, omit those flags and let CLI defaults apply.

### Step 5: Test the Hook (MANDATORY)

**You must test every hook before reporting success.** This is the most important step.

Create test cases by piping simulated JSON input through the hook:

**Positive test (MUST block):**
```bash
echo '{"tool_name":"Write","tool_input":{"file_path":"test.js","content":"console.log(\"hello world\")"}}' | python3 .claude/hooks/console-log-hook.py
```
Expected: stdout contains `"decision": "block"`

**Negative test (MUST allow):**
```bash
echo '{"tool_name":"Write","tool_input":{"file_path":"test.js","content":"logger.info(\"hello world\")"}}' | python3 .claude/hooks/console-log-hook.py
```
Expected: no stdout, exit code 0

**Requirements:**
- Run at least 2 positive tests (variations of the violation)
- Run at least 2 negative tests (clean code that should pass)
- Run 1 irrelevant tool test
- If ANY test fails, fix the hook and retest ALL cases
- Continue iterating until all tests pass

**Critical: Negative tests must stress the boundary.** Simple "obviously clean" code is not enough. At least one negative test MUST use the same construct as the positive tests but in a safe way. Examples:
- If blocking `shell=True` with dynamic args, a negative test MUST include `shell=True` with `shlex.quote()` via an **intermediate variable** (not just inline):
  ```python
  quoted = shlex.quote(user_input)
  cmd = f"git clone {quoted}"
  subprocess.run(cmd, shell=True)
  ```
- If blocking `eval()`, a negative test MUST include `ast.literal_eval()` (the safe alternative)
- If blocking f-string SQL, a negative test MUST include parameterized queries that still use f-strings for table names (a common safe pattern)

The hardest bugs to find are false positives on safe code that *looks* like the dangerous pattern. Your negative tests must exercise this boundary.

**If the rule uses `|` suggestion syntax**, add one test that verifies the `stopReason` output contains the user's suggestion text. Pipe a positive test case and check that the JSON output includes the suggestion. The user chose those words deliberately — the stopReason must include them.

### Step 6: Report to User

After all tests pass, report:
- Hook name and file location
- What it blocks (with examples)
- Test results summary
- Any known limitations or edge cases

## Critical Rules

1. **Never crash** - Wrap stdin parsing in try/except. Exit 0 on any error.
2. **Exit early** - Filter irrelevant tools and file types before expensive analysis.
3. **Minimize false positives** - A false positive that blocks legitimate code is worse than missing a violation. Be precise.
4. **Comments and strings** - A pattern inside a comment or string literal is almost never a violation. For Python, prefer AST parsing (`ast` module) over regex — it inherently ignores comments and strings. For JS/TS and other languages without convenient AST access, strip comments and string literals before running regex detection. A rule blocking `console.log` must not block `// I removed console.log`. This is the #1 cause of developer frustration with custom hooks.
5. **Clear stopReason** - Explain WHAT was wrong and HOW to fix it.
6. **Safe regex** - Never use nested quantifiers like `(a*)*` or `(a|b)*` inside repetition. These cause catastrophic backtracking (ReDoS).
7. **Handle Edit, MultiEdit, and Write** - Edit provides `new_string`, Write provides `content`, MultiEdit provides `edits` (an array — concatenate all `new_string` values). See the template in Step 2.
8. **Multi-language rules** - When the rule mentions multiple languages, handle ALL of them with file extension routing. Don't focus on only one.

## User Suggestion Syntax

The `|` pipe splits a rule into a detection part and an optional suggestion (see Step 0). The suggestion appears **only** in the `stopReason`:

- Rule: `"never use npm | please use yarn instead"`
- stopReason: `"Use of npm detected. Suggestion: please use yarn instead"`

Never invent suggestions. Only include them when explicitly provided after `|`.
