---
name: steer
description: Turns a natural-language rule into the fix that can enforce it — a repository check, a PreToolUse Python guard, or an instruction rule — and installs it. Use when enforcing coding standards, blocking insecure patterns, making sure checks pass before a turn ends, managing hook lifecycle, or running steer CLI commands (register, remove, list, test, preset, verify, git-hooks).
user-invocable: true
prompt_version: 17
compatibility: Requires Claude Code, Codex, or Pi with tool-call hook support. Hooks are installed into .steer/hooks/ and registered in every installed harness (settings.json for Claude Code, hooks.json for Codex, extensions for Pi). steer CLI must be available on PATH or bundled under scripts/.
allowed-tools: Bash(steer *), Bash(python3 *), Bash(echo *), Read, Write, Edit
---

# Steer - Fixes from Natural Language Rules

When the user describes a coding rule in natural language, route it to the tier
that can enforce it, then install the fix. Most rules that name a concrete
pattern become a Python guard: generate it, test it, and install it.

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

Headless mode is the guard-generation benchmark's interface. It skips
[Pre-flight](#pre-flight-find-what-is-already-installed) and
[Route the rule](#route-the-rule), and always generates a guard, unless a
routing flag from [Headless routing](#headless-routing) is present.

In headless mode you MUST:
- **Never ask questions** — make autonomous best-effort decisions for any ambiguity
- **Never ask the user to do anything** — do all work yourself including directory creation, file writes, and registration
- Still generate, self-test, and iterate on the hook (the agentic loop is valuable)
- Strip `--headless` from the rule text before processing (it's a flag, not part of the rule)
- After completion, output ONLY a JSON result object as your final message (no surrounding text):

```json
{
  "hook_path": ".steer/hooks/<name>-hook.py",
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

### Headless routing

Two flags make headless mode route the rule. Each still skips Pre-flight. Strip
them, and any `--harness <id>`, from the rule text as you strip `--headless`.

- `--route-only`: route the rule with [prompts/routing.md](prompts/routing.md)
  and install nothing.
- `--route`: route the rule, then install on the routed tier as [Route the
  rule](#route-the-rule) says.

`--harness <id>` names the harness the rule must hold in (`claude-code`,
`codex` or `pi`). Without it, the rule must hold in every harness installed
here. `supported` is false when the routed tier cannot enforce the rule in
that harness, such as a repository check under Pi, which has no Stop runner.
Keep the routed tier; never swap in a weaker one to reach `supported: true`.
With `--route`, a rule that is not supported installs nothing.

`--route` never runs a command that replaces the operator's own hooks or
settings, such as `steer check migrate --apply`, and never passes `--force`.
List each such step in `pending`.

Output ONLY this JSON object as your final message. It replaces the guard
result above:

```json
{
  "mode": "route-only",
  "rule": "<the original rule text>",
  "tier": "check | guard | instruction | review",
  "enforcement_point": "before-action | turn-end | review | judgment",
  "action": "enable-existing-oracle | add-detector | generate-guard | add-instruction-rule | add-review-entry",
  "supported": true,
  "reason": "<one sentence: why this tier and action>",
  "installed": [],
  "pending": []
}
```

- `mode` is `route-only` or `route`.
- `tier` is one of the four words shown, never the rung's display name from the
  routing guide's table.
- `action` is the install path: for a repository check, `enable-existing-oracle`
  when this repository's `check.sh` already runs the suite the rule names, and
  `add-detector` when the rule needs a detector that `check.sh` does not run
  yet.
- `installed` lists what `--route` installed, each item with its path or id.
  `route-only` leaves it empty.
- `pending` lists each step left for the user, such as a migration or a `/hooks`
  approval. A repository check that is not armed yet always leaves a step here:
  name the command that arms it, or `steer check migrate --apply` when a
  personal check gate must move first. `route-only` leaves it empty.

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
- **Edit**: `file_path`, `old_string` (removed text), `new_string` (added text). Every edit shape arrives as Edit, and each carries `old_string` except a file deletion.
- **Write**: `file_path`, `content` (entire file)
- **Bash**: `command`

The master hook rewrites every other edit shape into these, so a guard never sees `MultiEdit` or `apply_patch`:
- A **MultiEdit** arrives as one Edit per hunk, with that hunk's `old_string` and any `replace_all` as sent.
- A **Codex patch** arrives as one Edit per updated file (a Write per added file). Its `old_string` joins the removed lines and its `new_string` the added lines, each in order across the file's hunks. They are ordered fragments that may not be contiguous: match text in them, never apply them as search-and-replace. A section that removes nothing has `old_string: ""`.
- A **Codex file deletion** arrives as Edit with `steer_operation: "delete"`, `new_string: ""` and no `old_string`. A guard that needs the deleted content opens the file.
- A **shell deletion** (`rm`, `git rm`) stays a Bash call; the guard parses the target from `command`.

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

Follow these steps for every hook creation.

### Pre-flight: find what is already installed

Before you install anything, read the fixes Steer already holds and the classes
it already decided. This step stops a duplicate fix and a re-proposed class.

```bash
STEER_ROOT="${PLUGIN_ROOT:-${CLAUDE_PLUGIN_ROOT:-}}"
STEER_BIN="${STEER_ROOT:+${STEER_ROOT}/scripts/steer}"
STEER_BIN="${STEER_BIN:-steer}"
"${STEER_BIN}" status --json
"${STEER_BIN}" list --scope global
"${STEER_BIN}" preset list --scope global
```

`status --json` reads guards and presets in the project scope only; the two list
commands add the global scope.

**Installed fixes.** Compare the rule with each fix, whatever its tier:

- Guards: `hooks[]`, the global `list`, and each enabled preset in `presets[]`
  and the global `preset list`.
- Instruction rules: `rules[]`, both scopes.
- Review prompt entries: `addenda[]`, both scopes.
- Repository checks: `checks.checks[]`, for this repository only. For a request
  to run the tests before a turn ends, `checks.adopted` and `checks.runner[]`
  say whether this repository's `check.sh` already runs there.

Keep each fix that covers the rule, with its tier and scope. [Route the
rule](#route-the-rule) decides what the match means.

**Decided classes.** Read the `interventions[]` array. Each row is one live
intervention with its `class`, `tier`, `target`, `status`, and its `observation`
— what the corpus recorded, never a claim that the fix worked. Match the user's
rule against the classes there:

- The rule matches a class: the class is already decided. A record does not show
  that its fix stands here: read its `artifact` and `scope`. While the fix stands
  and the scope takes in this repository and harness, do not install a duplicate;
  say which record covers it and what its observation shows. Otherwise the
  install may go ahead, and the record does not measure it.
- The record's `observation.recurrence.occurred` is true: the class came back
  after the record went in. Treat the user's rule as a rework of that record
  rather than as a new fix, and open `observation.recurrence.example` first.
- The record has no `scope`, or its `observation.coverage` counts unknown units:
  the class is installed and its window is not accounted for. Name that from the
  row, and do not install a duplicate.
- No match: continue to Step 0.

An observation states no direction. Never read a quiet after-window as proof the
fix worked: prevention and absent opportunity both produce silence.

Fail soft. If a command errors, or the CLI is absent, say so in one line and
continue. This check is an optimization, not a gate. The `status --json` schema
is documented once, in the contract section of `skills/steer-report/SKILL.md`.

### Step 0: Parse the Rule

Before anything else, split the rule on `|` if present:

```
"Block f-string SQL queries | Use parameterized queries instead"
 ^--- detection_rule          ^--- suggestion (optional)
```

- **`detection_rule`** (before `|`): used for everything — classification, specialist prompt selection, detection logic, hook naming, self-tests
- **`suggestion`** (after `|`, if present): used **only** in the `stopReason` message to tell the developer what to do instead

Everything below operates on `detection_rule` only. The suggestion is carried forward solely for use in Step 2's stopReason.

### Route the rule

Read [prompts/routing.md](prompts/routing.md) and route `detection_rule` to a tier.
Then compare it with the fixes Pre-flight kept:

- A fix on that tier already covers the rule: install nothing. Name the fix, its
  tier and its scope.
- Only a weaker tier covers it, such as an instruction rule for a rule a guard
  can decide: install the stronger fix, then offer to remove the weaker one with
  its tier's remove command.
- A fix covers it in the other scope only: say so, and ask whether the rule is
  wanted in this scope too.

Then install on the routed tier:

- **Pre-tool guard:** continue with Step 1. Steps 1 to 6 are the guard branch.
- **Any other tier:** install it as that guide's section 3 says, then report the
  tier, what was installed and where, and what still needs the user. For an
  instruction rule, the rule text is the whole rule, suggestion included.

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

    # Filter to relevant tools - every edit arrives as Edit or Write
    if tool_name not in ['Edit', 'Write']:
        sys.exit(0)

    file_path = tool_input.get('file_path', '')

    # Added text. A rule about removed text reads tool_input.get('old_string', '')
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

> "Should this hook apply to **this project only** (saves to `.steer/hooks/`) or **all your projects** (saves to `~/.steer/hooks/`)? [default: this project]"

If the user doesn't respond or says "default", use **project-level** (`.steer/hooks/`).

**In headless mode**, default to **project-level** (`.steer/hooks/`) without asking.

After determining scope:
- Create the directory if it doesn't exist
- The store is `.steer` for project scope and `~/.steer` for global scope. `<store>` below names it.
- Write to `<store>/hooks/<rule-name>-hook.py` using descriptive kebab-case naming
- Remember the scope for Step 4 (settings.json must match)
- Write the test cases beside it, as `<rule-name>-cases.json`. Step 5 designs the
  cases; this file is where they live, so they can be rerun.

**The fixture file.** A case is a hook payload plus the outcome it asserts, so a
stored case is rerunnable evidence. A count is not.

```json
{
  "version": 1,
  "cases": [
    {
      "name": "console.log is blocked",
      "expect": "block",
      "origin": "synthetic",
      "payload": {
        "tool_name": "Write",
        "tool_input": {"file_path": "test.js", "content": "console.log(1)"}
      }
    },
    {
      "name": "logger.info is allowed",
      "expect": "allow",
      "origin": "synthetic",
      "payload": {
        "tool_name": "Write",
        "tool_input": {"file_path": "test.js", "content": "logger.info(1)"}
      }
    }
  ]
}
```

- `expect` is `block` or `allow`. Store the legitimate cases beside the violations.
- `origin` is `synthetic` for a case you wrote. A case reconstructed from a corpus
  is `reconstructed` — see `steer fixture mine`.
- `files` is optional: a map of relative path to content, the filesystem context a
  guard reads. The runner writes it into a sandbox and rewrites the payload's
  relative paths against it.

### Step 4: Register the Hook

Use the steer CLI to register the hook. Use `steer` from PATH if available, otherwise fall back to the plugin root (`PLUGIN_ROOT` on Codex, `CLAUDE_PLUGIN_ROOT` on Claude Code). Run a **single command** — do not try one and then the other:

```bash
STEER_ROOT="${PLUGIN_ROOT:-${CLAUDE_PLUGIN_ROOT:-}}"
STEER_BIN="${STEER_ROOT:+${STEER_ROOT}/scripts/steer}"
STEER_BIN="${STEER_BIN:-steer}"
"${STEER_BIN}" register <store>/hooks/<rule-name>-hook.py --rule "<original rule text>" --scope <project|global> \
  --source generated \
  --prompt-version "<prompt_version from SKILL.md frontmatter>" \
  --prompt-modules "<comma,separated,modules>" \
  --model "<your model name from system prompt>" \
  --fixtures <store>/hooks/<rule-name>-cases.json \
  --self-test '{"passed":true,"positive_pass":2,"positive_fail":0,"negative_pass":2,"negative_fail":0}'
```

This atomically:
- Consolidates all individual hooks into a single `steer-hooks.py` master hook
- Registers only the master hook with the harness (preserving non-steer hooks)
- Adds the rule to that harness's instruction file under `## Steer Rules`

The store is shared between harnesses; the registration is not. `register` writes
to every hook-registering harness installed on the machine — the one whose config
directory exists — and reports which ones on the last line of its output. A guard
is a rule about the operator's work, not about one agent, so all get it. Add
`--harness claude-code`, `--harness codex`, or `--harness pi` to write to one only, or
`--all-harnesses` to write to every hook-supporting harness whether or not it is
installed here. On a machine with no harness directory, `register` writes to
Claude Code.

Installing is not measuring. Where the guard runs and which units get counted are
separate decisions: install everywhere, then score each harness on its own record
with `--scope-harness`. Codex and Claude Code carry different models, so pooling
their chains into one rate moves the number for a reason the guard did not cause.

The `--prompt-version` value MUST come from this file's YAML frontmatter — do not guess or hallucinate a version number. For generated hooks, when `--hook-version` is omitted the CLI automatically derives it from `--prompt-version`. If other optional metadata is unavailable, omit those flags and let CLI defaults apply.

### Step 5: Test the Hook (MANDATORY)

**You must test every hook before reporting success.** This is the most important step.

Create test cases by piping simulated JSON input through the hook:

**Positive test (MUST block):**
```bash
echo '{"tool_name":"Write","tool_input":{"file_path":"test.js","content":"console.log(\"hello world\")"}}' | python3 .steer/hooks/console-log-hook.py
```
Expected: stdout contains `"decision": "block"`

**Negative test (MUST allow):**
```bash
echo '{"tool_name":"Write","tool_input":{"file_path":"test.js","content":"logger.info(\"hello world\")"}}' | python3 .steer/hooks/console-log-hook.py
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

**Verify both passes (MANDATORY).** Registration (Step 4) consolidates your hook
into `<store>/hooks/steer-hooks.py` and reruns the stored cases. Run them again on
demand with:

```bash
steer test <rule-name>-hook
```

This replays every case twice, against byte-identical copies in a temporary store,
so your audit log records no synthetic firing:

- **Against the guard alone.** This is the guard's whole contract: every case,
  block and allow. Any mismatch is a failure — fix the hook and rerun.
- **Through the generated master.** Guards run in a fixed order and the first to
  object wins, so an expect-block case has three outcomes. *Blocked by this guard*
  is verified. *Blocked by another guard* is **shadowed**: reported, naming that
  guard, and never a failure — it is simply not proof that your guard ran.
  *Allowed* is a failure: a case that blocks alone did not block through the
  master. Inspect the integration before blaming consolidation. An expect-allow
  case has three outcomes of its own. *Allowed* asserts nothing. *Blocked by
  another guard* names that guard and is not this guard's failure, and that
  guard's block is not established as legitimate either. *Blocked by this guard*
  is a failure: the guard alone allowed a case it blocked through the master.
  The run does not diagnose it, because equal source can still answer differently
  inside the master — it normalizes one tool call into several and calls `main()`
  once per virtual edit. Read whether the master embeds this guard's current
  source; when it does, inspect normalization, the guard's own state across calls,
  and the case's label.

The state this earns says the guard intercepts its stored cases. It never says a
field block was correct, or that what the agent did next was useful.

The state is bound to four values — the guard's digest, the fixtures' digest, the
rule's hash and the master's digest — so an edit to any one of them drops it.
Consolidation clears it and the rerun re-establishes it; a failing case never
blocks a removal.

### Step 6: Report to User

After all tests pass, report:
- Hook name and file location
- What it blocks (with examples)
- Test results summary
- Any known limitations or edge cases

## Removing or Restoring a Guard

Remove a guard through the CLI, never by deleting its file. `steer remove`
archives the guard file, its fixtures and its manifest entry before it deletes
them, then drops the rule line and rebuilds the master. A hand deletion keeps
no copy and leaves the manifest entry behind.

```bash
"${STEER_BIN}" remove <hook-id> --scope <project|global> --reason "<why it goes>" --force
```

The command asks for confirmation on a terminal, and an agent's shell has none.
Pass `--force` only after the user confirmed this removal. Always pass
`--reason`: the archive keeps it for the next reader.

To put a guard back, run `steer archive list` to find its entry, then:

```bash
"${STEER_BIN}" archive restore <hook-id> --scope <project|global>
```

Restore reinstalls the guard through `steer register`'s installer, so its
fixtures run again. Report that result as in Step 5. Add `--tier guard` when
the id also holds another tier's entry, and `--entry <name>` when the list shows
more than one guard entry. Restore refuses an id that is installed now.

If `steer status --json` lists a record for the guard, a removal reads as an
`absent` fix, and the record measures nothing until the guard returns or the
record is retired. After a restore, the record carries `artifact.removed`:
`steer fix register --supersedes <record-id>` starts a clean window. Leave the
choice between the two to `/steer-report`.

The other tiers have their own remove commands, and each archives the same way
and takes `--reason`: `steer rules remove <id>` for an instruction rule,
`steer commit-gate addendum remove <id>` for a review addendum entry, and
`steer check remove <id>` for a repository check. `steer archive restore`
restores all of them; pass `--tier` when one id holds entries of more than one tier.

## Critical Rules

1. **Never crash** - Wrap stdin parsing in try/except. Exit 0 on any error.
2. **Exit early** - Filter irrelevant tools and file types before expensive analysis.
3. **Minimize false positives** - A false positive that blocks legitimate code is worse than missing a violation. Be precise.
4. **Comments and strings** - A pattern inside a comment or string literal is almost never a violation. For Python, prefer AST parsing (`ast` module) over regex — it inherently ignores comments and strings. For JS/TS and other languages without convenient AST access, strip comments and string literals before running regex detection. A rule blocking `console.log` must not block `// I removed console.log`. This is the #1 cause of developer frustration with custom hooks.
5. **Clear stopReason** - Explain WHAT was wrong and HOW to fix it.
6. **Safe regex** - Never use nested quantifiers like `(a*)*` or `(a|b)*` inside repetition. These cause catastrophic backtracking (ReDoS).
7. **Handle Edit and Write** - Every edit reaches a guard as one of these (see Hook Input). Edit provides `new_string` (added) and `old_string` (removed), Write provides `content`. A rule about code being added reads `new_string` or `content`; a rule about code being removed reads `old_string`; a rule about deleting files checks `steer_operation == "delete"` for Edit and parses `command` for Bash. Write test cases and stored fixture cases in these shapes, never as a raw MultiEdit or apply_patch payload. See the template in Step 2.
8. **Multi-language rules** - When the rule mentions multiple languages, handle ALL of them with file extension routing. Don't focus on only one.

## User Suggestion Syntax

The `|` pipe splits a rule into a detection part and an optional suggestion (see Step 0). In a guard, the suggestion appears **only** in the `stopReason`:

- Rule: `"never use npm | please use yarn instead"`
- stopReason: `"Use of npm detected. Suggestion: please use yarn instead"`

Never invent suggestions. Only include them when explicitly provided after `|`.
