---
name: steer-review
description: Review the whole branch before opening a pull request — run steer's branch reviewer over base..HEAD, rule on each finding, fix what needs fixing in normal commits, and record the rulings. Use before gh pr create, or when the user asks to review the branch, review the PR before opening it, or check the whole change rather than one commit.
user-invocable: true
compatibility: Runs the bundled launcher at the plugin root (CLAUDE_PLUGIN_ROOT on Claude Code, two directories above this skill on Codex and Pi), or the steer CLI from PATH. Needs a configured reviewer (steer-setup) that can read the repository — codex, claude-code, pi, or an OpenAI-compatible endpoint with reviewer.api.tools on. Writes ~/.steer/state/branch-review and the fix commits the user's rulings call for.
---

# Steer Review — review the branch before the pull request

A commit review reads one commit; this reads the branch's whole net change, so it
finds what only shows across commits: a caller one commit broke that another commit
never touched, a new value one commit writes that no reader handles. It is advisory:
nothing is blocked, and the user decides what happens next.

## Step 0: Resolve the CLI

`<STEER_BIN>` in a command stands for the launcher's absolute path. Write that
path in its place, quoted for the shell when it holds a space, a quote or another
metacharacter, and run the command as it stands, never through a shell
variable or a `${…}` expansion: the harness cannot check those before they run,
so a user's "don't ask again" would never match. The launcher is
`${CLAUDE_PLUGIN_ROOT}/scripts/steer`. If that path begins with `$`, the harness
filled in no plugin root: the launcher is `scripts/steer` two directories above
`<SKILL_DIR>`, the absolute directory that holds this SKILL.md, which the harness
names when it loads the skill; write it without `..`. A `steer …` command named in
prose runs the same way.

```bash
<STEER_BIN> --version
```

If that command fails, run `steer --version` once, and when it succeeds use
`steer` as `<STEER_BIN>` from then on. If neither runs, stop and say steer could
not be resolved.

## Step 1: Make sure HEAD is the change

The reviewer reads files from disk, while the diff ends at HEAD.

```bash
git status --porcelain --untracked-files=no
```

If it lists a path, stop and ask the user whether to commit those edits first. Do
not pass `--allow-dirty` unless the user asks for it.

## Step 2: Run the review

```bash
<STEER_BIN> review branch --json
```

Add `--base <ref>` when the user named the branch this one merges into. A review
can take several minutes; give the command a timeout of at least 15 minutes.

A non-zero exit means the review did not complete: no base branch, uncommitted
edits, a reviewer that cannot run, or no reviewer answered (the JSON then still
prints, with a `trouble` field and any `warnings`). Report the error and each
warning, and stop.

Otherwise read the JSON, in this order. `warnings` lists a settings file that did
not load, an API reviewer with `reviewer.api.tools` off (it read only the diff), an
effort the provider does not document, a reviewed-rule manifest that did not load
or a diff that was not saved; whichever case below ends the
skill, report each warning with it, so a skip or a clean result is not read as
complete.

1. `phase` `skip-empty`: the branch changes nothing reviewable. Say so and stop.
   `phase` `skip-prose`: the branch changes prose files only, which steer does not
   review. Say that the changes were not reviewed, and stop.
2. `answered` below `runs`: in Step 6, say how many reviewers answered.3. `store_error`, or `findings_unparsed` above zero: the rulings cannot be
   recorded, because the review was not stored or a reply's findings cannot be
   numbered. Rule on every finding in `findings[]` and in the `review` text in
   Steps 3 and 4, skip Step 5, and in Step 6 say that the rulings are not recorded
   and why.
4. `finding_slots` zero: run `<STEER_BIN> review rule <review_id> --clean`. Tell the
   user the branch review found nothing, how many of the reviewers answered, and
   that it is not recorded if that command failed. Stop.

Otherwise `findings[]` holds every finding, numbered across the reviewers by
`index`. Those numbers are the ones `steer review rule` takes; the raw `review`
text numbers each reviewer's findings from 1.

## Step 3: Rule on each finding

For each finding, state its load-bearing premise in one sentence, then test that
premise against the code by reading it. Then rule:

- **ACCEPTED** — the premise holds and the failure reaches a use the branch must
  support, a caller or reader it breaks, or a protection boundary. Fix it.
- **ACCEPTED, not fixed** — the premise holds, but the failure needs an input no
  supported use produces and stops safely, or the user said to leave it.
- **REJECTED** — the premise is false. Say what the code does instead.

The reviewer's severity label does not decide a ruling. Two findings that cannot
both be true get one ruling each, and at least one of them is rejected.

## Step 4: Fix, in ordinary commits

Make each accepted fix and commit it as usual; each commit goes through the commit
gate on its own. Run the repository's checks before each commit.

## Step 5: Record the rulings

Record them after the fix commits land, so the receipt shows that HEAD advanced.
Every finding gets exactly one ruling; `qualifier` is `"not fixed"` only on an
acceptance you leave unfixed, and `resolution` is one sentence:

```bash
<STEER_BIN> review rule <review_id> --stdin <<'JSON'
{"rulings": [
  {"index": 1, "verdict": "ACCEPTED", "qualifier": "", "resolution": "<what you changed, file:line>"},
  {"index": 2, "verdict": "REJECTED", "qualifier": "", "resolution": "<why the premise is false>"}
]}
JSON
```

If the command refuses a ruling, correct that ruling and run it again. If it fails
for another reason, the rulings are not recorded: say so in Step 6.

## Step 6: Report

The user has not read the review. Give them one line per finding: what it claimed,
in your own words, the ruling, and what you did. Name each finding ruled ACCEPTED,
not fixed, as a defect the branch still carries. Open a pull request only if the
user asked for it.
