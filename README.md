# Steer

![Steer catches a missing auth check before commit, finds it recurring, installs a turn-end check, and monitors it](docs/demo.gif)

Steer is an agentic feedback loop. Independent reviewers check your agent's code before it commits. Over time, Steer finds the problems that reoccur and proposes fixes to prevent them: repository checks, guards, and instructions.

Steer supports Claude Code and Codex on macOS and Linux.

Website: [steer.sh](https://steer.sh)

## Install

For Claude Code:

```bash
claude plugin marketplace add steer-sh/steer
claude plugin install steer@steer-marketplace
```

For Codex:

```bash
codex plugin marketplace add steer-sh/steer
codex plugin add steer@steer-marketplace
```

The plugin carries the `steer` CLI, the `/steer`, `/steer-report` and `/steer-setup` skills, a custom statusline for Claude Code, and a session-start reminder when review work is pending.

Codex trusts no hook until you approve it in `/hooks`. Approve the plugin's reminder after you install it, the hooks that `/steer-setup` adds, and the first guard in each scope. Later guards need no new approval. If `/hooks` does not list a new hook, restart Codex.

## Setup

```text
/steer-setup
```

The skill asks which reviewer and model to use, suggesting from the ones installed on your machine. It then arms the commit gate for your agent in every repository and places `steer` at `~/.steer/bin/steer`. Add that directory to your `PATH` to run `steer` from your own shell.

Reviews run through an installed, authenticated CLI: `claude`, `codex`, or `agy` (Google Antigravity). They can also go to any OpenAI-compatible chat-completions endpoint, such as OpenRouter, with an API key that you store with `steer reviewer key set` in your own terminal; every reviewed diff is then sent to that endpoint. The reviewer can be different from the agent that writes your code. By default, three reviewers run in parallel. CLI reviews take time and use your reviewer's account or API allowance. If latency is too high, it's recommended to use a smaller model or use less reasoning effort.

Due to the current state of `agy`, it's not possible to block tool usage with command-line flags, nor is it possible to have ephemeral runs that do not create conversation history. With `agy`, your own `agy` permission rules stored in CLI configuration are the reviewer's only boundary, and each review stays in your `agy` conversation history.

Each review sends the commit message, the diff, and the unchanged files that refer to the changed code to your reviewer's provider. The reviewer can also read other files in the repository with its own tools. Do not commit a secret you would not send to that provider.

If you use both Codex and Claude Code, run `/steer-setup` in both CLI agents. It registers the new agent and keeps your reviewer configuration.

## Usage

### Commit as usual

Ask your agent to make a code change and commit normally. The reviewers raise concerns as findings. Your agent checks each finding against the code, fixes what holds up, and reports what it accepted and what it rejected. Steer will continue reviewing the commits until no accepted findings remain, or until 4 reviews in a row have blocked the commit.

The gate skips a commit when every changed file is prose (`.md`, `.txt` and similar) or when the diff is very small. `CLAUDE.md` and `AGENTS.md` are exceptions to the prose rule.

### Run `/steer-report`

The commit gate records every review and every decision locally under `~/.steer/state`. After some commits, run `/steer-report` in your agent. It reads those records and your agent's session history for recurring review findings, corrections and failed commands.

For each recurring problem, the report proposes a repository check, a guard, an instruction, or no change. Record a suggestion you decline too: Steer watches whether that problem grows afterwards.

A repository check runs `./check.sh` at the end of each agent turn, once you adopt the repository.

Steer measures each fix you accept from the report against how often its problem happened before. Later reports also audit each fix: they read a sample of the blocks it made and check whether they were correct.

To see what is active and what needs attention:

```bash
steer dashboard
steer status        # whether commits here are reviewed, and what stands in the way
```

## Add a rule yourself

The `/steer` skill routes a rule to the fix that can enforce it: a repository check, a guard, or an instruction. A concrete pattern becomes a Python guard, which the skill tests and installs. For example:

```text
/steer never use bare python in shell commands | use python3 instead
```

Guards check tool calls before they run and tell the agent how to correct a violation. They enforce concrete patterns and command forms; broader design decisions still need review. Python 3 is required to run guards.

You can also use bundled presets or verify staged files:

```bash
steer preset list
steer preset enable sql-injection-prevention
steer verify --staged
```

Guard commands default to project scope and register with every installed supported harness. In the global scope, once any harness holds a Steer registration, they register only with the harnesses that hold one, so a harness removed with `steer uninstall --harness` stays removed while another keeps Steer. Use `--scope global` or `--harness codex` to choose explicitly; `--harness` adds a harness back.

To remove a custom guard, run `steer remove <hook-name>`. For a factory preset, run `steer preset disable <preset>` yourself in a terminal; factory self-protection prevents an agent from disabling its own safeguards.

## Other setups

- **One repository only:** run `steer commit-gate install --harness <claude-code|codex> --scope project` in that repository instead of the global gate that setup installs.
- **Git pre-commit:** for other workflows, run `steer commit-gate install --precommit` in the repository. Choose one channel per repository to avoid reviewing commits twice.

## Update

For Claude Code:

```bash
claude plugin marketplace update steer-marketplace
claude plugin update steer@steer-marketplace
```

For Codex:

```bash
codex plugin marketplace upgrade steer-marketplace
```

A plugin update refreshes the skills and the plugin's own copy of `steer`. Your hooks run `~/.steer/bin/steer` and the guard runners Steer generated, so the update reaches them at the next session start: the updated plugin copies its build to `~/.steer/bin/steer`, repoints Steer's hook registrations at it, rebuilds your global guard runner, and says so in one start-up line, such as `steer: hooks updated 0.1.7 → 0.1.8`. It does this only when one of your global Steer registrations already calls `~/.steer/bin/steer`, so it never sets Steer up on a machine where you have not, or where you ran `steer uninstall --apply`. A repository's guard runner is committed, so the start-up message names `steer hooks rebuild --scope project` for you to run in that repository when you choose; if Codex must approve a changed guard command again, it says that too.

Run the updated plugin's `scripts/steer bin install` yourself when the start-up message names it: that happens when the automatic update failed, or when only an older plugin copy runs on this machine.

`steer --version` names the build you run, with its commit and build date.

## Uninstall

Remove Steer before you remove the plugin. The hooks that setup registered call `~/.steer/bin/steer`, not the plugin, so removing the plugin leaves them in place.

```bash
~/.steer/bin/steer uninstall          # prints what it will remove
~/.steer/bin/steer uninstall --apply
```

This removes Steer's commit gate, check runner, guard runner, rules section and status line from each agent's global settings. A status line that Steer wrapped comes back as it was. To remove Steer from one agent only, add `--harness claude-code` or `--harness codex`. If you remove the plugin first, the status line and each commit review name this command. Then remove the plugin:

```bash
claude plugin uninstall steer@steer-marketplace
codex plugin remove steer@steer-marketplace
```

`~/.steer` keeps your review ledgers, guards and reviewer settings in case you reinstall; add `--purge` to delete it. Project-scope registrations and a repository's `.steer/` directory are committed files: `steer uninstall` names the ones in the repository you run it from, and you remove them with a commit.

## Development and reference

Build the CLI with `cd cli && ./build.sh`, then run the standard checks from the repository root against that build:

```bash
STEER_BIN="$PWD/scripts/steer" ./check.sh
```

This checks Go formatting, builds the CLI, runs vet, and runs the tests. `./check-full.sh` also runs the slower model-backed skill-generation tests.

- [Contributing](CONTRIBUTING.md): how to propose a change and what a pull request needs.
- [Architecture](ARCHITECTURE.md): component boundaries and the invariants between them.
- [Project map](CLAUDE.md): files that change together, builds, test setup, and storage locations.
- [Guard skill](skills/steer/SKILL.md): rule generation and installation workflow.
- [Report skill](skills/steer-report/SKILL.md): evidence review and the fix workflow.
- `steer <command> --help`: command options, including team policy and verification.
