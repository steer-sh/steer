---
name: steer-setup
description: Set up steer after installing the plugin — pick a reviewer CLI and model, arm the commit gate for this agent, and place the stable executable. Use for first-time steer setup, when adding a second harness (Claude Code, Codex or Pi) to an existing steer install, or when the user asks to configure, arm or repair the commit gate. Also arms repository checks at turn end and moves a personal check gate into Steer.
user-invocable: true
compatibility: Runs the steer CLI from PATH, or from the bundled launcher under the plugin root (PLUGIN_ROOT on Codex, CLAUDE_PLUGIN_ROOT on Claude Code). Writes ~/.steer/config.json and the invoking harness's hook registration. Needs an installed, authenticated reviewer CLI — claude, codex or agy.
allowed-tools: Bash(steer *), Read
---

# Steer Setup — configure the reviewer, arm the gate

This skill takes a machine from "the plugin is installed" to "a commit here gets
reviewed". It writes only what is missing.

**Never overwrite a reviewer that is already configured.** The reviewer's provider,
model, effort and run count are the instrument the ledger stamps. Changing one splits
every class still collecting evidence across two instruments. A user who set steer up
in Claude Code and is now adding Codex needs the new harness registered and the
reviewer left exactly as it is.

## Step 0: Resolve the CLI

Prefer the launcher bundled with the plugin, and fall back to `steer` on PATH when
the plugin root is not set. Run a **single command**:

```bash
STEER_ROOT="${PLUGIN_ROOT:-${CLAUDE_PLUGIN_ROOT:-}}"
STEER_BIN="${STEER_ROOT:+${STEER_ROOT}/scripts/steer}"
STEER_BIN="${STEER_BIN:-steer}"
"${STEER_BIN}" --help >/dev/null && echo "steer resolved: ${STEER_BIN}"
```

Keep `${STEER_BIN}` for every later step. If that command fails and `${STEER_BIN}` was
the bundled launcher, try bare `steer` once — a machine may carry an installed CLI
beside a plugin root this session cannot read. If neither runs, stop and say steer
could not be resolved. Do not try to install it by another route.

## Step 1: Read what is already there

```bash
"${STEER_BIN}" config show
"${STEER_BIN}" commit-gate status
```

Read both before you write anything. They tell you which of two paths you are on:

- **First-time setup** — `config show` reports every value as the shipped default and
  the settings file is not written yet. Do Steps 2 through 5.
- **Adding a harness** — the settings file exists and names a reviewer, or a harness
  already reads `armed`. **Skip Step 2 entirely.** Do Steps 3 through 5 for the new
  harness only, and tell the user which reviewer the existing configuration uses so
  they can see that it did not change.

Fail soft on either command: report the error in one line and ask the user how to
proceed rather than guessing at the machine's state.

## Step 2: Choose the reviewer (first-time setup only)

Steer ships no reviewer default beyond a fallback. Ask the user, and suggest from
what they actually have installed.

```bash
"${STEER_BIN}" config providers
```

Each row is a provider id, its name, the executable it calls and whether that
executable resolves here. Present the `available` ones first. An executable that
resolves is not proof the reviewer runs — it may be unauthenticated — and a `missing`
one is still a valid choice if the user is about to install it. Say both plainly
rather than filtering the list.

The reviewer is independent of the agent writing the code. A user working in Codex
may prefer the Claude Code reviewer, and a Pi user must pick one of these three
because Pi ships no reviewer CLI of its own. Ask; do not infer from the harness.

Then ask for the model. Suggest one per provider and accept whatever the user types:

| Provider | Suggested model |
|---|---|
| `claude-code` | `sonnet` |
| `codex` | `gpt-5.6-luna` |
| `agy` | `gemini-3.7-flash-high` |

**These are suggestions, never a list to validate against.** A newer model must be
accepted the day it ships. If the user prefers the reviewer CLI's own default, leave
the model empty — the ledger then records `claude-code-default` or `codex-default`, a
label that stays stable while the model behind it changes.

Write the choice:

```bash
"${STEER_BIN}" config set reviewer.provider <provider>
"${STEER_BIN}" config set reviewer.model <model>
```

`config set` names any measurement the change lands in. Show the user what it prints.
Leave `reviewer.effort` and `reviewer.runs` alone unless the user asks: three parallel
runs is the shipped design, and each run costs their reviewer account.

## Step 3: Arm the gate

Ask which agent will make the commits. `commit-gate status` from Step 1 already shows
which harnesses are armed, so name those and do not re-arm them.

```bash
# one of:
"${STEER_BIN}" commit-gate install --harness claude-code --scope global
"${STEER_BIN}" commit-gate install --harness codex --scope global
"${STEER_BIN}" commit-gate install --harness pi --scope global
```

`--scope global` covers every repository. Use `--scope project` in the current
repository instead when the user asks for one repository only.

The command prints the conditions that are not yet true and the command that clears
each one. Relay them; do not summarize them away.

For a Git pre-commit workflow, `commit-gate install --precommit` is the alternative
channel. Choose one channel per repository, or a commit gets reviewed twice.

## Step 4: Place the stable executable

```bash
"${STEER_BIN}" bin install
```

This copies the binary to `~/.steer/bin/steer` and repoints every registration at
that absolute path. A registration naming the bare word `steer` is resolved on PATH
at hook time, and neither harness reports a command it cannot resolve: the tool call
runs, the hook is skipped, and nothing says so.

Then tell the user how to get `steer` in their own shell, because the plugin's PATH
entry belongs to this agent's tool calls and not to their terminal:

```bash
export PATH="$HOME/.steer/bin:$PATH"    # add to ~/.zshrc or ~/.bashrc
```

**Carry existing guards and rules into this harness, on every setup.** Guards and
instruction rules are registered only in the harnesses installed when they were
written. So a harness installed later has no guard runner and no `## Steer Rules`
section. A clone of a repository whose `.steer/hooks` store a teammate committed
is in the same state when the teammate used a different harness or did not commit
their harness configuration. Read the two readings that name this:

```bash
"${STEER_BIN}" status --json > "${TMPDIR:-/tmp}/steer-setup-status.json"
```

Read that file with the Read tool, to its end; a harness can show only the start
of a long command output.

In `posture.masters`, a row whose `state` is `detached` is an installed harness
that runs none of that scope's guards. In `posture.rules`, a row whose `state` is
`drifted` is a harness whose rules section differs from Steer's manifest. For
each scope with either row, name the harnesses to the user and ask before you run:

```bash
"${STEER_BIN}" hooks sync --scope <global|project>
```

Sync registers the runner the store already holds and rewrites each harness's
rules section. Lines written by hand inside that section are removed; the rest of
the file is left alone. In project scope these files are usually committed, so
the user sees a diff in `.claude/settings.json`, `.codex/hooks.json`, `CLAUDE.md`
or `AGENTS.md`. Sync never writes the store. If it refuses because the runner does
not carry the store's guards, relay the refusal. The repair it names, `hooks
rebuild`, rewrites the shared runner for the whole team, so it is the user's call.

## Step 5: Name the trust step, then confirm

Each harness decides whether a registered hook actually runs:

- **Claude Code** — the registration is enforcement. Nothing more to do.
- **Codex** — restart Codex, then review and approve the hook definitions in `/hooks`.
  Until then the gate is registered and not enforcing.
- **Pi** — a project extension runs only after Pi trusts the project. A global
  registration needs no project trust.

Finish by reading the answer back:

```bash
"${STEER_BIN}" status
```

Report its `Commit review` section verbatim. Do not claim the gate is armed on the strength
of the write alone: registration is the weakest of four states, and only an observed
review proves the whole path. Tell the user to make a code change and commit
normally, and that a small or prose-only change may skip review — a skipped review is
not a clean review.

## Step 6: Repository checks (only when the user asks)

A repository check blocks the end of an agent's turn while the repository's
`check.sh` fails. The `steer` and `steer-report` skills arm a repository through
this step too, and pass the scope they chose; setup itself uses `global`.

**First, look for a personal Stop hook that already runs `check.sh`.** Steer
recognizes a personal gate by its `check-gate.py` hook script and the approval
list at `~/.claude/check-gate-approved.txt`. When one exists, do not register the
runner beside it: both would run `check.sh` at every Stop. Move it as the end of
this step describes, instead of steps 1 and 2.

Three separate facts arm a repository, and none implies another:

1. **The Stop runner is registered** in the harness:
   `"${STEER_BIN}" check install --harness <claude-code|codex> --scope <project|global>`.
   Pi has no Stop runner. In Codex, the user must approve the new Stop hook in
   `/hooks`; Steer cannot read that trust record, so `check install` reports the
   step as unknown.
2. **The repository is adopted** on this machine: `"${STEER_BIN}" check adopt`
   inside it. Adoption replays each declared check and runs `check.sh` in
   isolation first, and it refuses on any failure. A registration alone runs
   nothing, and a clone at another path is not adopted.
3. **A run is observed**: `"${STEER_BIN}" check list` shows the latest `check.sh`
   run and each check's latest execution.

Relay what `check install` and `check adopt` print, as in Step 3.

**Moving a personal gate.** Preview the move first:

```bash
"${STEER_BIN}" check migrate
```

The preview writes nothing. Show it to the user: it classifies each approval as
already adopted, importable, without a `check.sh`, or without a repository. Run
`"${STEER_BIN}" check migrate --apply` only after the user says yes. Apply adopts
each importable repository the way `check adopt` does. It replaces the personal
hook with the runner only when no approved repository with a `check.sh` stays
unadopted, so a refused import keeps the personal hook in place. It never deletes
the approval list or the hook script.

## What this skill does not do

- It never authors guards or installs presets. `/steer` writes the fix for a rule.
  Setup only registers the guards a store already holds.
- It never writes a check. `steer check guide` is the authoring contract, and
  `/steer` or `/steer-report` routes a rule or a recurring problem to a check.
- It never registers an intervention or reads the evidence corpora. `/steer-report`
  does that once there is evidence to read.
- It never changes an existing reviewer. `steer config set` is the user's own command,
  and it warns about the measurements the change lands in.
