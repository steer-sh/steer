# Routing a rule to a tier

`/steer` reads this guide to route one rule the user asked for. `/steer-report`
reads it to route a class its evidence supports. Routing decides where a fix
lives and how it is installed. It records nothing: a measurement record needs
evidence, and only `/steer-report` writes one.

## 1. Name the enforcement point

Before choosing a tier, decide what must be prevented, and when:

- **Before an action runs.** A command must never execute, or a file must never
  be written with some content. Only a guard intercepts the action itself; a
  check sees the tree after the action happened.
- **When the turn ends.** The finished tree must hold a property: the tests
  pass, the build is clean, a constraint holds across files.
- **At review.** The commit reviewer keeps raising a finding that is wrong.
- **In the agent's judgment.** A preference or a practice that no test can
  decide, such as "prefer composition over inheritance" or "ask before
  refactoring". A mechanical ban here enforces a different rule than the one
  the user stated.

## 2. Take the strongest tier that meets it

| Tier | Use when | Install |
|---|---|---|
| repository check | A local, deterministic test of repository content can decide it | [Repository check](#repository-check) |
| pre-tool guard | A regex or AST test on one edit or one command can decide it at the moment it happens | [Pre-tool guard](#pre-tool-guard) |
| instruction rule | No check or guard can decide it without changing what the rule means | [Instruction rule](#instruction-rule) |
| review prompt | The commit reviewer raises the same wrong finding again and again | [Review prompt](#review-prompt) |

Where a check and a guard both meet the enforcement point, take the check. A
guard sees one edit fragment or one command, so a violation assembled across
several edits passes it; a check sees the finished tree.

An instruction rule is the last tier to reach for. Choose it only after you can
say why a repository check and a pre-tool guard cannot decide the rule, or
would misrepresent it.

A review prompt is not a rung between a guard and an instruction. It applies
to one case only, a recurring reviewer false positive, and a defect never goes
there.

Tell the user the tier and the reason in one sentence before you install. When
the user asked for one tier and the enforcement point names another, say so and
let the user choose.

## 3. Install

### Repository check

A check runs in the one repository that declares it; it has no global scope. A
rule meant for several repositories needs a check in each.

Two different requests route here:

- **Turn on verification the repository already has.** "Make sure the tests
  pass before the turn ends" asks for no new detector. Read `check.sh` if one
  exists, and make sure it runs the existing suite. `steer check init` writes a
  scaffold only when `check.sh` is absent and never changes an existing one.
  Then arm it through Step 6 of the `steer-setup` skill, which runs in the
  order the arming needs.
- **Add a detector for one property.** Run `steer check guide` and follow it:
  it is the authoring contract. The detector needs a case that passes and a
  defect case that fails with the check's diagnostic. `steer check add <id>`
  declares the check only when every case matched. Then add the managed lines to
  `check.sh` as the guide's "Running checks from check.sh" section shows: a
  declared check that `check.sh` never calls reads as not executed. Arm the
  repository through Step 6 of the `steer-setup` skill.

The check is done when `steer check list` shows it validated, the repository
adopted, and a run of `check.sh` that executed it.

Limits to state to the user, not work around:

- A detector that needs the network, credentials or a device, or that is slow
  or flaky, does not belong in `check.sh`. Suggest it as a manual or CI check.
- The Stop runner exists for Claude Code and Codex. Pi has none, so a check
  blocks no Pi turn. Say so. An instruction rule is not a substitute for a
  requested guarantee.
- The runner blocks a failing turn once. When the same failure repeats on an
  unchanged tree, the turn ends without another block. A timeout also lets the
  turn end. Never promise that the tree always passes.

### Pre-tool guard

Follow the generation protocol in the `steer` skill's `SKILL.md`, from Step 1:
specialist prompt, hook, fixtures, `steer register`, and the mandatory tests.

### Instruction rule

```bash
"${STEER_BIN}" rules add <id> --rule "<text>" --scope <project|global>
```

It writes the rule under `## Steer Rules` in each installed harness's
instruction file. It prints the scope's rules before it writes and refuses text
another id already holds. Read the printed rules: a rule that says the same
thing in other words is a duplicate the refusal does not catch.

### Review prompt

```bash
"${STEER_BIN}" commit-gate addendum add <id> --text "<text>" --scope <project|global>
```

The stock review prompt is compiled into the binary; never edit it. A project
entry reaches the reviewer only after the store's `steer-manifest.json` is
committed, because the gate reads project entries from HEAD.
