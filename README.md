# steer-skill

`steer` turns natural-language rules into enforceable Claude Code guardrails. You describe a rule once; steer generates a deterministic Python hook, self-tests it against boundary cases, installs it, and keeps rule state synchronized across settings and documentation. You pay generation cost once — enforcement is then free and instant on every tool call.

Two things distinguish this from a linter:

- **It blocks before the write, and tells the agent what to do instead.** The hook fires at tool time, and its `stopReason` carries your suggested alternative (the `detect | suggest` syntax below) — so the agent self-corrects in the same turn. A linter reports a problem for a human to triage later; steer redirects the agent to your preferred pattern immediately.
- **It covers shell commands, not just file content.** A rule like "block piping file-reading commands to network tools" intercepts `cat .env | curl ...` at the Bash tool.

Steer enforces lexical and syntactic rules: patterns, imports, call forms, command shapes. Broader semantic rules ("no business logic in controllers") are handled by negotiating them down to a concrete, enforceable rule during creation — the interactive flow asks clarifying questions until the rule is precise enough to compile.

## Install

```bash
npx skills add steer-sh/steer-skill
```

Installs the `/steer` skill into Claude Code (and any other [Agent Skills](https://agentskills.io)-compatible tool).

### Claude Code Plugin (alternative install path)

```bash
claude plugin marketplace add steer-sh/steer-skill
claude plugin install steer@steer-marketplace
```

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

## First Run: Let Your History Write the Rules

Steer can propose rules from evidence instead of guesswork. Run `/steer-report` in Claude Code as your first step after installing:

1. It scans your recent Claude Code transcripts and CLAUDE.md for recurring friction — corrections you keep repeating, patterns you keep reverting.
2. It presents hook candidates with the evidence behind each one (how often, in which projects).
3. Accept a candidate and it hands off to `/steer` to generate, test, and install the hook.

The raw digest is also available outside Claude Code: `steer report --days 30` emits versioned JSON on stdout for your own tooling.

## What This Repo Contains

- `.claude-plugin/marketplace.json`: plugin manifest and marketplace catalog (monorepo — plugin and marketplace in one repo).
- `SKILL.md`: the steer skill prompt and workflow used by `/steer`.
- `skills/steer-report/SKILL.md`: the usage-mining skill behind `/steer-report`.
- `prompts/`: specialist prompt modules for domain-specific hook generation.
- `cli/`: Go CLI source (`register`, `list`, `test`, `remove`, `preset`, `verify`, `git-hooks`, `report`).
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

In practice the soft layer prevents most violations before any hook fires; the hard layer is the deterministic backstop for the rest.

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

# Distribute team policy through a repo-committed lockfile
steer team lock
steer team apply
steer team apply --dry-run
steer team verify --ci

# Verify staged content against active project hooks
steer verify --staged

# Install a pre-commit hook that runs steer verify --staged
steer git-hooks install

# Red-team commits from Codex before exec_command runs
steer commit-gate install --harness codex --scope global
steer commit-gate status

# Remove the Codex tool-time gate (the git pre-commit channel is independent)
steer commit-gate remove --harness codex --scope global

# CI check for changed files in a commit range
steer verify --changed --base origin/main --head HEAD

# Mine recent Claude Code transcripts for hook-suggestion evidence (JSON digest on stdout)
steer report --days 30            # all projects by default
steer report --project-only      # restrict to the current directory's transcripts
steer report refresh             # run the deterministic tier; write the /steer-report work queue
steer report cadence             # SessionStart entrypoint: refresh in the background, nudge if work is pending
```

The steer plugin ships a `SessionStart` hook that runs `steer report cadence` at
every session start. It refreshes the work queue in the background on a weekly
cadence and, when hooks need review, shows one operator line: `run /steer-report`.
Zero setup — the hook is plugin-native.

To run the analysis on a schedule instead (cron, CI), the skill has a headless
form. `/steer-report` is an agent skill, not a shell binary, so run it through the
agent harness:

```bash
claude -p '/steer-report --headless'   # same headless call the test harness uses
```

All commands support `--scope project|global` (default: `project`).

Codex hook definitions require local trust. After installing or changing the global
commit gate, restart Codex and approve its PreToolUse definition in `/hooks`. Status can
locate the index-keyed trust record but cannot verify Codex's internal handler hash. The Codex
adapter reuses the same review engine and ledger as Claude Code and the git pre-commit
adapter; rows identify the client and harness, while Codex rows omit Claude-only
transcript enrichment.

## When a Hook Misbehaves

A hook that blocks wrongly has one immediate fix: remove it.

- **Hooks you registered:** `steer remove <hook-name>` uninstalls the hook and cleans up all managed state in one step. The agent can run this for you — ask it to remove the hook by name.
- **Factory preset hooks:** the self-protection preset blocks the agent from running `steer remove` or `steer preset disable` against factory-managed hooks — a blocked agent must not be able to lift its own guardrails. Run `steer preset disable <name>` (or `steer preset disable-bundle <bundle>`) yourself in a terminal; hooks only intercept agent tool calls, so your own shell is never blocked.

The same self-protection covers steer's managed artifacts (manifest, consolidated hook, team lockfile, audit log), so a misfiring hook can't be worked around by editing those files through the agent either.

`steer preset sync` reconciles enabled factory presets in the selected scope against the embedded catalog:

- re-installs missing/tampered/drifted factory preset hooks
- auto-ensures internal factory protection presets
- reports orphan factory entries (catalog source IDs no longer present)
- keeps orphans by default, fails with `--strict`, and removes with `--prune-orphans`

## Audit Log

Every hook block appends one JSON line to `.claude/hooks/steer-audit.jsonl` — the artifact that answers "what did steer stop this week":

```bash
# Blocks in the last run, by rule
jq -r '.hook_name' .claude/hooks/steer-audit.jsonl | sort | uniq -c | sort -rn

# Full detail for one hook
jq 'select(.hook_name == "sql-injection-prevention")' .claude/hooks/steer-audit.jsonl
```

Each entry carries `timestamp`, `rule`, `hook_name`, `enforcement`, `file_path`, `tool_name`, and a `snippet` of the blocked input. The log is append-only and protected by the self-protection hook — the agent that got blocked can't erase the evidence.

## Team Policy in Git

`steer team` distributes policy the git-native way: a repo-committed lockfile is the source of truth, reviewed like any other file — no cloud push, no live API.

```bash
# Lead: snapshot current steer state and commit it
steer team lock
git add steer-team.lock.json && git commit -m "steer: team policy"

# Everyone else: reconcile a clone against the lockfile
steer team apply

# CI: fail the pipeline on drift
steer team verify --ci
```

The lockfile embeds hook content and integrity hashes, so `apply` works on a fresh clone and rejects tampered lockfiles. By default team policy is a floor, not a ceiling: `apply` installs locked hooks and removes factory extras, but personal hooks outside the lockfile survive. `steer team apply --force` switches to strict mode — local state exactly matches the lockfile.

## Git Enforcement

Tool-time hooks are not a sandbox — content produced through indirect means (a Bash heredoc, a generator script) can slip past them. Commit-time verification applies the same rule corpus as a backstop, and extends enforcement to humans writing code in git workflows.

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

## Supported Models

Steer is tested and certified with **Claude Opus 4.6** (medium reasoning). You can use any Anthropic model to generate hooks via `/steer`, but hook quality and test pass rates are only validated against that configuration.

## Testing Notes

- Tests invoke Claude in headless mode via `/steer --headless ...` and require a working `claude` CLI.
- Canonical case definitions live in `tests/test_data/prompts.yaml`.
- Failing generated hook artifacts are saved under `tests/failures/`.
- `tests/framework/` is legacy and not used by the current harness.
