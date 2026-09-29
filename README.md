# Steer

Steer reviews your agent's code before it commits, then helps you prevent recurring mistakes.

Start with the commit gate. Independent reviewers inspect a proposed change, and your agent checks their findings against the code, fixes what holds up, and explains what it accepted or rejected. The review history becomes evidence for better checks, rules, and instructions.

Steer supports Claude Code and Codex, with a Git pre-commit option for other workflows.

## Review your next commit

### 1. Install the plugin

No clone. For Claude Code:

```bash
claude plugin marketplace add steer-sh/steer-skill
claude plugin install steer@steer-marketplace
```

For Codex:

```bash
codex plugin marketplace add steer-sh/steer-skill
codex plugin add steer@steer-marketplace
```

Codex trusts no plugin hook until you review it. Approve the plugin's hook in `/hooks` to enable its reminder.

Inside a Claude Code session, `/plugin marketplace add steer-sh/steer-skill` and `/plugin install steer@steer-marketplace` do the same thing. Codex installs plugins from the shell; manage or toggle them in-session with `/plugins`.

The plugin carries the CLI, the `/steer`, `/steer-report` and `/steer-setup` skills, and a session-start reminder when review work is pending.

### 2. Run setup

```text
/steer-setup
```

The skill asks which reviewer CLI and model to use, suggesting from the ones installed on your machine; arms the commit gate for the agent you choose; and places `steer` at `~/.steer/bin/steer`. Add that directory to your `PATH` to run `steer` from your own shell.

Setup writes only what is missing. Running it again in a second agent registers that agent and leaves your reviewer configuration alone.

To do the same by hand, or from a checkout of this repository, place the binary first and put it on your `PATH`. `scripts/steer` is the launcher that dispatches to the right prebuilt binary, so this needs neither Go nor `sudo`:

```bash
./scripts/steer bin install                   # places ~/.steer/bin/steer
export PATH="$HOME/.steer/bin:$PATH"          # add to ~/.zshrc or ~/.bashrc

steer config providers                        # which reviewer CLIs are installed
steer config set reviewer.provider claude-code
steer config set reviewer.model sonnet
steer commit-gate install --harness claude-code --scope global
steer status                                  # whether commits here are reviewed
```

Steer runs reviews through an installed, authenticated CLI: `claude`, `codex`, or `agy`. The reviewer can be different from the agent writing your code. Leave the model unset to use the reviewer CLI's own default. By default, three reviewers run in parallel. Reviews take time and use your reviewer's account or API allowance.

For Codex, restart and approve the hook definitions in `/hooks`. To enable the gate for one repository instead, run the command there with `--scope project`.

Then ask your agent to make a code change and commit normally. A reviewer raises concerns as findings; your agent decides each one against the code and reports what it accepted and what it rejected. Small and prose-only changes may skip review; a skipped review is not a clean review.

For a Git pre-commit workflow, use `steer commit-gate install --precommit` in the repository instead. Choose one channel per repository to avoid reviewing commits twice.

## Learn from recurring mistakes

The commit gate records every review and every decision locally under `~/.steer/state`. Steer can also read your agent's session history to find recurring corrections and failed commands.

Open the dashboard to see what is active and what needs attention. `steer status`
answers the narrower question — are commits here reviewed, and what stands in the
way — in a form you can paste or script:

```bash
steer dashboard
steer status
```

With the skills installed, run `/steer-report` in your agent to read the evidence and consider fixes. A recurring problem might call for a repository check, a guard, an instruction, or no change at all. A suggestion you decline is worth recording too — Steer watches whether that problem grows afterwards.

Steer measures each fix against how often the problem happened before it. That takes time, and a change of agent, reviewer or workload can make the two periods unfair to compare. Steer says so rather than treating fewer findings as proof of improvement.

`steer fix` is the command set behind that measurement — register a fix, score it, list what is tracked, or stop tracking one. `steer intervention` is the same set under its former name.

## Add a rule

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

Guard commands default to project scope and register with every installed supported harness. Use `--scope global` or `--harness codex` to choose explicitly. Codex guard registrations also require trust review in `/hooks`.

To remove a custom guard, run `steer remove <hook-name>`. For a factory preset, run `steer preset disable <preset>` yourself in a terminal; factory self-protection prevents an agent from disabling its own safeguards.

## Development and reference

Run the standard checks from the repository root:

```bash
./check.sh
```

This checks Go formatting, builds the CLI, runs vet, and runs the tests. `./check-full.sh` also runs the slower model-backed skill-generation tests.

- [Project reference](CLAUDE.md): architecture, storage, configuration, builds, and test setup.
- [Guard skill](skills/steer/SKILL.md): rule generation and installation workflow.
- [Report skill](skills/steer-report/SKILL.md): evidence review and the fix workflow.
- `steer <command> --help`: command options, including team policy and verification.
