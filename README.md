# steer-skill

`steer` turns natural-language rules into enforceable Claude Code guardrails. Instead of relying on memory, code review luck, or lint configs that do not match your actual policy intent, steer generates real hook scripts, installs them into Claude Code, and keeps rule state synchronized across settings and documentation.

If you care about preventing repeated mistakes, policy drift, or security regressions while coding with AI assistance, this gives you a practical enforcement layer with low operational overhead.

## What This Repo Contains

- `skills/steer/SKILL.md`: the steer skill prompt and workflow used by `/steer`.
- `cli/`: Go CLI source (`register`, `list`, `test`, `remove`).
- `bin/`: prebuilt binaries for:
  - `darwin-amd64`
  - `darwin-arm64`
  - `linux-amd64`
  - `linux-arm64`
  - `windows-amd64`
- `scripts/steer` and `scripts/steer.cmd`: platform launchers that dispatch to the correct binary.
- `tests/`: headless generation + canonical fixture validation harness.

## How Steer Works

1. You invoke `/steer <rule>` (interactive) or `/steer --headless <rule>` (automation).
2. The skill generates a Python hook and self-tests it.
3. The skill registers the hook through the steer CLI.
4. The CLI updates managed artifacts atomically:
   - copies/normalizes hook into the selected scope hook directory
   - updates `steer-manifest.json`
   - rebuilds consolidated `steer-hooks.py`
   - upserts the master hook command in `settings.json` while preserving non-steer hooks
   - appends/removes rule entries in `CLAUDE.md` under `## Steer Rules`
5. Claude Code executes the consolidated master hook, which delegates to individual steer hooks.

## Rule Syntax (`detect | suggest`)

Steer supports split rule syntax:

- Left side (`detect`): what must be blocked by the generated hook.
- Right side (`suggest`, optional): what developers should do instead.

Example:

```text
never use var declarations | use const or let instead
```

The hook enforces the left side and uses the right side in developer-facing guidance (`stopReason`) when blocking.

## Hard + Soft Steering

Steer gives you two enforcement layers:

- Hard steer: hook-level block/allow decisions at tool time.
- Soft steer: rule text persisted into `CLAUDE.md` (`## Steer Rules`) so the assistant is continually nudged by project policy even before a hard block fires.

## CLI Workflow

```bash
# Register a generated (or hand-written) hook
steer register .claude/hooks/no-var-hook.py --rule "never use var | use const or let"

# See active hooks and their rules
steer list

# Test one hook locally against sample code
steer test no-var-hook --code "var x = 1"

# Remove a hook and clean up managed state
steer remove no-var-hook
```

All commands support `--scope project|global` (default: `project`).

## Build / Install From Source

Build all supported binaries:

```bash
cd cli
bash build.sh
```

Optional local install of the current platform binary to `/usr/local/bin`:

```bash
cd cli
bash build.sh --install
```

If using this as a Claude plugin, use the included launcher scripts (`scripts/steer` on macOS/Linux, `scripts/steer.cmd` on Windows).

## Test Harness

Install dependencies:

```bash
pip install -r tests/requirements.txt
```

List test cases:

```bash
python3 tests/skill_test.py --list
```

Run a single case:

```bash
python3 tests/skill_test.py --test console-log-block
```

Run the full suite:

```bash
python3 tests/run_tests.py
```

## Testing Notes

- Tests invoke Claude in headless mode via `/steer --headless ...` and require a working `claude` CLI.
- Canonical case definitions live in `tests/test_data/prompts.yaml`.
- Failing generated hook artifacts are saved under `tests/failures/`.
- `tests/framework/` is legacy and not used by the current harness.
