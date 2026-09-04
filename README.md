# Steer

Steer reviews your agent's code before it commits, then helps you prevent recurring mistakes.

Start with the commit gate. Independent reviewers inspect a proposed change, and your agent checks their findings against the code, fixes what holds up, and explains what it accepted or rejected. The review history becomes evidence for better checks, rules, and instructions.

Steer supports Claude Code, Codex, and Pi, with a Git pre-commit option for other workflows.

## Review your next commit

### 1. Install the CLI

From a checkout of this repository, on macOS or Linux:

```bash
bash scripts/install-prebuilt.sh
```

This installs `steer` into `~/.local/bin`. Make sure that directory is on your `PATH`.

### 2. Choose a reviewer

Steer runs reviews through an installed, authenticated CLI: `claude`, `codex`, or `agy`. The reviewer can be different from the agent writing your code.

For example, to review through Claude Code:

```bash
steer config set reviewer.provider claude-code
```

Use `codex` or `agy` as the provider instead if you prefer. Steer defaults to `agy`; Claude Code and Codex use their own default model unless you configure one.

```bash
steer config show
```

By default, three reviewers run in parallel. Reviews take time and use your reviewer's account or API allowance.

### 3. Enable the gate

Choose the agent that will make your commits:

```bash
# Claude Code
steer commit-gate install --harness claude-code --scope global

# Codex
steer commit-gate install --harness codex --scope global

# Pi
steer commit-gate install --harness pi --scope global
```

For Codex, restart and approve the hook definitions in `/hooks`. Pi project extensions require project trust. To enable the gate for one repository instead, run the command there with `--scope project`.

Check the registration and reviewer settings:

```bash
steer commit-gate status
```

Then ask your agent to make a code change and commit normally. When reviewers raise concerns, the agent adjudicates them and reports its decisions. Small and prose-only changes may skip review; a skipped review is not a clean review.

For a Git pre-commit workflow, use `steer commit-gate install --precommit` in the repository instead. Choose one channel per repository to avoid reviewing commits twice.

## Learn from recurring mistakes

The commit gate records reviews and adjudications locally under `~/.steer/state`. Steer can also read agent transcripts to find recurring corrections and tool failures.

Use the overview to see what is active and what needs attention:

```bash
steer status
steer dashboard
```

With the skills installed, run `/steer-report` in your agent to review the evidence and consider interventions. A recurring problem might call for a repository check, a guard, an instruction, or no change at all. Rejected proposals are worth remembering too.

Steer tracks interventions against their baselines. Scoring can take time, and changes in agents, reviewers, or work can make a comparison inconclusive. It reports those limits instead of treating fewer findings as proof of improvement.

## Add a rule

The `/steer` skill turns a concrete rule into a Python guard, tests it, and installs it. For example:

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

## Install the skills

The plugin includes `/steer`, `/steer-report`, and a session-start reminder when review work is pending. The commit gate and guards are enabled separately.

For Claude Code:

```bash
claude plugin marketplace add steer-sh/steer-skill
claude plugin install steer@steer-marketplace
```

For Codex:

```bash
codex plugin marketplace add steer-sh/steer-skill
codex plugin add steer@steer-marketplace
```

Review and trust the Codex plugin's hook in `/hooks` to enable its reminder.

For a skills-compatible agent:

```bash
npx skills add steer-sh/steer-skill
```

## Development and reference

Run the standard checks from the repository root:

```bash
./check.sh
```

This checks Go formatting, builds the CLI, runs vet, and runs the tests. `./check-full.sh` also runs the slower model-backed skill-generation tests.

- [Project reference](CLAUDE.md): architecture, storage, configuration, builds, and test setup.
- [Guard skill](skills/steer/SKILL.md): rule generation and installation workflow.
- [Report skill](skills/steer-report/SKILL.md): evidence review and intervention workflow.
- `steer <command> --help`: command options, including team policy and verification.
