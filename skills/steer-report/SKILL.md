---
name: steer-report
description: Audits the fixes steer already installed, then mines Claude Code and Codex transcripts and the commit-review ledger for new fix candidates backed by evidence — repository checks, guards, instruction rules, or review prompt entries. Use for steer onboarding ("what hooks should I create?"), periodic review of installed hooks and fixes, or when the user asks to mine usage history for candidates.
user-invocable: true
compatibility: Requires the steer CLI (report, commit-gate and check subcommands) on PATH or bundled under scripts/. Reads transcripts from ~/.claude/projects and $CODEX_HOME/sessions, and, when present, the commit-review ledger under ~/.steer/state/commit-review (~/.claude/state/commit-review on a machine that has not yet migrated).
allowed-tools: Bash(steer *), Read, Edit, Write
---

# Steer Report — audit installed fixes, then mine for new ones

This skill closes the friction loop. It decides two things, in this order:

1. **Is a problem the loop already answered still happening?** A class that came back gets reworked or removed.
2. **Which new patterns recur often enough to deserve a hook?** Both corpora feed this — transcript friction and commit-review findings.

The audit comes first on purpose. Proposing new hooks is the interesting half, and skipping the audit is how a log of interventions turns into a graveyard of unmeasured good intentions.

Two corpora feed this skill, and either may be empty on a given machine:

- **Transcript friction** — repeated corrections, rejected tool calls, recurring lint/test failures, and instruction-file rules (CLAUDE.md, AGENTS.md) not yet enforced. Mined by `steer report` across every harness with transcripts on this machine.
- **Commit-review findings** — defects the commit-review gate found and adjudicated. Mined by `steer commit-gate mine`. Absent when there is no commit-review ledger on this machine.

An empty corpus produces one line, never an error. An adopter with no commit-review ledger still gets the transcript half — the cold-start front door.

## Example Invocations

```
/steer-report
/steer-report --days 7
/steer-report --project-only
/steer-report --repo-only
```

## Step 0: Fresh-context gate (interactive mode only)

The analysis reads a sizable digest and a finding dump, then reasons over both. If this conversation already contains substantial prior work — more than a few turns, or a large amount of loaded code and discussion — warn before proceeding:

> "A usage report works best in a fresh session: the analysis gets the full context window, and cost is lower because each step doesn't re-pay for this conversation's history. I recommend running `/steer-report` as the first message of a new session. Proceed here anyway?"

Respect the user's choice. If the conversation is fresh, skip the warning and proceed.

## Step 1: Snapshot the loop, refresh the queue, then audit

First read the whole loop's posture and full class inventory in one snapshot:

```bash
STEER_ROOT="${PLUGIN_ROOT:-${CLAUDE_PLUGIN_ROOT:-}}"
STEER_BIN="${STEER_ROOT:+${STEER_ROOT}/scripts/steer}"
STEER_BIN="${STEER_BIN:-steer}"
"${STEER_BIN}" status --json > "${TMPDIR:-/tmp}/steer-status.json"
```

Read `${TMPDIR:-/tmp}/steer-status.json` **once**. The `interventions[]` array is
the **full** inventory — every live class across every tier. This is your dedup
source: when you cluster new candidates in Step 3, drop any class already here. A
class in this list is already decided, so a new hook for it is a rework of that
record. The schema is documented in "The `steer status --json` contract" below.
Fail soft — if the command errors, say so in one line and continue; the audit
below still runs.

A deterministic tier runs this loop's mechanical half — scoring, corpus sweeps, threshold checks — with no model. It may have run on a schedule already; run it again now, synchronously, so you never read a stale queue. It takes seconds and spends no tokens.

```bash
steer report refresh --days 30
```

Then Read the queue **once**: `~/.steer/state/steer-report/queue.json` (`version: 14`). It holds:

- `interventions[]` — full contract records the tier flagged as **asking for a reader**: the class recurred after the split, an absent guard audit log or missing check evidence left the fix's activity unread, the record carries no frozen scope, the fix it binds is `absent` or `unreadable`, or the fix is in place but carries `artifact.removed`. These carry a `match`; they are the audit's action list, pre-filtered. Each carries `problem` and `intervention` — the record's own explanation of what goes wrong and what it does about it, authored when the class was found. Lead your report with those rather than with the class name or the regex, and correct one that no longer describes the evidence with `steer fix describe ID`, which changes no window. Each also carries the conditions the counts were taken under: `corpus` and `unit` (`review-findings` counts by `chain`, `transcript-friction` by `session` — never compare the two rates), `signal_kind` on a session-unit record, `scope` (the frozen eligibility rule, `null` on a legacy record), `before_coverage` / `after_coverage` (`total`, `eligible`, `ineligible`, `unknown`, `unknown_scope`, `unknown_evidence`, `pending`, `ledger_missed`, `ledger_overdue`, `partial_exposure`), `observation` — the seven field blocks documented in the status contract below — and `artifact`, where the fix the record binds stands now.
- `unscored[]` — ids of records no engine reads. Report their existence; do not read a result into them.
- `candidates[]` — **one row per corpus**, each pointing at a pile of evidence no problem covers yet. The transcript row carries `ungrouped_clusters`; the review row carries `undecided_chains` and a `dump` command. The transcript row also carries a few `examples` — the strongest cluster keys, for a reader glancing at the row, never the pile itself; the review row carries none, because its `dump` command prints the findings in full. A row has **no** `match` and no title: grouping the pile and authoring the regex are your job, below. Read the full dumps for the pile; the row only sizes it.
- `corpora[]` — one row per corpus (`review-findings`, `transcript-friction`) with `present` and `size`. Relay this sweep to the user in one sentence.
- `checks[]` — the open steps in the repository the refresh ran in, each with `kind`, `repo`, an optional `check_id`, a `detail` and the `fix` command that clears it. `not-adopted` is a repository declaring checks that this machine never authorized to run at the end of a turn (`steer check adopt`). `never-validated` is a declared check whose cases were never replayed here, so nothing shows it fails on the problem it names (`steer check test <id>`). `integration-failure` is a passing `check.sh` that ran no declared check — the script's wiring, never a failed assertion (`steer check init` prints the managed line to add). A **stale** receipt is deliberately not here: a stale check keeps running, and `steer status --json` carries the label. These are states of a declaration, not recurrence measured over a window, so they carry no count and never enter a before/after reading.

The queue tells you what is pending. The full dumps below are the raw material you cluster over. Read the review-history fixes for the complete audit — the queue lists only the ones asking for a reader, not the quiet ones worth showing too. If this machine has no commit-review ledger, `mine score` reports an empty corpus — say "no fixes to audit on this machine" in one line, then run the reading pass below anyway. Guard evidence is a separate stream from review history, so an absent ledger says nothing about what the installed guards blocked.

```bash
steer fix score
```

`steer fix` is the fix command set — `match`, `register`, `reject`, `scope`, `score`,
`list`, `describe`, `retire`. `steer intervention` is the same set under its former
name and still works, as does `steer commit-gate mine`; `findings`, which dumps the
review corpus rather than managing a fix, lives only under the latter.

Three kinds of evidence answer three different questions, and none of them establishes that a fix works:

- **Stored fixtures.** A current passing result establishes that the stored cases produced their expected allow and block outcomes, under the recorded guard, rule, fixtures and master (`steer fix mine`, `steer test <hook>`). It establishes no field reach.
- **Audited firings.** Reading a blocked action in its original task context supports a judgment about that one attempt: the action would have produced the failure the rule names, or the action was acceptable work, or the evidence does not settle it. These judgments establish no rate.
- **Field counts.** A count reports matches to the recorded problem over eligible work. Read the matches before you call one a recurrence. A count attributes neither a recurrence nor its absence to the fix.

Each record reports before/after incidence over its own unit — the fraction of reviewed commits on which the class appeared for a `review-findings` record, or the fraction of root sessions carrying it for a `transcript-friction` one — split at the install timestamp. **The comparison is uncontrolled and the record makes no efficacy claim.** An agent's standing instruction set — CLAUDE.md, AGENTS.md, imported globals, skills, hooks — can affect its behaviour, and no chain and no session records it, a person edits it by hand between sessions, and an instruction-tier fix edits it directly. So a fall between the two windows is a fact about the corpus and never a fact about the fix.

### Read the fields, report the facts

Each record carries an `observation` with seven blocks. Read those; say what they
show, in the same vocabulary the CLI prints: a **fix** rather than an
intervention, a **recurring problem** rather than a class, a **result** rather
than a verdict, **review history** or **session history** rather than a corpus, a
**review sequence** rather than a chain, **stop tracking** rather than retire.

| Block | What it holds | What you may say from it |
|---|---|---|
| `counts` | `split_ts`, `observed_ts`, `unit`, and per window `{hits, eligible, first_activity_ts, last_activity_ts}` | The two windows' sizes and dates. Never a rate comparison. |
| `recurrence` | `occurred`, `matches`, `example_kind`, `example`, `example_ts` | Whether the record matched again, and the newest example. Never that the problem came back: read the example first. |
| `firing` | `applicable`, `log_present`, `hook_id`, `scope`, `class_scoped`, `rows`, `units` | What the guard's audit stream recorded, and over what. |
| `fixtures` | `applicable`, `state`, `cases`, `changed`, `verified_at`, `shadowed`, `reason` | Whether the guard's stored cases were last run against the guard installed now. Never that the guard blocks correctly in the field. |
| `checks` | `applicable`, `attribution`, `stream_present`, `registered_host`, `other_host`, `check_id`, `scope`, `executions`, `outcomes`, `blocked`, `changed_detector` | What a repository check's executions on this machine recorded, and over what. |
| `coverage` | the two coverage maps plus `zero_eligible_after` | Which units the window could not account for. |
| `provenance` | `kind` plus the identities per window | Whether one instrument or two counted these windows. |

There are three readings and no fourth:

- **The record matched again.** `recurrence.occurred` is true. **Act on this first.** Open `recurrence.example` and decide whether the record still describes the problem. `example_kind` says what the example is: `adjudicated_finding` is a reviewer's confirmed concern; `cluster_key` is the text a classifier grouped a session's friction by, and is NOT a confirmed escaped failure. **An example that does not belong to the recorded problem is a false match, and it establishes no recurrence.** The record's own regex is then the defect, and the installed fix is not: read the other matched findings with `steer fix match`, tighten the regex until it excludes the unrelated ones and still holds the known members, then `steer fix register --supersedes <id>` with the same binding and scope. Say which counts hold unrelated matches, and correct a count only for a window whose matches you read completely — a false match in one window establishes nothing about the other. The replacement record opens a fresh window at registration, so its baseline is not the old baseline minus the false matches; give the replacement's window dates. A recurrence beside `firing.rows: 0` on a known log points at the rule, its coverage or its registration — the guard recorded no block over work the class did match. For a repository check, `checks` separates three cases: zero `executions` on a known stream means the check never ran here — look at the adoption and the `check.sh` wiring; executions that all passed mean the check ran and did not catch the problem — look at the detector; `blocked` above zero means it stopped turns and the problem still came back — look at whether the class is wider than the check. `changed_detector` above zero means some of those runs were an edited detector, not the one registered. On a record with `declined: true`, nothing was installed: the recurrence is a reason to reconsider the refusal, never a fix that failed.
- **The accounting is incomplete.** `firing.applicable` is true while `log_present` is false, or a bound `checks` block reads `stream_present: false` with `other_host: false`. A block with `other_host: true` is not this case: name `registered_host` as the machine that holds the check runs. **Act on this:** name the cause. An absent guard audit log and a repository with no check evidence on this machine have no command; name the cause rather than handing the user a command that changes nothing. A coverage map's `unknown` units do not put a record in this reading: they are left out of the counts, which stand over the rest, and no command recovers them. Say how many were left out, and why. `unknown_scope` is a review sequence that recorded no repository or changed files; the `unknown` remainder beyond `unknown_scope` + `unknown_evidence` is a session that recorded no project; `unknown_evidence` is `pending` + `ledger_missed` + `ledger_overdue`. `ledger_missed` is a ledger the resolver searched for and did not find; nothing routine recovers it. `ledger_overdue` is a ledger the resolver has not read yet; the status entry for `unresolved_rows` owns that repair on this machine, and a row on another host's shard is read there. A `null` `scope` is its own case: the record has no frozen eligibility rule, so its two windows count different opportunity sets. Scope it with `steer fix scope <id>`, which restarts its after-window.
- **The window is quiet.** Eligible work arrived and the class did not. Say that, and stop there. `zero_eligible_after` is the separate case: no eligible work has arrived at all, which is not a quiet window and is not a fault — every freshly installed record reads that way.

**These three names are yours, not the reader's.** Each one tells you which facts
matter. The reader gets the facts. Write the counts, the dates and the limit that
produced the reading, and never the label itself.

| Instead of the label | Write |
|---|---|
| The record matched again | The newest example, its date, what kind of example it is — a reviewer's confirmed concern, or a classifier's grouping that is not a confirmed failure — and whether it belongs to the recorded problem. |
| The accounting is incomplete | Which evidence is missing on this machine, how many units it leaves out of the counts, and the cause. |
| The window is quiet | How much eligible work arrived in each window, over what dates, and that the class did not appear in the second one. |

"This fix is in a quiet window" tells the reader nothing. "Eligible work arrived on
24 days since 12 August and this problem appeared in none of it" tells the reader
what was counted.

State no direction and no percentage. "Fewer since the fix", "the fix worked", "a 60% reduction" all state a causal claim these counts cannot carry. Say what was counted: "this problem hit 4 of 37 review sequences before the fix and recurred 0 times in the 40 after."

Read `provenance` before you weigh the two windows against each other. When the `before` and `after` lists differ, two instruments counted them — a different reviewer, or a different transcript parser — and the counts describe two measurements rather than one.

For every record with a recurrence you confirmed by reading its matches, open the target file and decide between:

- **Rework** — the fix was aimed at the wrong tell. Fix it, then `register --supersedes <id>` so the old record is retired with a forward pointer and the new one gets a fresh baseline.
- **Remove** — the class is not mechanizable after all, or the defect stopped mattering. Remove the fix with its tier's remove command and a `--reason` (`steer check remove`, `steer remove`, `steer rules remove`, `steer commit-gate addendum remove`), then `retire <id> --reason "..."`. Each remove command archives the fix first; `steer archive list` shows the entries, and `steer archive restore <id>` puts one back through its tier's installer. A restore refuses an id its tier holds now, and a check comes back only when its cases still validate.

A gate that never fires is not free. It costs oracle runtime on every commit and, worse, it reads as coverage that does not exist.

### Read a sample of the blocks a guard recorded

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

#### Repairing a guard you judged incorrect

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
> queued cases stay unexamined, seven of them never read and two judged with an
> unread continuation. One case was skipped: its evidence needed 31,000 bytes and
> this pass had 6,000 left.

Three limits hold on every sentence you write from this pass. **An inspected
fraction is never an efficacy estimate**, so no percentage and no direction follows
from these judgments. **Reading three cases certifies nothing about the rest.** And
**new unread work never erases a prior finding** — say "previously read blocks were
correct; nine new cases await reading", not "audit pending".


## Step 2: Gather new evidence from both corpora

### 2a. Transcript friction

Use `steer` from PATH if available, otherwise fall back to the plugin root (`PLUGIN_ROOT` on Codex, `CLAUDE_PLUGIN_ROOT` on Claude Code). Redirect the digest to a temp file so it enters context exactly once — via Read, not via command output. Run a **single command**:

```bash
STEER_ROOT="${PLUGIN_ROOT:-${CLAUDE_PLUGIN_ROOT:-}}"
STEER_BIN="${STEER_ROOT:+${STEER_ROOT}/scripts/steer}"
STEER_BIN="${STEER_BIN:-steer}"
"${STEER_BIN}" report --days 30 > "${TMPDIR:-/tmp}/steer-digest.json"
```

Then Read `${TMPDIR:-/tmp}/steer-digest.json` **once**. Do not cat the file and do not re-read it later — everything needed for analysis is in that single read.

All projects and every harness are scanned by default (cross-project, cross-harness habits are the strongest candidates). Pass through any flags the user supplied (`--days N`, `--project-only`, `--repo-only`, `--harness claude-code|codex`, `--max-digest-bytes N`). `--project-only` reads the working directory alone; `--repo-only` reads every session launched anywhere inside the current checkout, and the two are mutually exclusive.

The digest — not the queue — is the transcript corpus. The queue carries one row saying how many correction clusters are ungrouped; the clusters themselves are here. Most hold a single occurrence, because a cluster key is the user's own wording and merges verbatim repeats and nothing else: two complaints about one habit, phrased differently, arrive as two clusters. A cluster is therefore never a problem and its `count` is never a recurrence. Merging them into classes is your job, not the tier's.

Digest schema (`digest_version: 3`):

- `stats` — coverage: `sessions_scanned`, `threads_scanned`, `bytes_scanned`, `clusters_found`, `clusters_emitted`, `truncated`. **Always relay coverage to the user in one sentence.** If `truncated` is true, say so and suggest narrowing (`--days 7`) or raising `--max-digest-bytes`.
- `stats.harnesses[]` — per harness: `harness`, `files_scanned`, `sessions_scanned`, `threads_scanned`, `bytes_scanned`, `entries_scanned`, `entries_skipped`. A harness missing from this list was never reached; a harness present with zeros has no evidence on this machine. Say which when a harness contributes nothing.
- The session is the unit and a subagent thread is provenance under its root session. `threads_scanned` reports how many threads rode along; it is never added to `sessions_scanned`.
- `instruction_files[]` — per instruction document (CLAUDE.md or AGENTS.md), tagged with its `harness`: `steer_rules` (every line of the `## Steer Rules` section), `guard_rules` (the subset a guard in that scope enforces) and `directives` (standing instructions with their section heading, NOT yet enforced). A guard writes its rule into the section and so does an instruction rule, so a `steer_rules` line absent from `guard_rules` is an instruction rule: the loop decided on it, and nothing enforces it.
- `signals.user_corrections[]` — heuristic correction candidates: user turns matching correction language. A match is not a confirmed correction, so read the examples before you count one as evidence, and do not treat a missing cluster as proof that no correction happened. Each cluster: `pattern`, `count`, `sessions` (distinct sessions), `first_seen`, `last_seen`, `examples` (up to 3 raw snippets).
- `signals.user_rejections[]` — tool calls the user declined at the permission prompt (pattern is `Tool: target`).
- `signals.tool_failures[]` — recurring lint/test/compile failures from shell-tool output, whichever harness ran the shell.
- `signals.hook_blocks[]` — operations already blocked by existing hooks. These are enforcement *working*; use them only to avoid suggesting duplicates.

The digest is size-capped: the CLI evicts the lowest-scoring clusters to fit, and reports honesty in `stats`.

### The `steer status --json` contract (`status_version: 26`)

Step 1 reads this snapshot for posture and the full class inventory. The unit is
the failure class and what the corpus recorded about it, not the hook count. This
is the one copy of the schema in the bundled skills; change it with
`cli/internal/status/status.go`. Fields:

**Read every count here as an observation.** The windows are uncontrolled: the
agent's standing instruction set can affect its behaviour, no chain and no
session records it, a person edits it by hand between sessions, and an
instruction-tier fix edits it directly. So the snapshot reports hits,
eligible units, window bounds, coverage, guard firings and the identities that
counted each window, and it reports no efficacy verdict. The intervention's
contribution to any change in these counts is unresolved. v11 removes `verdict`,
`p_value`, `effect`, `required_after`, `confounds`, `composition`,
`instrument_drift`, `firings_applicable`, `attribution` and `chain_rate`, and adds
`observation`. v12 adds `checks`: the repository check tier, whose registration,
authorization and observed execution are three readings and not one. v13 adds
`observation.checks`: a check-tier fix's executions in its window, counted on
this machine only. v16 adds `observation.checks.registered_host` and
`other_host`: a fix registered on another host is not missing coverage here.
v17 adds `artifact`: the fix a record binds and where it stands now. v18 adds
`rules` and `addenda`: the instruction and review entries each store holds.
v19 adds `posture.masters[].state` `detached` and `store`: an installed harness
that runs none of a scope's guards. v20 adds `artifact.removed`: a fix in place
now that its store's archive records as removed after the record's split. v21
removes `posture.denials`, `telemetry.denials_rows` and the `auto-denials` corpus.
v25 adds `observation.fixtures`: what this machine knows about the stored
cases behind a guard-tier fix.
v22 adds coverage `ledger_missed` and `ledger_overdue`: a missing ledger is
disclosed and no longer puts a record under repair. v23 does the same for an
`unknown_scope` unit and a session with no project. v24 adds `store_problems`:
a guard store whose manifest a reader refused, so an unreadable store no longer
reads as a store holding nothing. v26 adds `posture.masters[].sources` and
`store_sources`: the guard source a runner carries, read against the store's own
guard files.

- `status_version` — the schema version. `--json` is a stable agent contract.
- `posture` — the arming state: `instrument` (reviewer model / runs / prompt
  digest), `reviewer`, `ledger` path, and `commit_gate[]` / `resolver[]`. Each row carries `harness`, `scope`, `registered`, `effective`,
  `trust`, `command`, `resolvable`, `observed` and `observed_corpus`. A
  registration is three separate readings, and only the first is what the
  configuration says. `command` is the command the registration names, verbatim.
  `resolvable` is whether that command resolves — `resolvable`, `missing`,
  `unknown`, or `not-registered` when there is no command to test. Treat it as a
  PROXY, never a proof: the hook runs under the harness's environment, whose
  PATH may differ, so it can prove a command absent and never prove one runs.
  **Act on `missing`:** neither harness reports a command it cannot resolve — the
  tool call runs, the hook is skipped, and nothing says so — and
  `steer bin install` repoints every registration at `~/.steer/bin/steer`.
  `observed` is the epoch second of the newest row the channel wrote to
  `observed_corpus`, 0 when it never wrote one, and both fields are absent when
  the channel writes no corpus of its own (the resolver). Never observed is not
  proof of breakage: nobody may have attempted the action the channel fires on.
  A corpus records no scope, so both scopes of one harness report the same
  evidence. One guard store serves both harnesses but each harness holds its
  own registration, so a scope alone does not identify a row: `commit_gate` and
  `resolver` report one row per harness per scope. `trust` is `not-applicable` (the harness reviews no hook trust),
  `record-present-unverified`, `missing` or `unknown` — Steer never claims to
  have verified a harness's internal hash. The resolver is the Stop hook that
  joins ledger rows to transcripts; it arms with the commit gate on the same
  scope. `unresolved_rows` counts the rows still owing that join — a window
  cannot count them until the resolver reads them, and `steer commit-gate backfill --apply`
  reads them now.
  `masters[]` is the guard master's runtime provenance, one row per harness per
  scope: `path`, `runtime` (the digest of the Python runtime spliced into it),
  `version` (the steer that wrote it), `expected` (this binary's runtime digest)
  `state` and `observed` (the epoch second of the last block in the audit log
  beside this registered master; the log holds blocks only, so a master that
  allowed every call reads as 0). `state` is `current`, `stale` (an older runtime, so a firing
  writes fewer audit fields than the join reads), `unknown` (an unstamped
  master — every store built before the stamp existed, which never reads as
  current), `detached` or `not-registered`. `detached` is an installed harness
  that runs none of the scope's guards: the store holds guards and the harness
  registers no runner for them, or one other than the store's. A store with guards
  and no runner file never reads `detached`: its repair is a rebuild.
  Its `store` names the runner the store holds. `trust` is the harness's approval of the
  registered runner, in the same values as a `posture` row's `trust`, and is
  absent when no runner is registered. A Codex runner reading `missing` runs no
  guard, while the guard's rule still reaches the agent as text; approve the
  definitions in `/hooks`. The row reads the REGISTERED master, not the one
  the store resolves to: a migrated machine holds a master at both paths. **Act
  on `stale` or `unknown`:** run `steer hooks rebuild --scope <scope>`, which
  regenerates the master AND re-registers it in one operation. **Act on
  `detached`:** run `steer hooks sync --scope <scope>`, which registers the
  runner the store already holds and writes nothing in the store; a clone of a
  repository whose guards were committed reads `detached` until it runs.
  `sources` reads the guard source the REGISTERED runner embeds against the
  scope's store files, and is a fourth fact beside registration, runtime
  freshness and detachment: a runner carrying this binary's runtime can still run
  source the store file no longer holds. Its `state` is `complete`, `no-master`,
  `unavailable` or `not-applicable`, and only `complete` supports a reading —
  `guards[]` then names each guard `matches`, `differs`, `absent-from-master` or
  `absent-from-store`. An `unavailable` comparison is not agreement. `differs` is
  directionless: it says the runner runs something other than the file, never
  which one is wanted, so read the guard file before rebuilding. A detached row
  also carries `store_sources`, the same reading for the runner the store holds,
  because the two choose different repairs: a store runner that already covers
  the store needs `sync`, not `rebuild`.
  `rules[]` reads each installed harness's `## Steer Rules` section against the
  manifest, one row per installed harness per scope; a harness that is not
  installed gets no row. `path` is the instruction file (CLAUDE.md or AGENTS.md),
  `expected` counts the rules the manifest names in that scope, `missing` lists
  the rule ids whose text the section does not carry, and `extra` lists section
  lines the manifest does not name, which the next sync removes. `state` is
  `current`, `drifted` or `unknown` (the file could not be read). A record whose
  rule is missing from a harness's file stays live while that agent never reads
  the rule: a harness installed after the rule was written, or a hand edit to the
  file, causes it. **Act on `drifted`:** run `steer hooks sync --scope <scope>`,
  which rewrites every installed harness's section from the manifest.
- `checks` — this repository's check tier on this machine, with an empty reading
  when `steer status` ran outside a repository. `repo` is the resolved Git root,
  `manifest` says whether it declares checks, and `adopted` / `adopted_at` say
  whether THIS machine authorized its `check.sh` to run at the end of a turn.
  Registration, authorization and observed execution are three separate readings,
  and a clone carries none of the second. `runner[]` is the Stop runner's
  registration in the same shape as a `posture` row, one per harness per scope;
  its `observed` is the newest run that harness's runner recorded, and its
  `trust` is `unknown` wherever a harness reviews hook trust, because Steer reads
  no Stop trust record for that event. `checks[]` is one row per declaration:
  `id`, `kind` (`script` or `go-test`), `binding` (the script, or the package and
  test), `validation` (`current`, `stale` or `none`) with `changed` naming what
  moved since the receipt in `receipt` / `validated_at`, and `execution` /
  `executed_at` / `execution_run` from the newest execution record here. **A
  stale check keeps running:** staleness is a label on the proof, never a repair
  the loop demands, and `steer check test <id>` replays the cases when a fix
  registration needs fresh proof. An absent `execution` is unobserved and never a
  pass — an aggregate `check.sh` outcome attributes no result to one assertion.
  `latest_run` is the newest run that did any work: `run_id`, `source`,
  `harness`, `outcome`, `disposition`, `finished`, `missing[]`,
  `integration_failure` and `refused_after`. A run the repository lock turned
  away ran nothing, so it never stands in for the result of the run that held the
  repository; `refused_after` is when such a run was turned away. **Act on
  `integration_failure`:** a passing `check.sh` that ran no declared check is the
  script's wiring and never a failed assertion — add the managed line naming
  `steer check exec --all` or `steer check go-test`.
- `interventions[]` — the **full** inventory. It carries every live class. Each
  row: `id`, `class`, `problem`, `intervention`, `legacy_note`, `match` (the regex
  that defines the class), `tier`, `target`, `status` (`active` or `rejected`),
  `before` / `after` incidence (`hits`, `units`, `rate`), and for
  a refused class `reason`, `reopen` and `rejected_ts`. `problem` and `intervention` are the record's own explanation, authored when the
  evidence was in hand: what goes wrong, and what this record does about it. Lead
  with them — a `class` is a label and a `match` is a regex, and neither says what
  the class is. On a `rejected` row the `intervention` is the one that was
  considered, or the sentence `no suitable intervention was identified`.
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
- `summary` — `total` plus `counts` by action section (`inspect`, `repair`,
  `observe`).
- `chains[]` — recent review chains, findings split `accepted` / `rejected`.
- `attention` — the subset asking for a reader: `interventions[]` (a recurrence,
  an absent audit log or check evidence, or no frozen scope), `candidates[]` (no `match` yet),
  `unscored[]` (ids of records no engine reads), and `checks[]` — the open steps
  in this repository's check tier, the same rows the queue carries.
- `telemetry` — corpus sizes: `review_chains`, `audit_firings`,
  `session_snapshots`, plus `harnesses[]` — one row per harness with its
  `sessions`, `firings` and `chains`, so a harness that contributed nothing reads
  differently from one that was never mined.
- `corpora[]` — one row per corpus with `present`, `size` and `path`.
- `hooks[]` — the registered managed hooks in the active scope: `name`, `scope`,
  `source`, `rule`.
- `presets[]` — the catalog roster: `name`, `bundle`, `description`, `enabled`,
  `drifted`.
- `rules[]` — the instruction-tier rules in both stores: `id`, `scope`, `text`.
  Whether an agent's instructions carry one is `posture.rules[].missing`.
- `addenda[]` — the review addendum entries in both stores: `id`, `scope`,
  `text` (the store on disk), `head_text` (the committed text the commit gate
  reads for a project entry) and `state`: `live`, `uncommitted` (on disk, not at
  HEAD), `uncommitted-change` (the gate reads `head_text`), `uncommitted-removal`
  (removed on disk, still read at HEAD) or `unreadable` with `detail`. A global
  entry is read from disk, so it is `live` whenever the store holds it.
- `store_problems[]` — the guard stores a reader refused, each `scope` with the
  `error`. A row says `hooks`, `presets` and `rules` are INCOMPLETE for that
  scope, not empty: the guards the store registered still run. A manifest naming
  a file outside its store is refused this way, and an operator repairs it by
  editing the named entry.
- `team` — the repo-committed team lockfile summary: `present`, `path`,
  `hooks_locked`, `catalog_version`, and `error`. v10 adds `error`: a project with
  no lockfile is `present: false` and is not a fault, while a lockfile that exists
  and cannot be read is `present: true` with `error` naming why. Before v10 both
  read as absent.

### 2b. Commit-review findings

If this machine has a commit-review ledger, dump the findings. If not, say "no commit-review corpus on this machine" in one line and skip to Step 3.

```bash
steer commit-gate mine findings --kind accepted --undecided > "${TMPDIR:-/tmp}/gate-findings.txt"
steer commit-gate mine findings --kind rejected > "${TMPDIR:-/tmp}/gate-rejected.txt"
```

Read each file **once**. Do not cat them and do not re-read.

The queue's review candidate names how many chains carry an accepted finding that no class regex — active or rejected — yet covers (`undecided_chains`). A class counts as covering one only when it is live (active or rejected, never removed or reworked), mined from this corpus, and authored against that finding's verdict — a false-positive class decides rejected findings and leaves an accepted defect undecided. A transcript-friction class names a pattern in a session transcript, so its regex meeting a finding's text here is a coincidence. A record stored before the corpus tags existed reads as review-findings. That is the pool the classes below come from; the review side has no per-class threshold the tier can compute, because a new class has no regex until you author one.

`--undecided` hands you the pool, not the corpus: it drops the chains whose every accepted finding a live class already covers, so what you cluster over is what is left to decide. A chain that carries one covered finding and one residual finding stays, with the covered one shown in place and marked `[covered-by=<class-id>]`. Quote the finding text without that marker — it is generated classification metadata, not part of the reviewer's words. The header names how many findings are covered, how many remain, and over how many chains, which is the arithmetic that tells you whether your classes are finished.

Each chain header names the repository its rows recorded (`repo=`), so the project-or-global scope of a proposal is arithmetic over the chains in the class, not recall. Two checkouts sharing a directory name are both named by their whole path. A chain whose rows carried no repository metadata reads `repo=unknown`: it is a missing fact, so leave it out of the count rather than assigning it to the repository you are standing in. Counting where a class occurred tells you where it has been seen, never where it would occur — a class seen in one repository can still be a global rule.

Output is grouped by chain — one chain per commit attempt, findings deduplicated within it. The header line gives the denominator (`N chains reviewed`), which is what every rate on the review side is over. `--undecided` does not move that denominator: it narrows what you are shown, never what a rate is over, so an incidence printed under it means the same thing it means without it.

Above that header the dump lists every class already decided on this corpus, each with its incidence **now** and, for a rejection, its reason and reopen condition. Read that block before you cluster. It is the one place a past refusal reaches you at the moment you would otherwise re-derive it.

Pass `--since 60` to limit the window, `--strict-joins` to restrict to rows whose ledger join is reliable, and drop `--undecided` to see the covered chains too. Default is the full corpus, which is right for a first pass.

## Step 3: Analyze and cluster — this is your job, not the script's

Both corpora are mechanical evidence. All semantic judgment — what is hookable, how to phrase it — happens now, in this session. Tag every candidate with the corpus it came from.

### Transcript candidates — what some tier can fix

A transcript candidate qualifies when some tier in the routing guide (Step 4) can address it.

- A concrete pattern in an edit or a command: a pre-tool guard. "don't use console.log", "always python3 not python", "never commit .env files", "no rm -rf".
- A property of the finished tree: a repository check. "tests must pass before the turn ends", "no literal test counts in scripts".
- A judgment or workflow complaint with no mechanical test: an instruction rule, and only when it recurs across sessions and no instruction already says it. "ask before refactoring", "be more concise".

Discard the rest silently — do not pad the report.

Then:

1. **CLAUDE.md gap analysis first.** Directives are the highest-precision source: the user already wrote the rule down; it just isn't enforced. Check each directive a check or guard could detect against `guard_rules`, `hook_blocks` and the `checks` reading — skip anything a guard or check already covers. A directive no check or guard can detect is already in its strongest place; skip it. A directive that matches only an instruction rule (in `steer_rules`, not in `guard_rules`) is not enforced: propose a guard for it only with a reason why the instruction rule's `--not-mechanizable` argument no longer holds, and name the instruction rule the guard would replace.
2. **Corrections and failures second.** Weight by `sessions` over `count` (friction across sessions beats one bad session). Read the `examples` — the pattern key is normalized and lossy; the examples carry the user's actual words. Drop a cluster that a `steer_rules` line (guards and instruction rules both write one) or a check in the `checks` reading already answers: a fix `/steer` installed has no record in `interventions[]`, and the friction from before its install stays in the window. Where it keeps recurring after the install, propose registering the installed fix with `steer fix register` and its `--hook-id`, `--rule-id` or `--check-id`, not a second fix.
3. **Deduplicate across sources.** A CLAUDE.md directive corroborated by correction clusters is one candidate with stronger evidence, not two.
4. **Phrase each candidate as a rule.** A guard candidate uses `detect | suggest` syntax, e.g. `never use python bare | use python3 instead`. Reuse the user's own wording where possible.

### Review candidates — class qualification

The script does no semantic grouping by design. Read the findings and name the classes yourself. A review class qualifies only when all of these hold:

- It appears on **2 or more distinct chains**, and you state the count in the proposal. Two occurrences justify showing the class to the reader; the reader decides whether to install it. Say where you drew the class boundary, because the count follows from it: one class split in half can read as 2 and 3 where the merged class reads as 5.
- It spans **more than one commit and more than one file**. A defect that only ever hit one file is that file's bug, not a class.
- It names a **concrete, detectable tell** — a pattern in the source or the commit message, not a quality ("insufficient error handling").

Do the same pass over the rejected dump. A recurring **false-positive** class is equally actionable — it goes to the review addendum as a suppression, and it answers whether the rejected fraction is a prompt problem or reviewer noise.

Everything else is the irreducible cost of an independent reader. One-off findings are the gate working as designed; do not manufacture classes to fill a report.

Cap the whole report at the **top 5–7 candidates across both corpora**. Finding fewer — or none — is a valid result; say so plainly rather than padding with weak candidates.

## Step 4: Route each candidate to the strongest tier that can catch it

The routing guide is shared with the `steer` skill. Read it from this skill's
directory with symlinks resolved, because an installed skill directory can be a
link:

```bash
cat "$(cd "<this skill's base directory>" && pwd -P)/../steer/prompts/routing.md"
```

Route each candidate with it: name the enforcement point, then take the strongest
tier that meets it. An instruction-rule candidate also needs the recurrence Step 3
asks for.

The `--tier` flag takes either vocabulary and refuses anything else. Say the type,
not the flag value.

| `--tier` | Say |
|---|---|
| `check` | repository check |
| `steer` / `guard` | pre-tool guard |
| `prose` / `instruction` | instruction rule |
| `prompt` / `review` | review prompt |

Scope a guard project vs global — a rule seen across projects or from global CLAUDE.md suggests global scope. If nothing routes, say so. "No candidate qualified this pass" is a correct result.

A detector the guide keeps out of `check.sh` may still be suggested as a manual or CI check. Say plainly that Steer observes no execution of it and cannot bind a check-tier record to it, so its effect stays unmonitored here.

## Step 5: Present, then act

### How a proposal reads

Write each proposal as connected prose that answers five questions in order. A reader
who has never met Steer decides from it without a glossary.

1. **What problem keeps happening?** Show one representative example, in the user's own
   words where the evidence carries them.
2. **What supports that?** Name the source — session history or review history — the
   amount of relevant work it was counted over, and the limit on it.
3. **What change is proposed?** Say the behavior it affects, then name the scope as
   **project** or **global** in those words: project reaches this repository alone,
   global reaches every repository on this machine. Choose it from the evidence —
   chains from one repository scope to project, chains from several scope global —
   and give that reason in a clause. Then say which coding agents and which files.
4. **What is the tradeoff?** Name one concrete legitimate case the proposal could
   obstruct, and say what in the proposed detector or wording would catch it. Each
   tier has its own form: a **repository check** rejects valid repository content; a
   **guard** blocks an acceptable tool action; an **instruction rule** discourages a
   valid approach; a **review prompt entry** makes the reviewer flag correct code or
   a deliberate design choice. Then say how the proposal preserves that case, or what
   restriction stays. Keep the answer qualitative unless the evidence carries the rate
   you claim, which needs a denominator: "no corpus records this" states a limit on
   the measurement and is not an answer. If no concrete case comes up, give the
   boundary you examined and the uncertainty that remains; never invent a harm, and
   never call a proposal cost-free.
5. **What decision is needed?** State the action and its consequence in one question.

A worked proposal:

> The agent used `python` where this project requires `python3` in three recorded
> sessions. I suggest a guard that blocks the bare command and tells the agent to use
> `python3`. It is a project fix: every recorded session was in this repository, so it
> applies here alone, to Claude Code and Codex. It could also block a deliberate use of
> `python`, because it matches the bare command anywhere in a shell call. Apply this guard and track whether the problem becomes less frequent?

Carry the same shape into the audit half. A result reads as its counts and dates,
then the limit on them, then whether anything is asked of the reader.

Two rules from Step 1 apply again here, at the moment you write the sentence.
State no direction and no percentage: "fewer since the fix", "the fix worked" and
"a 60% reduction" are causal claims these counts cannot carry, and they are the
natural sentence to reach for in a summary. And never hand the reader a reading's
name — "the window is quiet", "the accounting is incomplete" — in place of the
facts that produced it.

> The problem appeared in three of ten sessions before this guard was installed and
> in one of four since, the most recent on 14 September. Four sessions is too few to
> read a trend into, and the same rule also sits in CLAUDE.md, so these counts cannot
> separate the guard from the instruction. The guard stays installed; no action is
> needed now.

Name the tier by its type, never by its flag value: a **repository check**, a **pre-tool
guard**, an **instruction rule** or a **review prompt**.

Say plainly which action does what. **Applying a fix** makes a change. **Stopping the
tracking** of a fix ends its measurement and leaves the change in place — it removes
nothing. **Removing a guard** takes the guard out and is a separate operation from
either. A reader who conflates them removes a rule they meant to keep.

Then ask which candidates to act on (multi-select). Create nothing unasked.

### Every record explains itself

`--problem` and `--intervention` are required on `register` and on `reject`, and the
CLI refuses a blank or a value that only repeats the class name. Author both **now**,
while the evidence is on screen. You are the last reader who has it: months later the
record is read from a status page by someone with the class name, the regex and
nothing else, and neither of those says what the class is.

- **`--problem`** — what goes wrong, in one sentence, in the vocabulary of the defect.
  Write it for a reader who was not in this session. "The reviewer raises a concern an
  earlier round of the same review already settled", not "within-chain re-raised finding".
- **`--intervention`** — what this record does about it, as concrete behavior. "check.sh
  refuses a printed literal count", not "added a check". On a `reject`, describe the
  intervention you considered; if you found none worth installing, say exactly
  `no suitable intervention was identified`. `--reason` still explains the decision;
  these two explain the class.
- **`--note`** stays optional and is for implementation detail that fits neither.

To correct a description on an existing record, use `steer fix describe ID
--problem … --intervention …`. It writes the prose and nothing else — the id, the
match, the scope, the baseline, the instrument and both windows are untouched, so a
clearer sentence never restarts the measurement. Omit a flag to keep that half.

### Transcript candidates — install through the tier's own workflow

Install each accepted transcript candidate through its tier's section in the routing guide. A pre-tool guard goes through the steer skill's generation protocol with the phrased rule — specialist prompt selection, hook generation, registration via `steer register`, and mandatory self-testing all apply unchanged. Do not duplicate or shortcut that protocol here.

A candidate routed to a repository check registers with `--tier check --check-id` under the same binding and `--scope-repo-root` rules as a review candidate.

Then register the class over the transcript corpus, so the fix you just installed is measured rather than assumed:

```bash
steer fix match 'YOUR REGEX' --corpus transcript-friction --signal-kind user_corrections

steer fix register \
  --class "bare python call" \
  --match 'YOUR REGEX' \
  --corpus transcript-friction --signal-kind user_corrections \
  --tier steer --target hooks/no-bare-python.py --hook-id no-bare-python \
  --scope-harness claude-code,codex \
  --problem "the session runs bare python and the interpreter is missing" \
  --intervention "a pre-tool guard refuses a bare python invocation" \
  --note "why this tell was chosen"
```

The unit here is the **root session**, not the chain: a class either occurred in a session or it did not, and a subagent thread is provenance on its root. `--signal-kind` names the classifier kind the regex is counted over — `any`, `user_corrections`, `user_rejections`, `tool_failures` or `hook_blocks` — and is frozen with the rest of the record. `--scope-path` is refused here, because a session carries no changed-path list; scope by harness or repo instead. The corpus lives in a snapshot store that `steer report refresh` writes, so a frozen baseline outlives the transcript the harness later rotates away.

Pass `--tier steer --hook-id` for a generated guard. That is what gives the record an audit-log join, so `observation.firing` reports what the guard actually blocked over the window. A quiet window beside `firing.rows: 0` is silence the guard did not produce, and a recurrence beside `firing.rows: 0` points at the matcher. Neither reading is available from the incidence alone.

### Review candidates — author the regex, install, register

For each accepted review candidate:

1. **Author the regex and test it before writing any fix:**

   ```bash
   steer fix match 'YOUR REGEX' --kind accepted
   ```

   `match` also reports every recorded class that covers chains your candidate matches. A high share against a rejected class means you are re-proposing it: read the reason before going further.

   Add `--show-residue` to see what the regex leaves behind: the findings of that kind it does not match, and the terms recurring among them, each with its count in the residue and its count across the whole corpus. Where the two counts are equal the term is the corpus's own vocabulary and names nothing; where the residue count is well under the corpus count, the regex already reaches most of that term's chains. A residue term with a high count and few matches is the class you did not name — either widen the regex or author a second class for it. The residue is per finding, so a chain your regex matches still contributes the findings inside it that it misses. What it lists is unmatched by **this** regex, which is not the same as unclassified: another registered class may already cover it, and `mine findings --undecided` is what answers that.

   Iterate until it matches the class members and nothing else. This regex is the class's permanent definition — `score` re-counts with it months from now, so an over-broad regex will report the class as recurring forever, and a commit-specific one will report a quiet window it did not earn. Prefer the defect's vocabulary over any single commit's identifiers.

2. **Install the fix** through its tier's workflow, and verify it fires on the real case:

   Follow the tier's section in the routing guide. For a repository check, the defect case reproduces the real finding. Step 3 accepts a check-tier record only once the check is validated, the repository adopted, and its Stop runner registered; `steer check list` shows all three.

3. **Register it**, after the fix is in:

   ```bash
   steer fix register \
     --class "stale test-count claim" \
     --match 'YOUR REGEX' \
     --kind defect \
     --tier check --target check.sh \
     --check-id no-literal-count \
     --scope-repo-root "$(git rev-parse --show-toplevel)" \
     --scope-harness claude-code --scope-path '\.go$' \
     --problem "a check prints a literal test count and the number goes stale" \
     --intervention "check.sh refuses a printed literal count" \
     --note "why this tell was chosen"
   ```

   `register` computes the pre-install baseline and refuses a regex with no baseline, since an unmatched class can never be scored. Use `--kind false-positive` for reviewer suppressions; it scores against rejected findings instead of accepted ones.

   **A scope is required.** It names the opportunity window the class is measured over, and it is frozen at register time. Pass `--scope-all` for every chain, or any of `--scope-harness IDS` (exact harness ids — `claude-code`, `codex` — repeatable or comma-separated), `--scope-repo REGEX` (matches the repository root basename), `--scope-repo-root PATH` (the one exact repository root, which two checkouts sharing a basename do not share) and `--scope-path REGEX` (matches a repo-relative changed path); the predicates combine with AND. **A `--tier steer` record must name its harnesses with `--scope-harness`, and each one must already carry the guard** — `register` refuses the record otherwise, because a scope that admits an unguarded harness pools guarded and unguarded work and can never come back true. A Codex runner with no approval record in Codex's `config.toml` counts as unguarded: Codex runs no hook the user has not approved in `/hooks`, so its sessions would read the guard's rule as text with no guard behind it. Install the guard in every harness (`steer register` now does this by default), then register one record per harness: Codex and Claude Code run different models, so their chains carry different base rates and a single pooled rate measures the mix, not the fix. A `--tier check` record answers the same rule: it names the harnesses whose Stop runner is registered, and `--scope-repo-root` rather than `--scope-all`. `prose` and `prompt` tiers are harness-independent and keep `--scope-all`. Scope repo and path metadata only — never the finding text. Selecting chains on the failure content is the bias the scope exists to remove, so a `check`-tier fix in Go code scopes to `--scope-path '\.go$'`, not to the finding's words. The baseline `register` reports is restricted to the scope and to chains that carry an adjudication; widen the scope if it reads as zero, and check `steer commit-gate backfill --apply` first, because unresolved rows shrink the baseline the write can see.

   **`--tier check` additionally requires `--check-id`** — the id in the repository's `.steer/checks/manifest.json`. `register` freezes that check's binding kind, resolved repository root, detector digest and current validation receipt onto the record, and refuses a check that is undeclared, has no current receipt (`steer check test <id>`), whose repository is not adopted on this machine (`steer check adopt`), or whose Stop runner is registered in no harness (`steer check install --harness <id>`). Without that binding the record can only be read beside an aggregate `check.sh` outcome, which attributes no result to its assertion. Records written before the binding existed stay readable as unbound, with execution attribution unavailable.

   **`--tier steer` additionally requires `--hook-id`** — the steer-manifest key, which is also the `hook_name` in `hooks/steer-audit.jsonl`. That id makes the audit-log join deterministic. Reconstructing it later from `target` means searching the manifest for a path, and the manifest stops describing a hook the moment this loop renames or retires it. `register` also copies the rule text and `rule_hash` from the manifest, so the join survives a rename, plus `hook_version` and `hook_digest` — the identity of the implementation that ran. The rule hash names the policy text; a guard repaired without a rule change moves the digest alone, which is what says the treatment changed under a class still collecting. The lookup reads whichever store holds the id and refuses an id held by both; `--hook-scope project|global` names one.

   **`--tier prose` additionally requires `--rule-id` and `--not-mechanizable`.** The prose has to exist before a record can name it: write it with `steer rules add <id> --rule "<text>"`, which prints the scope's rules first and refuses text a second id already holds, then pass that id. `register` copies the rule text and its `rule_hash` onto the record, and refuses an id the store does not hold. `--not-mechanizable` carries the argument the routing ladder asks for — one sentence naming why a repository check and a pre-tool guard cannot detect this class. It is refused blank and refused when it only restates the class name. Today that argument is made in a session and lost there, so no later reader can check it was made at all; the flag preserves its presence, and its soundness stays the reader's.

   **`--tier prompt` additionally requires `--addendum-id` and `--kind false-positive`.** The stock review prompt is compiled into the binary and fixed; a fix on this tier is an entry the commit gate adds to it. Write it with `steer commit-gate addendum add <id> --text "<text>"`, then pass that id. `register` copies the entry text and its `addendum_hash` onto the record, refuses an id the store does not hold, and refuses an id held by both stores unless `--addendum-scope project|global` names one. A defect class is refused, because an entry that stops the reviewer raising a real defect would read as a falling count. A project entry reaches the reviewer only once the store's `steer-manifest.json` is committed: the gate reads project entries from HEAD, so `register` refuses a project entry that HEAD does not hold with the same text. Commit the manifest first; `steer commit-gate addendum list` names each entry's state at HEAD.

   The audit log is a second, independent measurement: it says whether the hook ever *fired*. A steer fix with falling incidence and zero fires did not cause the fall, and incidence alone cannot tell you that.

   `register` refuses a candidate that shares half or more of its chains with a rejected class. Two escapes, and they mean different things: `--supersedes <id>` says the refusal is overturned and marks that record `reopened`, while `--force` says the two classes only look alike. Prefer the first.

For each review candidate you examine and refuse, record the refusal:

```bash
steer fix reject \
  --class "within-chain repeat finding" \
  --match 'YOUR REGEX' \
  --scope-all \
  --problem "the reviewer raises a concern an earlier round of the same review already settled" \
  --intervention "no suitable intervention was identified" \
  --reason "why no gate was installed" \
  --reopen "the observation that would change this answer"
```

Author the regex the same way as for `register`, and test it with `match` first — a rejection whose regex matches nothing cannot recognise the class when it returns, so `reject` refuses one, exactly as `register` does. A rejection also takes a frozen scope, on the same `--scope-all` / `--scope-harness` / `--scope-repo` / `--scope-path` rule as `register`: it is the window the class's growth is watched over if the refusal is ever revisited.

A legacy record written before scopes existed carries no scope. `steer fix scope <id> --scope-…` installs one: it sets the scope once, recomputes the baseline over the eligible chains, and opens a fresh after-window at the migration moment so inspecting the record's own history cannot pick a scope that flatters it. An installed scope is frozen — correct one by retiring the record and registering afresh.

`--reason` and `--reopen` are both required, and the second is the load-bearing one. "It is not mechanizable" is a verdict on today's instrument, not a permanent property of the class. Name what would have to change: a signal that starts being persisted, a rate that triples, a tier that becomes cheap. The durable version of that reasoning belongs in a durable design note; the record here is what fires at re-proposal time.

**If your reason cites a count for a subset of the class, store that subset's regex.** Pass `--reopen-match '<regex>' --reopen-at <n>` together; either alone is refused. Without them the record keeps only the broad class regex, and a reason reading "only the numeric sub-case is detectable, and it is one chain" names a threshold no later pass can evaluate — the subset has no stored pattern to re-count. With them, the findings dump scores the subset on every run and prints `REOPEN CONDITION MET` when it reaches the threshold, so a refusal that time has overturned announces itself instead of waiting to be re-derived by hand.

Record a refusal only for a candidate you actually examined against the corpus. Every one-off finding is not a rejected class, and a log padded with them buries the four or five refusals that matter.

Register only after the fix exists. The install timestamp is the split point for every future measurement, so registering early credits the fix with commits it was never present for.

### Clear the queue

After you register, reject, rework, or remove anything, run the refresh once more:

```bash
steer report refresh --days 30
```

This rewrites the queue and its timestamp from the new state, so the SessionStart nudge stops pointing at work you just finished. Skip it only when you changed nothing this pass.

## Reading the numbers honestly

- **Keep the three kinds of evidence apart.** Fixtures establish expected outcomes on stored cases under the recorded identities; an audited firing supports a judgment about one attempted action; field counts describe matched recurrence over eligible work. None of them establishes efficacy.
- **The comparison is uncontrolled, so no count here credits a fix.** The standing instruction set — CLAUDE.md, AGENTS.md, imported globals, skills, hooks — can affect the agent's behaviour, no corpus records it, and a person edits it by hand between sessions. Report what recurred and what did not.
- **The adjudication is self-reported.** The review outcomes come from the agent that received the findings, so a class the agent systematically misjudges is invisible here. Treat incidence as a strong hint, never as ground truth.
- **The instrument moves under the measurement.** Every ledger row is stamped with the reviewer that produced it (`model/runs/prompt-digest`), and `observation.provenance` lists every identity that counted each window. When the two lists differ, two instruments counted these windows — re-read the findings before weighing the counts against each other. A regex leaning on the reviewer's framing verbs ("hardcodes", "still advertises") is far more exposed than one leaning on code vocabulary, which is a reason to prefer the latter when authoring.
  - The stamp cannot catch vendor-side drift. An external model alias whose behaviour changes with no local diff makes an unchanged instrument string weaker evidence than a changed one.
- **Workload moves the split.** A `check`-tier fix scoped to Python cannot recur in a month of shell commits, and that reads as silence. Read `observation.counts` for each window's date bounds and eligible size before reading anything into a quiet after-window.
- **A fresh record has an empty after-window.** The gate reviews on the order of a few commits a week, so `zero_eligible_after` is the normal state of a new record. Re-read on a cadence rather than the day after installing, and never read a fresh quiet window as success.
- **Never inject this history into the reviewer or the authoring session.** A reviewer handed past findings re-finds them instead of searching, and a list of old defects is a gauge rather than a directive. The output of this loop is a gate, not a reminder.
