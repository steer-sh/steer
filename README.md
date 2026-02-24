# steer-skill

`steer` turns natural-language rules into enforceable Claude Code guardrails. Instead of relying on memory, code review luck, or lint configs that do not match your actual policy intent, steer generates real hook scripts, installs them into Claude Code, and keeps rule state synchronized across settings and documentation.

If you care about preventing repeated mistakes, policy drift, or security regressions while coding with AI assistance, this gives you a practical enforcement layer with low operational overhead.

## Install as Claude Code Plugin

```bash
claude plugin add github:steer-sh/steer-skill
```

This gives you the `/steer` command inside Claude Code. The plugin includes prebuilt binaries for all supported platforms — no additional setup required.

## Install Standalone CLI

If you want to use `steer` outside of Claude Code (CI pipelines, scripts, shell workflows):

```bash
bash scripts/install-prebuilt.sh            # user install (~/.local/bin/steer)
bash scripts/install-prebuilt.sh --system   # system-wide (/usr/local/bin/steer)
```

Or build from source:

```bash
cd cli && bash build.sh              # build all platforms
cd cli && bash build.sh --install    # build + install current platform to /usr/local/bin
```

## What This Repo Contains

- `.claude-plugin/plugin.json`: plugin manifest for Claude Code plugin system.
- `skills/steer/SKILL.md`: the steer skill prompt and workflow used by `/steer`.
- `cli/`: Go CLI source (`register`, `list`, `test`, `remove`, `preset`, `verify`, `git-hooks`).
- `bin/`: prebuilt binaries for:
  - `darwin-amd64`
  - `darwin-arm64`
  - `linux-amd64`
  - `linux-arm64`
  - `windows-amd64`
- `scripts/steer` and `scripts/steer.cmd`: platform launchers that dispatch to the correct binary.
- `scripts/install-prebuilt.sh`: standalone CLI installer for use outside the plugin system.
- `tests/`: headless generation + canonical fixture validation harness (dev only).

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

# Manage bundled factory presets
steer preset list
steer preset enable sql-injection-prevention
steer preset enable-bundle security
steer preset sync
steer preset sync --dry-run
steer preset sync --strict
steer preset sync --prune-orphans

# Verify staged content against active project hooks
steer verify --staged

# Install a pre-commit hook that runs steer verify --staged
steer git-hooks install

# CI check for changed files in a commit range
steer verify --changed --base origin/main --head HEAD
```

All commands support `--scope project|global` (default: `project`).

`steer preset sync` reconciles enabled factory presets in the selected scope against the embedded catalog:

- re-installs missing/tampered/drifted factory preset hooks
- auto-ensures internal factory protection presets
- reports orphan factory entries (catalog source IDs no longer present)
- keeps orphans by default, fails with `--strict`, and removes with `--prune-orphans`

## Git Enforcement

Use these commands to enforce rules for humans writing code in git workflows.

```bash
# Local: verify staged/index content before commit
steer verify --staged

# Optional: include both project and global hooks
steer verify --staged --include-global

# Install pre-commit hook that runs staged verification
steer git-hooks install

# CI: verify files changed between two refs/SHAs
steer verify --changed --base origin/main --head HEAD
```

Typical setup:

1. Run `steer git-hooks install` once per repo for local pre-commit checks.
2. Add `steer verify --changed --base <sha> --head <sha>` as a required CI check on pull requests.


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
