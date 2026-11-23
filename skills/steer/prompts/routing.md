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
- **At commit.** A violation shows in the diff or the commit message, and it
  costs enough that a commit carrying it should be blocked. A reviewer reads the
  diff, so it can judge a rule no test decides, such as "read health from
  `status`; never compute it inside `tui`".
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
| reviewed rule | No check or guard can decide it, a violation shows in the diff or the commit message, and it is costly enough to block a commit | [Reviewed rule](#reviewed-rule) |
| instruction rule | No check or guard can decide it, and the reviewer cannot see a violation or it is too soft to block a commit | [Instruction rule](#instruction-rule) |
| review prompt | The commit reviewer raises the same wrong finding again and again | [Review prompt](#review-prompt) |

Where a check and a guard both meet the enforcement point, take the check. A
guard sees one edit fragment or one command, so a violation assembled across
several edits passes it; a check sees the finished tree.

A reviewed rule and an instruction rule both come after a check and a guard.
Choose either one only after you can say why a repository check and a pre-tool
guard cannot decide the rule, or would misrepresent it. Then ask two questions:

1. Can a reviewer that reads only the diff and the commit message see a
   violation? A rule about the agent's process, such as "ask before
   refactoring" or "read the logs before you guess", fails: the reviewer never
   sees the conversation.
2. Is a violation concrete and costly enough to block a commit? A soft
   preference, where an occasional miss does no harm, fails.

Two yes answers make it a reviewed rule; any no makes it an instruction rule. A
reviewed rule names the paths it covers, so the reviewer reads it only for a
commit that changes one of them. Keep the set small: each rule the reviewer
reads takes part of its attention on every matching commit.

A review prompt is not a rung of the ladder. It applies to one case only, a
recurring reviewer false positive, and changes what the stock reviewer raises.
A reviewed rule that the reviewer applies wrongly is fixed in its own text or
paths, not with a review prompt entry.

An instruction rule reaches the coding agent only. A reviewed rule reaches the
agent and the reviewer. A rule about what the stock reviewer flags is a review
prompt entry.

Tell the user the tier and the reason in one sentence before you install. When
the user asked for one tier and the enforcement point names another, say so and
let the user choose.

## 3. Install

### Repository check

A check runs in the one repository that declares it; it has no global scope. A
rule meant for several repositories needs a check in each.

Two different requests route here:

- **Turn on verification the repository already has.** "Make sure the tests
  pass before the turn ends" asks for no new detector. Read `check.sh` first.
  When it already runs the suite the rule names, the rule needs no detector:
  turn it on by arming the repository. When `check.sh` is absent, `steer check
  init` writes a scaffold; it never changes an existing one.
- **Add a detector for one property.** Run `steer check guide` and follow it:
  it is the authoring contract. The detector needs a case that passes and a
  defect case that fails with the check's diagnostic. `steer check add <id>`
  declares the check only when every case matched. Then add the managed lines to
  `check.sh` as the guide's "Running checks from check.sh" section shows: a
  declared check that `check.sh` never calls reads as not executed. Then arm the
  repository.

**Arming.** Run `steer check migrate` before anything else; it writes nothing.
When it finds a personal check gate, never run `steer check install`: the gate
and the runner would both run `check.sh` at every Stop. Moving the gate replaces
the user's own hook, so it needs the user's yes. Step 6 of the `steer-setup`
skill ([../../steer-setup/SKILL.md](../../steer-setup/SKILL.md)) holds the full
arming order.

The check is done when `steer check list` shows it validated, the repository
adopted, and a run of `check.sh` that executed it.

Limits to state to the user, not work around:

- A detector that needs the network, credentials or a device, or that is slow
  or flaky, does not belong in `check.sh`. Suggest it as a manual or CI check.
- The Stop runner exists for Claude Code, Codex and Pi only, so a check blocks no
  turn in any other harness. Say so. An instruction rule is not a substitute for a
  requested guarantee.
- The runner blocks a failing turn once. When the same failure repeats on an
  unchanged tree, the turn ends without another block. A timeout also lets the
  turn end. Never promise that the tree always passes.

### Pre-tool guard

Follow the generation protocol in the `steer` skill's `SKILL.md`, from Step 1:
hook, fixtures, `steer register`, and the mandatory tests.

### Instruction rule

```bash
<STEER_BIN> rules add <id> --rule "<text>" --scope <project|global>
```

It writes the rule under `## Steer Rules` in each installed harness's
instruction file. It prints the scope's rules before it writes and refuses text
another id already holds. Read the printed rules: a rule that says the same
thing in other words is a duplicate the refusal does not catch.

### Reviewed rule

```bash
<STEER_BIN> rules add <id> --rule "<text>" --review-path <path> [--review-path <path>…] --scope <project|global>
```

It is an instruction rule with a review section: the agent reads the text under
`## Steer Rules`, and the commit gate gives the same text to the reviewer for a
commit that changes a path the rule names. A path is relative to the repository
root and takes one of four forms: `dir/` (every path under it), `*.ext`, `dir/*.ext`
or one file. A project rule must name a path. A global rule spans repositories,
so it names an extension at most, or passes `--review` with no path to reach
every commit. Tell the user the paths with the tier.

The reviewer rates a clear violation SUBSTANTIVE, which blocks the commit, and
names the rule as `<scope>:<id>` on a `RULE:` line. Each reviewed round's ledger
row records the rules it applied (`reviewed_rules_applied`) and the findings that
named each one (`rule_findings`), both by `<scope>:<id>`. A commit that changes
only prose is reviewed when a rule reaches it, and a file moved out of a rule's
directory reaches that rule.

A project rule reaches the reviewer only after the store's `steer-manifest.json`
is committed, because the gate reads project rules from HEAD. Commit the manifest
on its own, before the change the rule is for. `--no-review` makes it an
instruction rule again.

### Review prompt

```bash
<STEER_BIN> commit-gate addendum add <id> --text "<text>" --scope <project|global>
```

The stock review prompt is compiled into the binary; never edit it. A project
entry reaches the reviewer only after the store's `steer-manifest.json` is
committed, because the gate reads project entries from HEAD. Commit the manifest
on its own, before the change the entry is for: a commit that carries both is
reviewed without the entry.
