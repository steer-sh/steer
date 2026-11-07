# Steer Report — one reading pass over recorded blocks

`./audit.md` sends you here. Two settings come with the pass:

- **reader** is `subagent` when the report started you to read for it, and `inline`
  when the report reads its own cases.
- **case cap** is 12 for a `subagent` reader and 3 for an `inline` one.

Every `steer` command here runs the launcher the report resolved. A subagent's brief
names it; use that path for every command.

A reader records receipts and nothing else. It excludes nothing, repairs nothing and
edits no file: those steps need the user's decision, which the report asks for.

## The budget

A pass reads at most its case cap, and each case at most 8,000 bytes of evidence.
Both numbers are provisional operating choices with no measurement behind them. The
commands enforce both: `queue --limit` lists no more cases than the cap, and
`show --max-bytes` bounds each case's whole output, so a single long case cannot
consume the pass. Keep no running total. Guard firings and check blocks share one
queue and one cap, never one each. Show each listed case once: a second `show` of a
case spends a second allowance.

## Take the queue

The queue lists unread cases in reading order and counts the whole queue before
`--limit` cuts the listing. A `subagent` reader runs:

```bash
steer audit queue --json --limit 12
```

An `inline` reader runs the same command with its own cap:

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
Neither is a check block that fails declared checks none of which is current, and
has no wiring (`integration`) failure, unless one of those checks reads `moved`: the
records show its detector changed before its next pass, or the session's next Stop
no longer declared it. A block failing only check.sh itself, or the wiring, has no
detector to go stale and is always listed. `counts.other` is how many such
cases only `--other` lists; it asks for them as separate work. Each failure's
`applies` says whether a reading of it describes the detector installed now.

Selection is the queue's own order, up to the case cap. Each item's `unit` is the fix
it stands for: a guard in its store, or one failure kind of one repository's
check.sh. Units take turns, the least-read first, so one busy fix cannot hold the
others back. `stratum` is `flagged` for a case an outcome fact moved to the front,
`unit` for a case its fix still owes, and `reserve` for a case of a fix that already
has three clean readings under its current identity. A flagged case names its facts
in `flags`: `contest` (the blocked agent filed feedback against it),
`silent-contest` (the session stopped again on the same tree with no contest),
`repeat` (the first of several blocks of one unit in one session) or `moved`.
`counts.flagged` says how many are flagged. Read at most one `reserve` case per pass,
and only when no `flagged` or `unit` case is left in the listing. `counts.reserve`
says how many wait there. Do not invent a sampling scheme.

## Show each case

Read cases in the **text** form. `--json` re-encodes and indents the same evidence
and costs close to twice as much, which buys nothing a reader needs:

```bash
steer audit show ID --max-bytes 8000
```

ID is the item's `firing_id` or `run_id`. `show` enforces the allowance itself. Do
not measure its output or stage it to a file. Its last line is `emitted_bytes N`,
the size of this case's output; keep it for the account. The line above it says how
many continuation steps you were shown against how many the lookup reached (for a
check block, the context steps before the block too), and names anything that
rendering withheld. A per-step truncation is a different fact: that is the
continuation reader cutting one step's text, not this rendering withholding whole
steps.

`show` refuses an allowance too small to carry the row and the blocked action, or a
check block's run and failures, and names the size it needed. That refusal is a
result, not a failure: the case costs more than this pass's allowance. Hand it back
as a case you did not read. Never treat retrieving a case as having inspected it.

## Record each case

Record one judgment per case you read. A guard firing:

```bash
steer audit record FIRING_ID --judgment prevented \
  --rationale "..." \
  --selection "reader subagent, case cap 12, stratum unit, unit guard:NAME@STORE" \
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
  repository leaves a real Stop block. Do not record it as `unresolved`, and do not
  exclude it: hand it back with what shows it is a test and the file that shows it,
  so the report can offer the user an exclusion.
- `--inspected-steps` is how many continuation steps actually reached you, which is
  `emission.steps_emitted` and not `steps_available`. Leave it off when you read no
  continuation. `record` refuses a number above what the lookup reached.
- `--selection` records why this case was picked, so a later pass advances coverage
  instead of rereading the same cases. Write your reader and case cap, then copy the
  item's `stratum`, its `flags` and its `unit`, so a later pass knows how each reading
  was picked and by which kind of reader.

A check block is read with the section below, and recorded with one `--failure` per
failure it reads:

```bash
steer audit record RUN_ID \
  --failure check:go-test=valid,introduced,fixed \
  --failure oracle=valid,introduced,fixed \
  --rationale "..." \
  --selection "reader inline, case cap 3, stratum flagged, flags contest, unit check:go-test@REPO" \
  --continuation-inspected --inspected-steps 6
```

`--judgment` is for guards only. `record` refuses a failure the run did not report
and a block whose `required` failures are not all read.

## Reading a check.sh block

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
  shows a step no declared check owns failing too. `integration` is a zero-exit
  check.sh with absent or nonpassing declared results. Absent records are wiring;
  unavailable or skipped results need their own reading, never an assumption that
  an executor line is missing. A check the run never reached (`not reached`) asserted nothing and is
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

## What the pass hands back

A `subagent` reader's final message is this account and nothing else. An `inline`
reader keeps the same account as the evidence behind the report's assessment.

- **The queue's `counts`, `coverage` and `check_coverage`**, as the queue printed
  them before you read anything.
- **One line per clean case**: its ID, its `unit`, its reading (with each
  failure's `applies` for a check block), whether you read the continuation, what
  `show` withheld, and its `emitted_bytes`. A clean case is a guard judged
  `prevented` whose continuation shows no derailed task, or a check block whose
  every failure you read as `valid,introduced,fixed`.
- **Every other case you recorded, in full**: its ID, `subject`, `unit`, `stratum`
  and `flags`; for a guard, the guard identity and the blocked action as `show`
  printed them, and the store its `unit` names; for a check block, the repository
  and each failure's excerpt and `applies`;
  each reading with its rationale; what the task did next; what `show` withheld; and
  its `emitted_bytes`. Name an over-fire or a derailed task as an explicit finding.
- **Each case `show` refused**, with the size it named.
- **Each case that records no real work**, with what shows it is a test and the file
  that shows it.
- **Each listed case you did not read**, and why.
