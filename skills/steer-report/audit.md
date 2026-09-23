# Steer Report — the audit of installed fixes

Step 1 of `SKILL.md` sends you here. Finish this audit, then go back to Step 2.

## Read the fields, report the facts

Each record carries an `observation` with seven blocks. Read those; say what they
show, in the vocabulary `SKILL.md` sets under "Words the reader sees".

| Block | What it holds | What you may say from it |
|---|---|---|
| `counts` | `split_ts`, `observed_ts`, `unit`, and per window `{hits, eligible, first_activity_ts, last_activity_ts}` | The two windows' sizes and dates. Never a rate comparison. |
| `recurrence` | `occurred`, `matches`, `example_kind`, `example`, `example_ts` | Whether the record matched again, and the newest example. Never that the problem came back: read the example first. |
| `firing` | `applicable`, `log_present`, `hook_id`, `scope`, `class_scoped`, `rows`, `units` | What the guard's audit stream recorded, and over what. |
| `fixtures` | `applicable`, `state`, `cases`, `changed`, `verified_at`, `shadowed`, `reason` | Whether the guard's stored cases were last run against the guard installed now. Never that the guard blocks correctly in the field. |
| `checks` | `applicable`, `attribution`, `stream_present`, `registered_host`, `other_host`, `check_id`, `scope`, `executions`, `outcomes`, `blocked`, `changed_detector` | What a repository check's executions on this machine recorded, and over what. |
| `coverage` | the two coverage maps plus `zero_eligible_after` | Which units the window could not account for. |
| `provenance` | `kind` plus the identities per window | Whether the reviewer setup, or the way Steer reads session history, changed between the two periods. |

There are three readings and no fourth:

- **The record matched again.** `recurrence.occurred` is true. **Act on this first.** Open `recurrence.example` and decide whether the record still describes the problem. `example_kind` says what the example is: `adjudicated_finding` is a reviewer's confirmed concern; `cluster_key` is the text a classifier grouped a session's friction by, and is NOT a confirmed escaped failure. **An example that does not belong to the recorded problem is a false match, and it establishes no recurrence.** The record's own regex is then the defect, and the installed fix is not: read the other matched findings with `steer fix match` under the record's scope flags and `--installed-ts`, tighten the regex until it excludes the unrelated ones and still holds the known members, then `steer fix register --supersedes <id>` with the same binding and scope. Say which counts hold unrelated matches, and correct a count only for a window whose matches you read completely — a false match in one window establishes nothing about the other. The replacement record opens a fresh window at registration, so its baseline is not the old baseline minus the false matches; give the replacement's window dates. A recurrence beside `firing.rows: 0` on a known log points at the rule, its coverage or its registration — the guard recorded no block over work the class did match. For a repository check, `checks` separates three cases: zero `executions` on a known stream means the check never ran here — look at the adoption and the `check.sh` wiring; executions that all passed mean the check ran and did not catch the problem — look at the detector; `blocked` above zero means it stopped turns and the problem still came back — look at whether the class is wider than the check. `changed_detector` above zero means some of those runs were an edited detector, not the one registered. On a record with `declined: true`, nothing was installed: the recurrence is a reason to reconsider the refusal, never a fix that failed.
- **The accounting is incomplete.** `firing.applicable` is true while `log_present` is false, or a bound `checks` block reads `stream_present: false` with `other_host: false`. A block with `other_host: true` is not this case: name `registered_host` as the machine that holds the check runs. **Act on this:** name the cause. An absent guard audit log and a repository with no check evidence on this machine have no command; name the cause rather than handing the user a command that changes nothing. A coverage map's `unknown` units do not put a record in this reading: they are left out of the counts, which stand over the rest, and no command recovers them. Say how many were left out, and why. `unknown_scope` is a review sequence that recorded no repository or changed files; the `unknown` remainder beyond `unknown_scope` + `unknown_evidence` is a session that recorded no project; `unknown_evidence` is `pending` + `ledger_missed` + `ledger_overdue`. `ledger_missed` is a ledger the resolver searched for and did not find; nothing routine recovers it. `ledger_overdue` is a ledger the resolver has not read yet; the status entry for `unresolved_rows` owns that repair on this machine, and a row on another host's shard is read there. A `null` `scope` is its own case: the record has no frozen eligibility rule, so its two windows count different opportunity sets. Scope it with `steer fix scope <id>`, which restarts its after-window.
- **The window is quiet.** Eligible work arrived and the class did not. Say that, and stop there. `zero_eligible_after` is the separate case: no eligible work has arrived at all, which is not a quiet window and is not a fault — every freshly installed record reads that way.

**These three names are yours, not the reader's.** Each one tells you which facts
matter. The reader gets the facts. Write the counts, the dates and the limit that
produced the reading, and never the label itself.

| Instead of the label | Write |
|---|---|
| The record matched again | The newest example, its date, what kind of example it is — a reviewer's confirmed concern, or a classifier's grouping that is not a confirmed failure — and whether it belongs to the recorded problem. |
| The accounting is incomplete | Which evidence is missing on this machine, how many units it leaves out of the counts, and the cause. |
| The window is quiet | How much work the fix covers arrived in each period, over what dates, and that the problem did not appear in the second one. |

"This fix is in a quiet window" tells the reader nothing. This tells the reader
what was counted:

> Work this fix covers arrived on 24 days since 12 August, and this problem
> appeared in none of it.

State no direction and no percentage. "Fewer since the fix", "the fix worked", "a 60% reduction" all state a causal claim these counts cannot carry. Say what was counted:

> This problem hit 4 of 37 review sequences before the fix and recurred 0 times in
> the 40 after.

Read `provenance` before you weigh the two windows against each other. When the `before` and `after` lists differ, two instruments counted them — a different reviewer, or a different transcript parser — and the counts describe two measurements rather than one.

For every record with a recurrence you confirmed by reading its matches, open the target file and decide between:

- **Rework** — the fix was aimed at the wrong tell. Fix it, then `register --supersedes <id>` so the old record is retired with a forward pointer and the new one gets a fresh baseline.
- **Remove** — the class is not mechanizable after all, or the defect stopped mattering. Remove the fix with its tier's remove command and a `--reason` (`steer check remove`, `steer remove`, `steer rules remove`, `steer commit-gate addendum remove`), then `retire <id> --reason "..."`. Each remove command archives the fix first; `steer archive list` shows the entries, and `steer archive restore <id>` puts one back through its tier's installer. A restore refuses an id its tier holds now, and a check comes back only when its cases still validate.

A gate that never fires is not free. It costs oracle runtime on every commit and, worse, it reads as coverage that does not exist.

## Show what blocked agents contested

A block's message offers the blocked agent `steer contest FIRING_ID "reason"`. The
agent holds the task context at the moment of the block, so its contest is the
cheapest signal that a block was wrong for the task. It is still a complaint, not a
judgment: it reads no rule and no evidence beyond its own task.

```bash
steer audit contests --json
```

This reads no case evidence and spends nothing from the reading budget below. For
each guard in `guards[]`, report these facts:

- `contested` distinct firings, with each firing's `reasons[].reason` quoted as the
  agent wrote it. Several contests of one firing are one contested firing.
- `unread` as its own number. A contested firing that no receipt names is in both
  counts; never add them, and never call an uncontested firing agreed.
- `not_offered`: blocks whose message carried no contest line, from an older master
  or a failed identifier allocation. Silence from these agents is not agreement, so
  say how many of `firings` could not be contested at all.
- A firing whose `applies` is not `current` was recorded by an implementation
  other than the installed one. Its contest describes that older guard.
- `judgment` is the latest receipt's judgment, when one exists. A contested firing
  judged `prevented` is a disagreement between the agent and a reader; show both.

List `contested_excluded` and `unmatched[]` apart from the counts. An unmatched
contest names a firing no guard store on this machine holds: the row may sit on
another machine, or the master may have failed to log it.

A contest never unblocks anything and never exempts a guard. Offer the user a
deeper audit of the contested firings: read each one with `steer audit show` and
record a judgment, under the budget below. Do not read them unasked, and do not
recommend removal on contests alone.

## Read a sample of the blocks a guard recorded

The counts above say whether a problem is still happening. They never say whether a
guard blocked the right thing. That is settled one case at a time, by reading the
action the guard blocked in its original task context, and separately what the task
did next. `steer audit` is that path.

**This pass has a budget: at most 3 cases, and at most 24,000 bytes of case evidence
across the whole report.** Both numbers are provisional operating choices with no
measurement behind them. Three cases make a pass incremental rather than exhaustive.
24,000 bytes holds the pass below one maximum-length default continuation, so a single
long case cannot consume the report. The budget is spent across every guard together,
not per guard.

Start with the queue. It lists unread cases oldest first and counts the whole queue
before `--limit` cuts the listing:

```bash
steer audit queue --json --limit 3
```

`counts` holds `unjudged`, `continuation_unread`, `queued` and `listed`. A case is
queued for one of two reasons: nobody judged it, or somebody judged the block and
nobody read what the task did next while that transcript is still on this machine.
`coverage[]` holds each guard's account of its own rows. Rows an earlier
implementation recorded are **not** in this listing: no reading of them says what the
guard running now does. `--other` asks for them as separate work.

Then read each case, handing it the bytes you can still afford. Read cases in the
**text** form. `--json` re-encodes and indents the same evidence and costs close to
twice as much, which buys nothing a reader needs:

```bash
steer audit show FIRING_ID --max-bytes 8000
```

`show` enforces the allowance itself. Do not measure its output or stage it to a
file. Its last line is `emitted_bytes N`: subtract that from the 24,000 you started
with and pass what is left to the next call. The line above it says how many
continuation steps you were shown against how many the lookup reached, and names
anything that rendering withheld. A per-step truncation is a different fact: that is
the continuation reader cutting one step's text, not this rendering withholding
whole steps.

`show` refuses an allowance too small to carry the row and the blocked action, and
names the size it needed. That refusal is a result, not a failure: the case costs
more than this pass can spend. Report it as a case you did not read. Never treat
retrieving a case as having inspected it.

Then record one judgment per case you read:

```bash
steer audit record FIRING_ID --judgment prevented \
  --rationale "..." --selection "oldest unread, pass budget 3 cases" \
  --continuation-inspected --inspected-steps 9 \
  --continuation-outcome "..."
```

- `--judgment` is `prevented`, `incorrect` or `unresolved`. Judge the attempted
  action on its own terms: why the action was wrong **in its original task context**,
  not that it violated the rule. A rule violation establishes enforcement only. An
  overbroad rule that validates itself is the failure this separation prevents.
- `unresolved` is a judgment, not a missing one. The case was read and it did not
  settle. A block's correctness and its continuation are separate axes, and an absent
  continuation settles neither.
- A case that records no real work is not judged. A test run under the operator's
  own guards, such as `codex exec` from a scratch directory ordering the forbidden
  command, leaves a real row with no task behind it. Do not record it as
  `unresolved`: tell the user what shows it is a test, and offer
  `steer audit exclude FIRING_ID --reason "..." --evidence PATH`. The row stays in
  the stream; the queue and every count leave it out. Exclude nothing unasked.
- `--inspected-steps` is how many continuation steps actually reached you, which is
  `emission.steps_emitted` and not `steps_available`. Leave it off when you read no
  continuation. `record` refuses a number above what the lookup reached.
- `--selection` records why this case was picked, so a later pass advances coverage
  instead of rereading the same cases.

Selection for this pass is the queue's own order: oldest first, up to the case cap.
Do not invent a sampling scheme. If a case you read shows an over-fire or a derailed
task, say so as an explicit finding and offer the user a focused drill-down or a
reading of every recorded case for that guard. Full inspection is always the user's
option; it is not the default, because reading cost grows with the installed
inventory.

### Repairing a guard you judged incorrect

A block judged `incorrect` is this pass's highest-value output, and it needs a
destination. Enter these steps only after `steer audit record --judgment incorrect`
holds the judgment; that record is the evidence the repair answers to.

1. **Read what fired.** Take the firing's scope, the guard identity it names and
   the action it blocked. `steer audit show` already printed them. The identity
   matters: a guard rebuilt since the firing is not the code that blocked it.
2. **Choose the remedy before editing anything.** An incorrect block does not
   always mean a new guard version. Four remedies are distinct: the store file
   already allows this and only the runner carries the old source (publish it);
   the guard's own logic is too broad (rework it); the rule the guard enforces is
   wrong (revise the rule, then the guard); or the class is not guard-shaped at
   all (remove the guard and say what replaces it). `steer status --json` answers
   the first one: `posture.masters[].sources`.
3. **Reproduce before you change the guard.** Store the blocked payload as an
   `expect: allow` case with the filesystem context it needs, and keep every case
   the guard already holds. Run `steer test <guard>` and confirm the new case
   fails. A payload that already passes standalone has not reproduced the firing:
   the runner normalizes one tool call into several, so the shape the guard saw
   may not be the shape the harness sent. **Never invoke the installed master to
   reproduce or check a firing.** It appends synthetic rows to the live audit
   stream, beside the master's own file, and the unjudged count stops being a
   measure of real work. `steer test` runs byte-identical copies in a temporary
   store for exactly this reason. If a probe the fixture runner cannot express is
   unavoidable, copy the master into a fresh temporary directory, invoke that copy,
   and keep its rows and evidence sidecars out of the live store.
4. **Repair, rebuild, then read the rebuild.** After the edit run
   `steer hooks rebuild --scope <scope>`. It publishes and registers the runner
   and reruns the stored cases, but it reports a fixture failure without failing,
   so read its `fixtures:` lines rather than its exit status. Then check that the
   harnesses you care about register that build: `steer status --json` again.
5. **Supersede the fix record, keep the audit receipt.** The record binds the fix
   and changes with it. The receipt describes the implementation that actually
   fired and stays true; it is why the repair exists.

Say which remedy you chose and what the rebuild's fixture lines reported. A guard
that passes its cases has established what it intercepts under this build, and
nothing about what it will do in the field.

**Close the pass with what it did not read.** Compose that sentence from `counts`
and `coverage`, never from the case cap alone:

> This pass read 3 blocks of `no-bare-python`, oldest first: two prevented the
> failure the rule names and one was an acceptable block of a deliberate use. Nine
> queued cases stay unexamined: seven were never read, and two were judged before
> what the agent did next was read. One case was skipped: its evidence needed 31,000 bytes and
> this pass had 6,000 left.

Three limits hold on every sentence you write from this pass. **An inspected
fraction is never an efficacy estimate**, so no percentage and no direction follows
from these judgments. **Reading three cases certifies nothing about the rest.** And
**new unread work never erases a prior finding** — say "previously read blocks were
correct; nine new cases await reading", not "audit pending".

## The `interventions[]` rows of `steer status --json`

`interventions[]` — the **full** inventory. It carries every live class. Each
row: `id`, `class`, `problem`, `intervention`, `legacy_note`, `match` (the regex
that defines the class), `tier`, `target`, `status` (`active` or `rejected`),
`before` / `after` incidence (`hits`, `units`, `rate`), and for
a refused class `reason`, `reopen` and `rejected_ts`. `problem` and `intervention` are the record's own explanation, authored when the
evidence was in hand: what goes wrong, and what this record does about it. Lead
with them — a `class` is a label and a `match` is a regex, and neither says what
the class is. On a `rejected` row the `intervention` is the one that was
considered, or the sentence `no suitable fix was identified` (`no suitable
intervention was identified` on an older record; tell the reader no fix was found).
`legacy_note` is present only on a record written before those fields existed;
it holds implementation detail, so never read it as either. This is the dedup source
— a class here is decided. `corpus` and `unit` name the evidence the class is
counted over and what one denominator entry is: `review-findings` counts by
`chain`, `transcript-friction` by `session`. Never compare a chain rate to a
session rate. A session-unit row also carries `signal_kind` — the classifier
kind its regex was authored against (`any`, `user_corrections`,
`user_rejections`, `tool_failures` or `hook_blocks`). Each row also carries
its measurement conditions:
- `scope` — the frozen eligibility rule: `{version, all}`, or `{version, repo,
  path}` at version 1, plus a `harness` id list at version 2. The predicates
  combine with AND. `null` is a legacy record: its two windows describe
  different opportunity sets, and no scope is ever inferred for it. Run
  `steer fix scope <id>`.
- `before_coverage` / `after_coverage` — each window's unit accounting:
  `total`, `eligible` (the denominator), `ineligible`, `unknown`,
  `unknown_scope`, `unknown_evidence`, `pending`, `ledger_missed`,
  `ledger_overdue`, `partial_exposure`. An unknown unit is not counted; it
  never shrinks the denominator. `unknown_evidence` is `pending` +
  `ledger_missed` + `ledger_overdue` on a map written by this version.
  `partial_exposure` is the session corpus's own category, for a session that
  straddled the install and so ran under both regimes: `unknown` means the
  scope could not decide, `partial_exposure` means the window could not.
- `observation` — what the corpus recorded. Seven blocks:
  - `counts` — `split_ts` (the boundary: the install, the refusal, or the
    scope migration), `observed_ts` (the read's clock), `unit`, and `before` /
    `after`, each `{hits, eligible, first_activity_ts, last_activity_ts}`. The
    two timestamp pairs are different things: the period bounds express an
    empty window and say how long one has been quiet, and the activity bounds
    name the eligible work actually seen, which an empty window cannot express.
  - `recurrence` — `{occurred, matches, example_kind, example, example_ts}`.
    `example_kind` is `adjudicated_finding` (a reviewer's confirmed concern) or
    `cluster_key` (the text a classifier grouped a session's friction by, which
    is NOT a confirmed escaped failure). Read the example before acting.
  - `firing` — the guard audit stream: `{applicable, log_present, hook_id,
    scope, class_scoped, rows, units, newest_ts}`. `applicable` is false for a
    tier that writes no audit rows and for a record naming no hook.
    `log_present: false` is unknown activity, never a known zero. `scope` is
    `eligible_units` (rows joined to this class's own eligible units, and
    `class_scoped` is then true) or `window_period` (every row this hook wrote
    inside the window's bounds, whatever work it landed on). A `window_period`
    count cannot establish that the guard fired on this class's work.
  - `fixtures` — the guard's stored cases, read against the guard as it is
    installed now: `{applicable, state, cases, changed, verified_at, shadowed,
    reason}`. `state` is `current` (a retained pass whose fixtures, guard, rule
    and master all still match), `stale` (a retained pass that no longer
    matches, or whose components could not all be read — `changed` names them
    and `reason` explains a read failure), `none` (the guard was resolved and
    holds no retained pass; a verification that failed and one that a rebuild
    invalidated leave the same manifest state, so this makes no historical
    claim), or `unavailable` (this reading could not attribute or reach the
    store — `reason` says which). A project-scope record is attributed only to
    the checkout it was registered against, and a record frozen on another host
    reads `unavailable` here. `cases` and `shadowed` describe a retained pass
    and are absent without one; `shadowed` counts cases another guard
    intercepted first, which that pass did not establish this guard blocked.
    A pass is over inputs frozen at verification time: `current` says the
    stored cases passed under this exact guard, rule and master, and says
    nothing about the guard being reached in the field. Never read it as
    efficacy, and never read `none` as a failed guard.
  - `checks` — a check-tier fix's execution stream: `{applicable, attribution,
    stream_present, registered_host, other_host, check_id, repo, scope, class_scoped, executions, outcomes,
    blocked, changed_detector, units, newest_ts}`. It counts executions recorded
    on THIS machine only; review history can hold another machine's commits, so
    a zero there can be work this machine never ran. `attribution` is
    `unavailable` for a record that binds no check. `stream_present: false` is
    unknown activity, never a known zero. `registered_host` is the host that
    registered the fix; with `other_host: true` and no stream here, its runs are
    on that host, and this machine has no coverage gap to repair. `scope` follows `firing`: a review
    record counts every execution in the window's bounds (`window_period`), a
    session record counts Stop runs in its eligible sessions (`eligible_units`).
    `outcomes` counts by execution outcome, `blocked` counts failures whose run
    blocked a turn, and `changed_detector` counts runs of a detector other than
    the one the record registered.
  - `coverage` — `before` / `after`, the same maps as above, plus
    `zero_eligible_after`. That flag is a statement, not a fault: every freshly
    installed record has zero eligible after-units, and absent opportunity is
    not an observed quiet window.
  - `provenance` — `{kind, before, after}`. `kind` is `reviewer` (the reviewer
    identities that counted a chain window) or `parser` (the parser-and-adapter
    identities that produced a session window). Labels only: no rule here
    compares them. A window whose two lists differ was counted by two
    instruments, which is a reason to read the examples rather than the counts.
  - `declined` — true on a refused record. Its after-window is the period since
    the refusal, so a recurrence there implies no fix that failed.
- `artifact` — the fix the record binds and where it stands now:
  `{id, scope, repo, state, detail, removed}`, `null` on a declined record. It is read
  beside the observation and never enters it. `state` is `present` (the entry
  holds the text the record froze), `changed` (the same id with other text, or
  a check whose detector moved), `absent` (a readable store no longer holds
  it), `unreadable` (`detail` names why), `unbound` (a legacy record that names
  no entry), `elsewhere` (a project entry or a check in a repository other than
  the one read) or `unplaced` (a project entry whose record names no
  repository). A review entry is read where the commit gate reads it: the
  global store on disk, the project store at HEAD. **An `absent` fix is not a
  quiet window:** the record measures a fix that is no longer there, so
  put it back with `steer archive restore <id>`, which reinstalls it from the
  store's archive, or retire the record. `removed` is set on a `present` or
  `changed` fix whose store archived a removal of it after the record's split
  (compared in whole seconds): the fix is back, and the window may span a
  period without it. The archive proves a removal was written, not how long the
  fix was gone, so read the record's window against that time before trusting
  its counts; a fresh `steer fix register --supersedes` starts a clean window.
