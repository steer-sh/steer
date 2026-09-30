# Steer Report — the audit of installed fixes

Step 1 of `SKILL.md` sends you here. Finish this audit, then go back to Step 2.

## Read the fields, report the facts

Each record's score row carries an `observation` with seven blocks; join it to the
status summary row by exact `id`. Read those; say what they
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

- **The record matched again.** `recurrence.occurred` is true. **Act on this first.** Open `recurrence.example` and decide whether the record still describes the problem. `example_kind` says what the example is: `adjudicated_finding` is a reviewer's confirmed concern; `cluster_key` is the text a classifier grouped a session's friction by, and is NOT a confirmed escaped failure. **An example that does not belong to the recorded problem is a false match, and it establishes no recurrence.** The record's own regex is then the defect, and the installed fix is not: read the other matched findings with `steer fix match` under the record's scope flags and `--installed-ts`, tighten the regex until it excludes the unrelated ones and still holds the known members, then `steer fix register --supersedes <id>` with the same binding and scope. Say which counts hold unrelated matches, and correct a count only for a window whose matches you read completely — a false match in one window establishes nothing about the other. The replacement record opens a fresh window at registration, so its baseline is not the old baseline minus the false matches; disclose that tracking restarts, with the exact window dates available on request. A recurrence beside `firing.rows: 0` on a known log points at the rule, its coverage or its registration — the guard recorded no block over work the class did match. For a repository check, `checks` separates three cases: zero `executions` on a known stream means the check never ran here — look at the adoption and the `check.sh` wiring; executions that all passed mean the check ran and did not catch the problem — look at the detector; `blocked` above zero means it stopped turns and the problem still came back — look at whether the class is wider than the check. `changed_detector` above zero means some of those runs were an edited detector, not the one registered. On a record with `declined: true`, nothing was installed: the recurrence is a reason to reconsider the refusal, never a fix that failed.
- **The accounting is incomplete.** `firing.applicable` is true while `log_present` is false, or a bound `checks` block reads `stream_present: false` with `other_host: false`. A block with `other_host: true` is not this case: name `registered_host` as the machine that holds the check runs. **Act on this:** name the cause. An absent guard audit log and a repository with no check evidence on this machine have no command; name the cause rather than handing the user a command that changes nothing. A coverage map's `unknown` units do not put a record in this reading: they are left out of the counts, which stand over the rest, and no command recovers them. Keep how many were left out and why for the explanation; surface the gap when it limits the assessment. `unknown_scope` is a review sequence that recorded no repository or changed files; the `unknown` remainder beyond `unknown_scope` + `unknown_evidence` is a session that recorded no project; `unknown_evidence` is `pending` + `ledger_missed` + `ledger_overdue`. `ledger_missed` is a ledger the resolver searched for and did not find; nothing routine recovers it. `ledger_overdue` is a ledger the resolver has not read yet; the status entry for `unresolved_rows` owns that repair on this machine, and a row on another host's shard is read there. A `null` `scope` is its own case: the record has no frozen eligibility rule, so its two windows count different opportunity sets. Scope it with `steer fix scope <id>`, which restarts its after-window.
- **The window is quiet.** Eligible work arrived and the class did not. Say that, and stop there. `zero_eligible_after` is the separate case: no eligible work has arrived at all, which is not a quiet window and is not a fault — every freshly installed record reads that way.

Use these readings to form the executive assessment in Step 5 of `SKILL.md`.
Give the recommendation and its concrete reason. Keep the underlying counts,
dates, excluded units and examples for a requested explanation, unless a fact
changes the decision. Group records needing no action. A short assessment must
still distinguish absent opportunity, absent evidence and no observed recurrence.

> The tracked problem has not appeared again in subsequent work. Leave this fix
> in place; that observation alone does not establish how much it helped.

Read `provenance` before you weigh the two windows against each other. When the `before` and `after` lists differ, two instruments counted them — a different reviewer, or a different transcript parser — and the counts describe two measurements rather than one.

For every record with a recurrence you confirmed by reading its matches, open the target file and decide between:

- **Rework** — the fix was aimed at the wrong tell. Fix it, then `register --supersedes <id>` so the old record is retired with a forward pointer and the new one gets a fresh baseline.
- **Remove** — the class is not mechanizable after all, or the defect stopped mattering. Remove the fix with its tier's remove command and a `--reason` (`steer check remove`, `steer remove`, `steer rules remove`, `steer commit-gate addendum remove`), then `retire <id> --reason "..."`. Each remove command archives the fix first; `steer archive list` shows the entries, and `steer archive restore <id>` puts one back through its tier's installer. A restore refuses an id its tier holds now, and a check comes back only when its cases still validate.

A gate that never fires is not free. It costs oracle runtime on every commit and, worse, it reads as coverage that does not exist.

## Read feedback from blocked agents

A guard block's message offers the blocked agent `steer contest FIRING_ID "reason"`,
and a check.sh Stop block's message offers `steer contest --run RUN_ID "reason"`. The
agent holds the task context at the moment of the block, so its contest is the
cheapest signal that a block was wrong for the task. It is still a complaint, not a
judgment: it reads no rule and no evidence beyond its own task.

```bash
steer audit contests --json
```

This reads no case evidence and spends nothing from the reading budget below. For
each guard in `guards[]`, inspect these facts. Present relevant feedback using
the plain wording in Step 5; routine totals stay in the supporting evidence:

- `contested` distinct firings, with each firing's `reasons[].reason` read in the
  agent's own words. Several contests of one firing are one contested firing.
- `unread` as its own number. A contested firing that no receipt names is in both
  counts; never add them, and never call an uncontested firing agreed.
- `not_offered`: blocks whose message carried no contest line, from an older master
  or a failed identifier allocation. Silence from these agents is not agreement, so
  retain how many of `firings` offered no feedback path; disclose this when it limits a claim about feedback.
- A firing whose `applies` is not `current` was recorded by an implementation
  other than the installed one. Its contest describes that older guard.
- `judgment` is the latest receipt's judgment, when one exists. A contested firing
  judged `prevented` is a disagreement between the agent and a reader; show both.

Keep `contested_excluded` and `unmatched[]` apart from the counts in the evidence. An unmatched
contest names a firing no guard store on this machine holds: the row may sit on
another machine, or the master may have failed to log it.

`checks[]` holds the same account per repository for check.sh Stop blocks:
`blocks`, `contested`, `unread`, `not_offered` (blocks whose reason carried no
contest line) and `contested_excluded`, with each contested block's
`reasons[]` and, when a receipt reads it, the latest reading of each of its
`failures[]`. `unmatched_checks[]` names contested runs no recorded block here
carries. Read and summarize them by the same rules as a guard's.

A contest never unblocks anything and never exempts a guard. Offer the user a
deeper audit of the contested firings: read each one with `steer audit show` and
record a judgment, under the budget below. Do not read them unasked, and do not
recommend removal on contests alone.

## Read a sample of the recorded blocks

The counts above say whether a problem is still happening. They never say whether a
guard blocked the right thing, or whether check.sh stopped a turn for a real failure
of that turn's work. That is settled one case at a time, by reading the block in its
original task context, and separately what the task did next. `steer audit` is that
path, for guard firings and check.sh Stop blocks alike.

**This pass has a budget: at most 3 cases, and at most 24,000 bytes of case evidence
across the whole report.** Both numbers are provisional operating choices with no
measurement behind them. Three cases make a pass incremental rather than exhaustive.
24,000 bytes holds the pass below one maximum-length default continuation, so a single
long case cannot consume the report. The budget is spent across every guard and every
repository's check.sh together: guard firings and check blocks share one queue and
one budget, never one each.

Start with the queue. It lists unread cases oldest first and counts the whole queue
before `--limit` cuts the listing:

```bash
steer audit queue --json --limit 3
```

`counts` holds `unjudged`, `continuation_unread`, `queued` and `listed`, over both
kinds of case. A case is queued for one of two reasons: nobody judged it, or somebody
judged the block and nobody read what the task did next while that transcript is
still on this machine. Each item's `subject` says which kind it is: `guard`, named by
`firing_id`, or `check`, named by `run_id` with its `repo` and its `failures[]`.
`coverage[]` holds each guard's account of its own rows, and `check_coverage[]` each
repository's account of its blocks. Guard rows an earlier implementation recorded
are **not** in this listing: no reading of them says what the guard running now does.
`--other` asks for them as separate work. A check block is listed whatever its
detectors are now; each failure's `applies` says whether a reading of it describes
the detector installed now.

Then read each case, handing it the bytes you can still afford. Read cases in the
**text** form. `--json` re-encodes and indents the same evidence and costs close to
twice as much, which buys nothing a reader needs:

```bash
steer audit show ID --max-bytes 8000
```

ID is the item's `firing_id` or `run_id`. `show` enforces the allowance itself. Do not measure its output or stage it to a
file. Its last line is `emitted_bytes N`: subtract that from the 24,000 you started
with and pass what is left to the next call. The line above it says how many
continuation steps you were shown against how many the lookup reached (for a check
block, the context steps before the block too), and names anything that rendering
withheld. A per-step truncation is a different fact: that is
the continuation reader cutting one step's text, not this rendering withholding
whole steps.

`show` refuses an allowance too small to carry the row and the blocked action, or a
check block's run and failures, and names the size it needed. That refusal is a result, not a failure: the case costs
more than this pass can spend. Report it as a case you did not read. Never treat
retrieving a case as having inspected it.

Then record one judgment per case you read. A guard firing:

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
  command, leaves a real row with no task behind it, and a demo session in a real
  repository leaves a real Stop block. Do not record it as `unresolved`: tell the
  user what shows it is a test, and offer
  `steer audit exclude ID --reason "..." --evidence PATH`. The record stays where it
  is; the queue and every count leave it out. Exclude nothing unasked.
- `--inspected-steps` is how many continuation steps actually reached you, which is
  `emission.steps_emitted` and not `steps_available`. Leave it off when you read no
  continuation. `record` refuses a number above what the lookup reached.
- `--selection` records why this case was picked, so a later pass advances coverage
  instead of rereading the same cases.

A check block is read with the section below, and recorded with one `--failure` per
failure it reads:

```bash
steer audit record RUN_ID \
  --failure check:go-test=valid,introduced,fixed \
  --failure oracle=valid,introduced,fixed \
  --rationale "..." --selection "oldest unread, pass budget 3 cases" \
  --continuation-inspected --inspected-steps 6
```

`--judgment` is for guards only. `record` refuses a failure the run did not report
and a block whose `required` failures are not all read.

Selection for this pass is the queue's own order: oldest first, up to the case cap.
Do not invent a sampling scheme. If a case you read shows an over-fire or a derailed
task, say so as an explicit finding and offer the user a focused drill-down or a
reading of every recorded case for that guard. Full inspection is always the user's
option; it is not the default, because reading cost grows with the installed
inventory.

### Reading a check.sh block

A check block is one Stop run that stopped a turn. `show` prints the run, each
failure it reported, what the records show next, the turn before the block and what
the session did after it. Read each failure on its own: one run can hold a real
failure this turn caused beside a broken detector, and one reading spread over both
is wrong about one of them.

- **Which failures to read.** Each declared check whose execution failed, errored,
  timed out or produced no result is a `check:ID` failure, and is `required`.
  check.sh itself is the `oracle` failure: `required` when no declared check
  accounts for the exit, `optional` beside a failing check, since check.sh exits
  non-zero for that check and may report nothing else. Read it when check.sh's output
  shows a step no declared check owns failing too. `integration` is a check.sh that
  passed without running a declared check, which is wiring and never a failed
  assertion. A check the run never reached (`not reached`) asserted nothing and is
  not a failure.
- **Validity: is the failure real?** `valid` when it reports a real defect in what
  the check reads; `invalid` when it does not: a flaky test, a wrong assertion, or a
  dependence on something outside the code such as a credential, the network or a
  clock. Read the failure's excerpt and check.sh's output, not the check's name.
- **Attribution: did this turn introduce it?** `introduced` when the blocked turn's
  own changes caused it; `pre-existing` when it failed before the turn touched
  anything it reads. A pre-existing failure can still be `valid`: the defect is real
  and it is not this turn's. The context before the block, and earlier runs in
  `steer check blocks`, are the evidence.
- **Continuation: what did the task do about it?** Read only with
  `--continuation-inspected`. `fixed` when the turn changed the code and the failure
  cleared; `routed-around` when it made the check pass without repairing the defect
  (a skipped test, a loosened assertion, an edited detector, a suppression);
  `declined` when it said the failure was not its to clear and changed nothing, which
  is what the block reason asks of a failure outside the task; `interrupted` when the
  person or the harness stopped the turn.
- **Either axis can be `unresolved`.** It is a reading, not a missing one: the case
  was read and the evidence did not settle it.
- **What the records show next is facts, not readings.** "The detector changed before
  the recorded pass" says the detector's digest moved; a real repair moves it too,
  and routing around can leave it unchanged. Read the diff before you call it either.
- **`applies`.** `current` means the reading describes the detector installed now;
  `historic` means the detector changed or the check was removed since, and the
  reading stands only as a reading of that block; `unknown` means a record carries no
  digest; `unmanaged` is check.sh itself or its wiring, which carries no detector
  identity; `unavailable` means the repository or the detector does not read here.
- **`join=unavailable`.** A run from before the reason carried its run line, a
  transcript that is gone, or a harness with no Stop runner leaves no transcript
  reading. Judge validity from the excerpts and the records, leave the continuation
  unread, and say so.

### Where a check reading's repair goes

Each reading points at one destination. Say which, and hand the user the next step
rather than taking it unasked.

1. **Production fix.** `valid`, `introduced`, and the continuation did not fix it
   (`routed-around`, `declined`, `interrupted`). The defect the turn introduced is
   still in the code. Name it and where it is. Nothing in Steer changes.
2. **Detector repair.** `invalid`. The check is wrong, and a check that blocks real
   work for no defect teaches agents to route around it. Repair the detector through
   its lifecycle: edit the script or test, add the blocked case to its cases, and
   `steer check test ID` before anything is declared again (`steer check guide`
   is the authoring contract). Never loosen a detector to hide a `valid` failure.
3. **Oracle wiring repair.** An `integration` failure, or an `oracle` failure from a
   step of check.sh itself. The fix is in check.sh: add the managed line for an
   unreported check (see `steer check guide`), or repair or move the failing step.
4. **Failure outside the task.** `valid` and `pre-existing`. The defect is real and
   the turn did not cause it; the block cost that turn its end. Say that, name the
   defect, and leave the choice to the user: fix it on its own, or decide whether
   this check should stop turns that did not touch what it reads.

**Receipts are preserved through repairs.** A receipt records the detector that ran,
so after a detector repair its failures read `historic`; that is correct, not stale.
Never re-record a reading to make it current, and never exclude a real block because
its detector was wrong. A repaired detector earns trust from its own cases, then
from new blocks read under it.

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

**Keep the assessment bounded by what was read.** Use `counts` and `coverage`
to distinguish inspected cases, cases still unread, and older implementations.
Keep exact queue totals and byte budgets in the supporting evidence. Mention
remaining inspection when it limits a recommendation or needs a user decision.
A successful sample must never sound like approval of every recorded block.

> The two blocks I inspected stopped real mistakes. Other recorded blocks still
> await inspection; I found no reason in these two cases to change the guard.

For a check block, preserve whether the failure was real and whether it belonged
to the task, using plain language:

> The check found a real test failure that was already there before the task
> began. The task ended without repairing it.

Three limits hold on every sentence you write from this pass. **An inspected
fraction is never an efficacy estimate**, so no percentage and no direction follows
from these judgments. **Reading three cases certifies nothing about the rest.** And
**new unread work never erases a prior finding** — say "previously read blocks were
correct; nine new cases await reading", not "audit pending".

## The `interventions[]` rows of `steer status --json`, and their score rows

A live record has two rows, joined by exact `id`. The status snapshot's
`interventions[]` is the **full** inventory, one summary row per live class: `id`,
`class`, `problem`, `intervention`, `tier`, `target`, `status` (`active` or
`rejected`), `hook_id`, `scope`, `artifact`, `corpus`, `unit`, `signal_kind`,
`recurred` (`observation.recurrence.occurred`) and `before` / `after` incidence
(`hits`, `units`, `rate`). The `steer fix score --json` row with the same `id`
carries the detail: `scope`, `before` / `after` (`hits`, `chains`, `rate`, where
`chains` is the unit count whatever the corpus), `before_coverage` /
`after_coverage`, `observation`, and for a refused class `reason` and `reopen`.
The regex that defines a class, `match`, is on the queue's row for each record the
queue lists. `problem` and `intervention` are the record's own explanation, authored when the
evidence was in hand: what goes wrong, and what this record does about it. Lead
with them — a `class` is a label and a `match` is a regex, and neither says what
the class is. On a `rejected` row the `intervention` is the one that was
considered, or the sentence `no suitable fix was identified` (`no suitable
intervention was identified` on an older record; tell the reader no fix was found).
This is the dedup source — a class here is decided. `corpus` and `unit` name the
evidence the class is counted over and what one denominator entry is:
`review-findings` counts by `chain`, `transcript-friction` by `session`. Never
compare a chain rate to a session rate. A session-unit row also carries
`signal_kind` — the classifier kind its regex was authored against (`any`,
`user_corrections`, `user_rejections`, `tool_failures` or `hook_blocks`). Each row also carries
its measurement conditions:
- `scope` — the frozen eligibility rule: `{version, all}`, or `{version, repo,
  path}` at version 1, plus a `harness` id list at version 2, plus `repo_root`
  (one exact repository root) at version 3. The predicates
  combine with AND. `null` is a legacy record: its two windows describe
  different opportunity sets, and no scope is ever inferred for it. Run
  `steer fix scope <id>`.
- `before_coverage` / `after_coverage` (score row) — each window's unit accounting:
  `total`, `eligible` (the denominator), `ineligible`, `unknown`,
  `unknown_scope`, `unknown_evidence`, `pending`, `ledger_missed`,
  `ledger_overdue`, `partial_exposure`. An unknown unit is not counted; it
  never shrinks the denominator. `unknown_evidence` is `pending` +
  `ledger_missed` + `ledger_overdue` on a map written by this version.
  `partial_exposure` is the session corpus's own category, for a session that
  straddled the install and so ran under both regimes: `unknown` means the
  scope could not decide, `partial_exposure` means the window could not.
- `observation` (score row) — what the corpus recorded. Seven blocks:
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
- `artifact` (summary row) — the fix the record binds and where it stands now:
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
