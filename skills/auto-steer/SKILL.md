---
name: auto-steer
description: Runs the steer-report pass without questions — audits the fixes steer installed, mines session and review history for recurring problems, then installs, repairs or removes every fix that clears a stated bar, and ends with one report of what changed, what was held back and what is left to do. Use when the user wants steer to act on its evidence without deciding each step ("auto steer", "just fix what steer finds") and will undo what they do not want.
user-invocable: true
compatibility: Requires the steer CLI on PATH or bundled under scripts/, and the steer-report and steer skills of the same plugin, whose files it reads. Reads what steer-report reads. Changes guards, instruction rules and review prompt entries in the global scope and in the project scope of each repository its evidence names, and repository checks in a repository already adopted. Never commits.
allowed-tools: Bash(steer *), Read, Edit, Write
---

# Auto Steer — run the report pass, act, then report

This skill is the `steer-report` pass with a decision policy in place of its
questions. The user started it so that they decide nothing during the pass: you
audit the installed fixes, mine for new ones, carry out every action that clears a
bar below, and end with one report. The user reads that report and undoes what they
do not want.

Two rules hold from the first step to the last:

- **Ask the user nothing.** A point that needs the user becomes an item in the report.
- **Never run `git commit`, `git add` or `git push`.** Changed files stay in the
  working tree; committing them is the user's step.

`/auto-steer` takes the flags `/steer-report` takes (`--days 7`, `--repo-only`) and
passes them to the same commands.

## Step 1: Load the pass

`<STEER_BIN>` in a command stands for the launcher's absolute path. Write that path in its place, quoted for the shell when it holds a space, a quote or another metacharacter, and run the command as it stands, never through a shell variable or a `${…}` expansion: the harness cannot check those before they run, so a user's "don't ask again" would never match. The launcher is `${CLAUDE_PLUGIN_ROOT}/scripts/steer`. If that path begins with `$`, the harness filled in no plugin root: the launcher is `scripts/steer` two directories above `<SKILL_DIR>`, the absolute directory that holds this SKILL.md, which the harness names when it loads the skill; write it without `..`. If the first command fails because the launcher could not run (no such file, permission denied, or `binary not found`), run that command again with `steer` and use `steer` as `<STEER_BIN>` from then on. A `steer …` command named in prose, here or in a file this skill reads, runs the same way.

The pass lives in the `steer-report` skill, beside this one. An installed skill
directory can be a link, and the Read tool shortens `..` before it follows a link,
so resolve the directory first:

```bash
realpath "<this skill's base directory>"
```

Then Read the whole of `../steer-report/SKILL.md` beside the path it prints, with
the Read tool. That file is written for its own directory: where it or a file it
loads says "this skill's directory", `<SKILL_DIR>` in the reader brief, or a `./`
path, use the `steer-report` directory. Its audit is `../steer-report/audit.md` and
its reading pass `../steer-report/reading.md`; load each when that skill says to.
The routing guide it reads, `../steer/prompts/routing.md`, is beside both skills.

## Step 2: Run the pass under this policy

Follow those files for everything they say about evidence: what to run and read, in
what order, how to group and route, how to validate a fix, which records to write
and what a sentence may claim. This policy replaces every point where they ask the
reader, wait for approval, offer a next step, or say to do nothing unasked. It
decides whether an action happens; they decide how it is done.

- **Step 0 never asks.** If this conversation was not fresh, proceed, and say in one
  line of the final report that a new session gives the pass its full context.
- **The reader of recorded blocks is unchanged.** It records its readings and
  changes nothing. You take the actions below after its account comes back.
- **A question the `steer` skill would ask during an install has these answers.**
  Scope follows the evidence: one repository alone is project, several are
  global. A project fix for another repository is installed there, as "Other
  repositories" below says. A removal this policy decides counts as confirmed, so
  pass `--force` with `--reason`.

### Other repositories

The pass acts in every repository this machine's evidence names, not only the one
it runs in. Take each repository's absolute root from:

- a review sequence's `repo_root`, in the output of
  `<STEER_BIN> commit-gate mine findings --kind <kind> --json --to-file`. Leave
  `--undecided` out of that command: with it, the command records what it showed as
  read. The text dump's `repo=` names the same repository by its directory name.
- an `interventions[]` row whose `artifact.repo` or `scope.repo_root` names a
  repository other than this one, as every `artifact.state` `elsewhere` does.
- a `checks[].repo` or `unmatched_checks[].repo` of `steer audit contests`.

A finding whose evidence names its repository by no root above, such as one seen
only in session history, is held with the repository named as the evidence names
it. Under `--repo-only` or `--project-only` the pass acts in this repository alone.

Before you act in another repository, run:

```bash
<STEER_BIN> -C <root> uncommitted --json
```

When the command fails (the path is gone or is no git checkout), prints a `repo`
other than the root you gave it (the root is no checkout of its own, and git found
the one around it), or lists any path, hold that repository and change nothing in
it: it is not where the evidence placed it, or the files Steer writes there already
hold uncommitted work. Put the repository and that reason under "Needs your
decision".

In a repository that passes, run the audit and the install steps as you run them
here, with `-C <root>` right after `<STEER_BIN>` in every steer command: its own
`status --json` and `fix score --json`, its contests, `fixture mine`, `test`,
`register`, `hooks rebuild --scope project` and `fix register`. Start a reader of
its recorded blocks with the launcher given as `<STEER_BIN> -C <root>`. The bars
below apply unchanged; a repository check there needs that repository's own
snapshot to read `checks.adopted` true. Write `--scope-repo-root` as that root
itself, never as a command substitution, which runs where the pass started. Where
the pass edits a file there, such as a guard source or a stored case, give the Edit
or Write tool its absolute path in that repository. Hold a change there that would
edit an existing file outside the paths `uncommitted` reads, such as a check's
detector script: the command does not show whether that file holds uncommitted
work. Install a global fix once, from here.

When the pass is done in that repository, run `<STEER_BIN> -C <root> uncommitted`
again. The paths it lists, with any file the pass wrote there outside them, such
as a check's detector script, are what the pass changed there.

### New findings: the bar to install

`steer-report` sets the bar to show a finding to a reader who then decides. Install
a finding only when it also meets its tier's bar here. One that misses is held: it
changes nothing and goes under "Needs your decision" with the part it missed.
Record no refusal for a held finding: `steer fix reject` is for a finding you would
drop whatever the user answered.

- **Pre-tool guard.** `steer fixture mine` reconstructs the real violation from a
  recorded diff or session, and that case is stored as a block case. Every block
  case and every boundary allow case passes under `steer test`. The finding does not
  rest mostly on `[ACCEPTED, not fixed]` review findings.
- **Repository check.** The status snapshot's `checks.adopted` is true for this
  repository. `steer check test <id>` passes. `steer check run` exits 0 on the
  current tree with the check wired in; when existing content fails it, undo the
  install and hold the finding: take out any line you added to `check.sh`, then
  run `steer check remove <id> --reason "..."`. That drops the declaration alone,
  so delete what it keeps and writes: the detector script and the `inputs` files
  you wrote, `.steer/checks/cases/<id>.json`, any patch file its cases name, and
  the archive entry its `Archived:` line names. Never run
  `steer check adopt`, `steer check install` or `steer check migrate --apply`: a
  repository's first adoption is the user's decision.
- **Instruction rule.** The problem recurs across sessions, no instruction already
  says it, and you can write the `--not-mechanizable` sentence truthfully: no
  repository check and no guard can detect it.
- **Review prompt entry.** A false-positive finding recurs across review sequences,
  each member holds when re-read against the code at its commit, and no existing
  entry says it.

Hold a finding in any tier when it depends on a workflow question only the user can
answer, when a refused class covers it ("Tracked fixes" below), or when the harm
reading (`steer-report` Step 5, "Harm") finds acceptable work the fix would
obstruct. An unknown harm does not hold a finding; say that it is unknown.

Install through the tier's own workflow, then register the record with `--problem`
and `--intervention` as `steer-report` requires. When `register` refuses the record
and the repair it names needs the user, keep the fix and say in the report that
Steer does not track it yet, with that repair under "Still to do" and, after it,
the `steer fix register` command written out.

### Recorded blocks: act on each reading

These rules cover global guards and the project guards of every repository the pass
acts in. They never touch a `hooks[]` row whose
`source` is `steer`: Steer's self-protection is not repaired, rewritten or removed.

- **`incorrect`.** Follow "Repairing a guard you judged incorrect" in the audit
  file. Name the stored allow case after the block: `audited block <FIRING_ID>`.
  Keep the repair only when every stored case passes. Remove the guard instead,
  with `steer remove`, which archives it, when no repair passes, or when its cases
  held an `audited block` allow case before this pass and the guard as installed
  now made this block: the repaired guard blocked valid work again. Several such
  readings in one pass are one repair, with one case each. After a removal,
  retire every record that binds the guard, as the audit does:
  `steer fix retire <record-id> --reason "..."`.
- **`prevented`, and the block derailed the task.** Rewrite the block message alone.
  Run `steer test` before and after: every stored case must give the same decision.
  Then run `steer hooks rebuild --scope <scope>` and read its `fixtures:` lines; the
  installed guard shows the old message until it does.
  Store the blocked action as a block case named `audited block <FIRING_ID>, message
  rewritten`. When the guard's cases held such a case before this pass, change
  nothing and hold it: a second derailed task is the user's decision.
- **`prevented`, and the task continued cleanly.** Keep the guard.
- **`unresolved`, or a case left unread.** Change nothing.

Agent feedback alone never removes or changes a guard; only a reading does.

Everything else the audit would offer is held, never done: excluding a block that
records no real work, a deeper reading of contested blocks beyond the pass, and
every destination of a check block reading (a defect left in the code, a detector
repair, a `check.sh` wiring repair, a failure outside the task).

### Tracked fixes: act on each record

- **A false match.** Tighten the record as the audit file says and register the
  replacement with `--supersedes`. This changes tracking and no behavior.
- **A confirmed recurrence under an installed fix.** Rework the fix when the
  reworked fix meets its tier's bar above, and supersede the record. Otherwise
  hold it. Never remove a fix because the problem came back.
- **A refused class that came back** (a `rejected` record, listed with its reason
  and reopen condition among the decided classes). While the stored reopen
  condition is not met, do nothing. When it is met, hold the finding under "Needs
  your decision" with the refusal's reason. Never install a fix for a class a
  live rejection covers, and never pass `register` a rejection's id as
  `--supersedes`, or `--force` past one.
- **A fix the record binds is `absent`.** Hold it: restoring or retiring is the
  user's decision.
- **Repairs with a named command** (`steer hooks rebuild`, `steer hooks sync`,
  `steer bin install`, `steer fix describe`, `steer commit-gate backfill --apply`):
  run one when the snapshot's reading calls for it, and list it under "What I
  changed" with what it repaired and that it has no undo. Hold two of them: a sync
  or rebuild of a scope whose `posture.rules[].extra` is not empty, because it
  deletes those lines, and `steer fix scope`, because a scope is frozen once set.
  Never run a command that turns Steer's self-protection off.

## Step 3: Clear the queue

Run the end of `steer-report`'s pass unchanged: the refresh after any change, then
the read mark, with `--corpus` when you read one pile.

## Step 4: The report

This report replaces `steer-report`'s Step 5 questions. Its rules on plain words
("Words the reader sees") and on what a sentence may claim still bind every line.
Write these parts in this order, and leave out a part with nothing in it. When
nothing changed and nothing was held, say so in one sentence. When the pass acted
in more than one repository, group the items of each part by repository, global
fixes first, and name each repository by its root.

When the evidence named a repository other than this one, or the pass ran outside
a repository, open the report by saying what the pass covered, before any count of
changes: the repositories it acted in, and each one it held with its reason. An
`unmatched[]` contest names no repository: say that the pass could not place it.
Otherwise write no coverage line.

1. **What I changed.** One item per action. Each carries:
   - what changed, exactly: an instruction rule or review prompt entry quoted in
     full; for a guard or check, its rule, its scope and its files; for a repair,
     the lines before and after; for a removal, the name and the reason stored.
   - the evidence behind it as a count with its unit: sessions, review sequences
     or inspected blocks.
   - the undo, written out with the launcher's path and every argument filled in,
     so the user can run it as it stands; a change in another repository carries
     `-C <root>` right after the launcher's path in each steer command:
     - an installed fix: its tier's remove command, always with `--reason "..."`
       (`steer remove <name> --scope <scope> --force`, `steer check remove <id>`,
       `steer rules remove <id> --scope <scope>`, `steer commit-gate addendum
       remove <id> --scope <scope>`), then `steer fix retire <record-id> --reason
       "..."` once for each record that tracks the fix (a guard scored per
       harness has one per harness), then `steer fix reject` with the `--class`,
       `--match`, `--kind`, `--corpus` (and `--signal-kind` for a
       `transcript-friction` class) and scope flags you registered or would
       have, `--problem`, `--intervention`, `--reason` and `--reopen`, joined
       with `&&` on one line. The rejection is what stops a later pass from installing the
       same fix again. For a check, also name the line to delete from `check.sh`
       when one names the check.
     - a removal: `steer archive restore <id> --scope <scope>`, with `--tier` and
       `--entry` when `steer archive list` shows more than one entry for the id,
       then, for each record you retired, `&& steer fix register` with that
       record's flags and its install time as `--installed-ts`. A restore
       registers no record and a retired one stays retired, so the new record
       is what tracks the guard again.
     - a repair: the earlier lines to put back and the stored case to delete, then
       `steer hooks rebuild --scope <scope>`. Say that Steer's tracking still
       names the repaired version until a later pass registers the fix again.
2. **Needs your decision.** Each held item with the reason it was held and the
   decision it needs, in one or two sentences. Say once that the user can answer
   here and you will carry it out, and that a later pass raises a held finding
   again only when the problem recurs.
3. **Not acted on.** One line for each thing the pass examined and left unchanged
   where the user has nothing to decide now: a refused problem that matched again
   while its reopen condition is not met, a block whose reading did not settle,
   the blocks the pass's caps left unread, and, when it is so, that no new problem
   cleared the bar. An item that needs the user's answer goes under "Needs your
   decision".
4. **Still to do.** What only the user can do: commit the changed files under
   `.steer/` and the instruction documents, named by path, in each repository (in
   another repository, the paths its last `uncommitted` listed and any other file
   the pass wrote there); approve a Codex
   guard runner in `/hooks` when `register` printed that line; commit the guard
   manifest before a project review prompt entry reaches the reviewer; adopt a
   repository for checks.

A report, to copy for shape; its fixes are invented:

> I acted in `~/code/app` and `~/code/api`, and held `~/code/web`: files Steer
> writes there already hold uncommitted changes. I installed one guard and held one
> finding.
>
> **What I changed**
>
> - **New guard, this repository: never run bare `python` | use `python3`.** You
>   corrected this in 4 sessions. Files: `.steer/hooks/no-bare-python-hook.py` and
>   its test cases. Undo, which also stops me from installing it again: `…`
>
> **Needs your decision**
>
> - **Migrations that drop a column in the release that stops reading it.** A check
>   could block these, but 14 existing migrations fail it. Do old and new code still
>   run side by side during a deploy?
>
> **Not acted on**
>
> - 3 blocks are still unread; a later pass reads them.
>
> **Still to do**
>
> - Commit `.steer/hooks/` and `CLAUDE.md`.
