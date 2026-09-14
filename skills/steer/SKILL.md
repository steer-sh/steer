---
name: steer
description: Generates and installs PreToolUse Python hooks from natural language rules. Use when enforcing coding standards, blocking insecure patterns, managing hook lifecycle, or running steer CLI commands (register, remove, list, test, preset, verify, git-hooks).
user-invocable: true
prompt_version: 10
compatibility: Requires Claude Code, Codex, or Pi with tool-call hook support. Hooks are installed into .steer/hooks/ and registered in every installed harness (settings.json for Claude Code, hooks.json for Codex, extensions for Pi). steer CLI must be available on PATH or bundled under scripts/.
allowed-tools: Bash(steer *), Bash(python3 *), Bash(echo *), Read, Write
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

Follow these steps for every hook creation.

### Pre-flight: Check guardrail health

Before you generate anything, read the full failure-class inventory. This step
stops you from re-proposing a class the loop already decided.

```bash
STEER_ROOT="${PLUGIN_ROOT:-${CLAUDE_PLUGIN_ROOT:-}}"
STEER_BIN="${STEER_ROOT:+${STEER_ROOT}/scripts/steer}"
STEER_BIN="${STEER_BIN:-steer}"
"${STEER_BIN}" status --json
```

Read the `interventions[]` array. Each row is one live intervention with its
`class`, `tier`, `target`, `status`, and its `observation` — what the corpus
recorded, never a claim that the fix worked. Match the user's rule against the
classes there:

- The rule matches any class here: the class is already decided. Do not install a
  duplicate. Say which record covers it and what its observation shows.
- The record's `observation.recurrence.occurred` is true: the class came back
  after the record went in. Treat the user's rule as a rework of that record
  rather than as a new hook, and open `observation.recurrence.example` first.
- The record has no `scope`, or its `observation.coverage` counts unknown units:
  the class is installed and its window is not accounted for. Name that from the
  row, and do not install a duplicate.
- No match: continue to Step 0.

An observation states no direction. Never read a quiet after-window as proof the
fix worked: prevention and absent opportunity both produce silence.

Fail soft. If the command errors, or the CLI is absent, say so in one line and
continue. This check is an optimization, not a gate. The `status --json` schema
is documented in "The `steer status --json` contract" below.

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
  case is asserted against the guard only, because another guard may legitimately
  block it.

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

## The `steer status --json` contract (`status_version: 17`)

`steer status` composes one read-only snapshot of the whole loop. The unit is the
failure class and what the corpus recorded about it, not the hook count. The
Pre-flight step reads it to dedup a rule against classes the loop already decided.
This schema is byte-shared with `skills/steer-report/SKILL.md`; change both
together (see CLAUDE.md Key Contracts). Fields:

**Read every count here as an observation.** The windows are uncontrolled: the
dominant input to an agent's behaviour is its standing instruction set, which no
chain and no session records, which a person edits by hand between sessions, and
which an instruction-tier fix edits directly. So the snapshot reports hits,
eligible units, window bounds, coverage, guard firings and the identities that
counted each window, and it reports no efficacy verdict. The intervention's
contribution to any change in these counts is unresolved. v11 removes `verdict`,
`p_value`, `effect`, `required_after`, `confounds`, `composition`,
`instrument_drift`, `firings_applicable`, `attribution` and `chain_rate`, and adds
`observation`. v12 adds `checks`: the repository check tier, whose registration,
authorization and observed execution are three readings and not one. v13 adds
`observation.checks`: a check-tier fix's executions in its window, counted on
this machine only. v16 adds `observation.checks.registered_host` and
`other_host`: a fix registered on another host is not missing coverage here.
v17 adds `artifact`: the fix a record binds and where it stands now.

- `status_version` — the schema version. `--json` is a stable agent contract.
- `posture` — the arming state: `instrument` (reviewer model / runs / prompt
  digest), `reviewer`, `ledger` path, and `commit_gate[]` / `denials[]` /
  `resolver[]`. Each row carries `harness`, `scope`, `registered`, `effective`,
  `trust`, `command`, `resolvable`, `observed` and `observed_corpus`. A
  registration is three separate readings, and only the first is what the
  configuration says. `command` is the command the registration names, verbatim.
  `resolvable` is whether that command resolves — `resolvable`, `missing`,
  `unknown`, or `not-registered` when there is no command to test. Treat it as a
  PROXY, never a proof: the hook runs under the harness's environment, whose
  PATH may differ, so it can prove a command absent and never prove one runs.
  **Act on `missing`:** neither harness reports a command it cannot resolve — the
  tool call runs, the hook is skipped, and nothing says so — and
  `steer bin install` repoints every registration at `~/.steer/bin/steer`.
  `observed` is the epoch second of the newest row the channel wrote to
  `observed_corpus`, 0 when it never wrote one, and both fields are absent when
  the channel writes no corpus of its own (the resolver). Never observed is not
  proof of breakage: nobody may have attempted the action the channel fires on.
  A corpus records no scope, so both scopes of one harness report the same
  evidence. One guard store serves both harnesses but each harness holds its
  own registration, so a scope alone does not identify a row: `commit_gate` and
  `resolver` report one row per harness per scope, and `denials` is Claude
  Code's alone. `trust` is `not-applicable` (the harness reviews no hook trust),
  `record-present-unverified`, `missing` or `unknown` — Steer never claims to
  have verified a harness's internal hash. The resolver is the Stop hook that
  joins ledger rows to transcripts; it arms with the commit gate on the same
  scope. `unresolved_rows` counts the rows still owing that join — a window
  cannot account for them, and `steer commit-gate backfill --apply` clears them.
  `masters[]` is the guard master's runtime provenance, one row per harness per
  scope: `path`, `runtime` (the digest of the Python runtime spliced into it),
  `version` (the steer that wrote it), `expected` (this binary's runtime digest)
  `state` and `observed` (the epoch second of the last block in the audit log
  beside this registered master; the log holds blocks only, so a master that
  allowed every call reads as 0). `state` is `current`, `stale` (an older runtime, so a firing
  writes fewer audit fields than the join reads), `unknown` (an unstamped
  master — every store built before the stamp existed, which never reads as
  current) or `not-registered`. `trust` is the harness's approval of the
  registered runner, in the same values as a `posture` row's `trust`, and is
  absent when no runner is registered. A Codex runner reading `missing` runs no
  guard, while the guard's rule still reaches the agent as text; approve the
  definitions in `/hooks`. The row reads the REGISTERED master, not the one
  the store resolves to: a migrated machine holds a master at both paths. **Act
  on `stale` or `unknown`:** run `steer hooks rebuild --scope <scope>`, which
  regenerates the master AND re-registers it in one operation.
  `rules[]` reads each installed harness's `## Steer Rules` section against the
  manifest, one row per installed harness per scope; a harness that is not
  installed gets no row. `path` is the instruction file (CLAUDE.md or AGENTS.md),
  `expected` counts the rules the manifest names in that scope, `missing` lists
  the rule ids whose text the section does not carry, and `extra` lists section
  lines the manifest does not name, which the next sync removes. `state` is
  `current`, `drifted` or `unknown` (the file could not be read). A record whose
  rule is missing from a harness's file stays live while that agent never reads
  the rule: a harness installed after the rule was written, or a hand edit to the
  file, causes it. **Act on `drifted`:** run `steer hooks rebuild --scope <scope>`,
  which rewrites every installed harness's section from the manifest.
- `checks` — this repository's check tier on this machine, with an empty reading
  when `steer status` ran outside a repository. `repo` is the resolved Git root,
  `manifest` says whether it declares checks, and `adopted` / `adopted_at` say
  whether THIS machine authorized its `check.sh` to run at the end of a turn.
  Registration, authorization and observed execution are three separate readings,
  and a clone carries none of the second. `runner[]` is the Stop runner's
  registration in the same shape as a `posture` row, one per harness per scope;
  its `observed` is the newest run that harness's runner recorded, and its
  `trust` is `unknown` wherever a harness reviews hook trust, because Steer reads
  no Stop trust record for that event. `checks[]` is one row per declaration:
  `id`, `kind` (`script` or `go-test`), `binding` (the script, or the package and
  test), `validation` (`current`, `stale` or `none`) with `changed` naming what
  moved since the receipt in `receipt` / `validated_at`, and `execution` /
  `executed_at` / `execution_run` from the newest execution record here. **A
  stale check keeps running:** staleness is a label on the proof, never a repair
  the loop demands, and `steer check test <id>` replays the cases when a fix
  registration needs fresh proof. An absent `execution` is unobserved and never a
  pass — an aggregate `check.sh` outcome attributes no result to one assertion.
  `latest_run` is the newest run that did any work: `run_id`, `source`,
  `harness`, `outcome`, `disposition`, `finished`, `missing[]`,
  `integration_failure` and `refused_after`. A run the repository lock turned
  away ran nothing, so it never stands in for the result of the run that held the
  repository; `refused_after` is when such a run was turned away. **Act on
  `integration_failure`:** a passing `check.sh` that ran no declared check is the
  script's wiring and never a failed assertion — add the managed line naming
  `steer check exec --all` or `steer check go-test`.
- `interventions[]` — the **full** inventory. It carries every live class. Each
  row: `id`, `class`, `problem`, `intervention`, `legacy_note`, `match` (the regex
  that defines the class), `tier`, `target`, `status` (`active` or `rejected`),
  `before` / `after` incidence (`hits`, `units`, `rate`), and for
  a refused class `reason`, `reopen` and `rejected_ts`. `problem` and `intervention` are the record's own explanation, authored when the
  evidence was in hand: what goes wrong, and what this record does about it. Lead
  with them — a `class` is a label and a `match` is a regex, and neither says what
  the class is. On a `rejected` row the `intervention` is the one that was
  considered, or the sentence `no suitable intervention was identified`.
  `legacy_note` is present only on a record written before those fields existed;
  it holds implementation detail, so never read it as either. This is the dedup source
  — a class here is decided. `corpus` and `unit` name the evidence the class is
  counted over and what one denominator entry is: `review-findings` counts by
  `chain`, `transcript-friction` by `session`. Never compare a chain rate to a
  session rate. A session-unit row also carries `signal_kind` — the classifier
  kind its regex was authored against (`any`, `user_corrections`,
  `user_rejections`, `tool_failures` or `hook_blocks`). Each row also carries
  its measurement conditions:
  - `scope` — the frozen eligibility rule: `{version, all}`, or `{version, repo,
    path}` at version 1, plus a `harness` id list at version 2. The predicates
    combine with AND. `null` is a legacy record: its two windows describe
    different opportunity sets, and no scope is ever inferred for it. Run
    `steer fix scope <id>`.
  - `before_coverage` / `after_coverage` — each window's unit accounting:
    `total`, `eligible` (the denominator), `ineligible`, `unknown`,
    `unknown_scope`, `unknown_evidence`, `pending`, `partial_exposure`. An
    unknown unit is not counted; it never shrinks the denominator.
    `partial_exposure` is the session corpus's own category, for a session that
    straddled the install and so ran under both regimes: `unknown` means the
    scope could not decide, `partial_exposure` means the window could not.
  - `observation` — what the corpus recorded. Five blocks:
    - `counts` — `split_ts` (the boundary: the install, the refusal, or the
      scope migration), `observed_ts` (the read's clock), `unit`, and `before` /
      `after`, each `{hits, eligible, first_activity_ts, last_activity_ts}`. The
      two timestamp pairs are different things: the period bounds express an
      empty window and say how long one has been quiet, and the activity bounds
      name the eligible work actually seen, which an empty window cannot express.
    - `recurrence` — `{occurred, matches, example_kind, example, example_ts}`.
      `example_kind` is `adjudicated_finding` (a reviewer's confirmed concern) or
      `cluster_key` (the text a classifier grouped a session's friction by, which
      is NOT a confirmed escaped failure). Read the example before acting.
    - `firing` — the guard audit stream: `{applicable, log_present, hook_id,
      scope, class_scoped, rows, units, newest_ts}`. `applicable` is false for a
      tier that writes no audit rows and for a record naming no hook.
      `log_present: false` is unknown activity, never a known zero. `scope` is
      `eligible_units` (rows joined to this class's own eligible units, and
      `class_scoped` is then true) or `window_period` (every row this hook wrote
      inside the window's bounds, whatever work it landed on). A `window_period`
      count cannot establish that the guard fired on this class's work.
    - `checks` — a check-tier fix's execution stream: `{applicable, attribution,
      stream_present, registered_host, other_host, check_id, repo, scope, class_scoped, executions, outcomes,
      blocked, changed_detector, units, newest_ts}`. It counts executions recorded
      on THIS machine only; review history can hold another machine's commits, so
      a zero there can be work this machine never ran. `attribution` is
      `unavailable` for a record that binds no check. `stream_present: false` is
      unknown activity, never a known zero. `registered_host` is the host that
      registered the fix; with `other_host: true` and no stream here, its runs are
      on that host, and this machine has no coverage gap to repair. `scope` follows `firing`: a review
      record counts every execution in the window's bounds (`window_period`), a
      session record counts Stop runs in its eligible sessions (`eligible_units`).
      `outcomes` counts by execution outcome, `blocked` counts failures whose run
      blocked a turn, and `changed_detector` counts runs of a detector other than
      the one the record registered.
    - `coverage` — `before` / `after`, the same maps as above, plus
      `zero_eligible_after`. That flag is a statement, not a fault: every freshly
      installed record has zero eligible after-units, and absent opportunity is
      not an observed quiet window.
    - `provenance` — `{kind, before, after}`. `kind` is `reviewer` (the reviewer
      identities that counted a chain window) or `parser` (the parser-and-adapter
      identities that produced a session window). Labels only: no rule here
      compares them. A window whose two lists differ was counted by two
      instruments, which is a reason to read the examples rather than the counts.
    - `declined` — true on a refused record. Its after-window is the period since
      the refusal, so a recurrence there implies no fix that failed.
  - `artifact` — the fix the record binds and where it stands now:
    `{id, scope, repo, state, detail}`, `null` on a declined record. It is read
    beside the observation and never enters it. `state` is `present` (the entry
    holds the text the record froze), `changed` (the same id with other text, or
    a check whose detector moved), `absent` (a readable store no longer holds
    it), `unreadable` (`detail` names why), `unbound` (a legacy record that names
    no entry), `elsewhere` (a project entry or a check in a repository other than
    the one read) or `unplaced` (a project entry whose record names no
    repository). A review entry is read where the commit gate reads it: the
    global store on disk, the project store at HEAD. **An `absent` fix is not a
    quiet window:** the record measures a fix that is no longer there, so
    reinstall it or retire the record.
- `summary` — `total` plus `counts` by action section (`inspect`, `repair`,
  `observe`).
- `chains[]` — recent review chains, findings split `accepted` / `rejected`.
- `attention` — the subset asking for a reader: `interventions[]` (a recurrence,
  a missing coverage cause, or no frozen scope), `candidates[]` (no `match` yet),
  `unscored[]` (ids of records no engine reads), and `checks[]` — the open steps
  in this repository's check tier, the same rows the queue carries.
- `telemetry` — corpus sizes: `review_chains`, `denials_rows`, `audit_firings`,
  `session_snapshots`, plus `harnesses[]` — one row per harness with its
  `sessions`, `firings` and `chains`, so a harness that contributed nothing reads
  differently from one that was never mined.
- `corpora[]` — one row per corpus with `present`, `size` and `path`.
- `hooks[]` — the registered managed hooks in the active scope: `name`, `scope`,
  `source`, `rule`.
- `presets[]` — the catalog roster: `name`, `bundle`, `description`, `enabled`,
  `drifted`.
- `team` — the repo-committed team lockfile summary: `present`, `path`,
  `hooks_locked`, `catalog_version`, and `error`. v10 adds `error`: a project with
  no lockfile is `present: false` and is not a fault, while a lockfile that exists
  and cannot be read is `present: true` with `error` naming why. Before v10 both
  read as absent.

### What an observation supports, and what it does not

An observation supports three readings and no fourth:

- **The class came back.** `recurrence.occurred` is a fact about the corpus.
  Open the example and decide whether the record still describes the problem.
- **The accounting is incomplete.** Unknown units, or an absent guard audit log,
  mean the window cannot be read at all. Name the cause and repair it.
- **The window is quiet.** Eligible work arrived and the class did not. That is
  what was recorded, and it is not evidence that the fix caused the silence.

Behaviour is established at install, not in the field: a guard's stored fixtures
(`steer fix mine`, `steer test <hook>`) replay real cases deterministically.
Field counts are maintenance evidence — they say whether a class is still
happening, never whether a fix is what stopped it.
