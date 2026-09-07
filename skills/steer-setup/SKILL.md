---
name: steer-setup
description: Set up steer after installing the plugin — pick a reviewer CLI and model, arm the commit gate for this agent, and place the stable executable. Use for first-time steer setup, when adding a second harness (Claude Code, Codex or Pi) to an existing steer install, or when the user asks to configure, arm or repair the commit gate.
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

Report its `Answer` section verbatim. Do not claim the gate is armed on the strength
of the write alone: registration is the weakest of four states, and only an observed
review proves the whole path. Tell the user to make a code change and commit
normally, and that a small or prose-only change may skip review — a skipped review is
not a clean review.

## What this skill does not do

- It never installs guards or presets. `/steer` writes a guard from a rule.
- It never registers an intervention or reads the evidence corpora. `/steer-report`
  does that once there is evidence to read.
- It never changes an existing reviewer. `steer config set` is the user's own command,
  and it warns about the measurements the change lands in.
