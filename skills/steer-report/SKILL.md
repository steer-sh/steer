---
name: steer-report
description: Audits the interventions steer already installed, then mines Claude Code transcripts and the commit-review ledger for new hook candidates backed by evidence. Use for steer onboarding ("what hooks should I create?"), periodic review of installed hooks, or when the user asks to mine usage history for candidates.
user-invocable: true
digest_version: 1
compatibility: Requires the steer CLI (report and commit-gate subcommands) on PATH or bundled under scripts/. Reads transcripts from ~/.claude/projects and, when present, the commit-review ledger under ~/.claude/state/commit-review.
allowed-tools: Bash(steer *), Read, Edit, Write
---

# Steer Report — audit installed hooks, then mine for new ones

This skill closes the friction loop. It decides two things, in this order:

1. **Did the interventions already installed work?** Ineffective ones get reworked or removed.
2. **Which new patterns recur often enough to deserve a hook?** Both corpora feed this — transcript friction and commit-review findings.

The audit comes first on purpose. Proposing new hooks is the interesting half, and skipping the audit is how a log of interventions turns into a graveyard of unmeasured good intentions.

Two corpora feed this skill, and either may be empty on a given machine:

- **Transcript friction** — repeated corrections, rejected tool calls, recurring lint/test failures, and CLAUDE.md rules not yet enforced. Mined by `steer report`.
- **Commit-review findings** — defects the commit-review gate found and adjudicated. Mined by `steer commit-gate mine`. Absent when there is no commit-review ledger on this machine.

An empty corpus produces one line, never an error. An adopter with no commit-review ledger still gets the transcript half — the cold-start front door.

## Example Invocations

```
/steer-report
/steer-report --days 7
/steer-report --project-only
/steer-report --headless
```

## Step 0: Fresh-context gate (interactive mode only)

The analysis reads a sizable digest and a finding dump, then reasons over both. If this conversation already contains substantial prior work — more than a few turns, or a large amount of loaded code and discussion — warn before proceeding:

> "A usage report works best in a fresh session: the analysis gets the full context window, and cost is lower because each step doesn't re-pay for this conversation's history. I recommend running `/steer-report` as the first message of a new session. Proceed here anyway?"

Respect the user's choice. If the conversation is fresh, skip the warning and proceed. In `--headless` mode, skip this gate entirely and never ask questions.

## Step 1: Snapshot the loop, refresh the queue, then audit

First read the whole loop's posture and full class inventory in one snapshot:

```bash
STEER_BIN="${CLAUDE_PLUGIN_ROOT:+${CLAUDE_PLUGIN_ROOT}/scripts/steer}"
STEER_BIN="${STEER_BIN:-steer}"
"${STEER_BIN}" status --json > "${TMPDIR:-/tmp}/steer-status.json"
```

Read `${TMPDIR:-/tmp}/steer-status.json` **once**. The `interventions[]` array is
the **full** scored inventory — every live class across every tier, `effective`
ones included. This is your dedup source: when you cluster new candidates in
Step 3, drop any class already here scored `effective` or `ineffective`. An
`ineffective` class is a rework, not a new hook. The schema is documented in "The
`steer status --json` contract" below. Fail soft — if the command errors, say so
in one line and continue; the audit below still runs.

A deterministic tier runs this loop's mechanical half — scoring, corpus sweeps, threshold checks — with no model. It may have run on a schedule already; run it again now, synchronously, so you never read a stale queue. It takes seconds and spends no tokens.

```bash
steer report refresh --days 30
```

Then Read the queue **once**: `~/.claude/state/steer-report/queue.json`. It holds:

- `interventions[]` — full contract records the tier already scored and flagged as **needing attention** (verdict `ineffective` or `class-grew`). These carry a `match`; they are the audit's action list, pre-filtered.
- `unscored[]` — intervention ids the local engine cannot score (session-unit hooks needing the audit-log join, unbuilt). Report their existence; do not read a verdict into them.
- `candidates[]` — pointers to new clusters. Transcript rows carry `pattern`/`count`/`sessions`; the review row carries `undecided_chains` and a `dump` command. These carry **no** `match` — authoring the regex is your job, below.
- `corpora[]` — one row per corpus (`review-findings`, `transcript-friction`, `auto-denials`) with `present` and `size`. Relay this sweep to the user in one sentence.

The queue tells you what is pending. The full dumps below are the raw material you cluster over. Score the commit-review interventions for the complete audit — the queue lists only the ones needing action, not the `effective`/`promising`/`partial` ones worth showing too. If this machine has no commit-review ledger, `mine score` reports an empty corpus — say "no interventions to audit on this machine" in one line and go to Step 2.

```bash
steer commit-gate mine score
```

Each record reports before/after **chain incidence** — the fraction of reviewed commits on which the class appeared — split at the install timestamp, plus a one-sided Fisher p-value. Verdicts:

- `effective` — incidence at least halved and the drop clears p ≤ 0.10. Leave it alone.
- `promising` — incidence at least halved, too little data to clear alpha. Leave it, re-score later.
- `partial` — a real but under-half drop. Judgement call: tighten the gate or accept it.
- `ineffective` — incidence flat or higher. **Act on this.** Either the gate does not catch what the class actually is, or the class was never mechanizable.
- `insufficient-data` — fewer than 12 post-install chains. Say so and move on; do not read a verdict into it.
- `no-baseline` — the regex matches nothing before install. The class definition is wrong.

Rejected classes are scored too, and carry their own labels — nothing was installed, so effectiveness is not the question. `class-grew` is the one to act on: incidence rose by half again since the refusal, which is usually the reopen condition arriving. `class-faded` and `class-steady` confirm the refusal still stands. Read the record's `reopen if:` line before reopening anything; the condition was written by the pass that had the evidence in front of it.

For every `ineffective` record, open the target file and decide between:

- **Rework** — the fix was aimed at the wrong tell. Fix it, then `register --supersedes <id>` so the old record is retired with a forward pointer and the new one gets a fresh baseline.
- **Remove** — the class is not mechanizable after all, or the defect stopped mattering. Delete the check and `retire <id> --reason "..."`.

A gate that never fires is not free. It costs oracle runtime on every commit and, worse, it reads as coverage that does not exist.

## Step 2: Gather new evidence from both corpora

### 2a. Transcript friction

Use `steer` from PATH if available, otherwise fall back to `${CLAUDE_PLUGIN_ROOT}/scripts/steer`. Redirect the digest to a temp file so it enters context exactly once — via Read, not via command output. Run a **single command**:

```bash
STEER_BIN="${CLAUDE_PLUGIN_ROOT:+${CLAUDE_PLUGIN_ROOT}/scripts/steer}"
STEER_BIN="${STEER_BIN:-steer}"
"${STEER_BIN}" report --days 30 > "${TMPDIR:-/tmp}/steer-digest.json"
```

Then Read `${TMPDIR:-/tmp}/steer-digest.json` **once**. Do not cat the file and do not re-read it later — everything needed for analysis is in that single read.

All projects are scanned by default (cross-project habits are the strongest candidates). Pass through any flags the user supplied (`--days N`, `--project-only`, `--max-digest-bytes N`). Strip `--headless` — it is a skill flag, not a CLI flag.

The queue's transcript candidates already list the correction clusters that cleared the deterministic threshold (3+ sessions). Treat that count as an upper bound: it is the mechanical grouping, not the final class. Read the full digest to refine, split, or discard those clusters and to catch anything the threshold missed.

Digest schema (`digest_version: 1`):

- `stats` — coverage: `sessions_scanned`, `bytes_scanned`, `clusters_found`, `clusters_emitted`, `truncated`. **Always relay coverage to the user in one sentence.** If `truncated` is true, say so and suggest narrowing (`--days 7`) or raising `--max-digest-bytes`.
- `claude_md[]` — per CLAUDE.md file: `steer_rules` (already enforced by hooks) and `directives` (standing instructions with their section heading, NOT yet enforced).
- `signals.user_corrections[]` — user turns matching correction language. Each cluster: `pattern`, `count`, `sessions` (distinct sessions), `first_seen`, `last_seen`, `examples` (up to 3 raw snippets).
- `signals.user_rejections[]` — tool calls the user declined at the permission prompt (pattern is `Tool: target`).
- `signals.tool_failures[]` — recurring lint/test/compile failures from Bash output.
- `signals.hook_blocks[]` — operations already blocked by existing hooks. These are enforcement *working*; use them only to avoid suggesting duplicates.

The digest is size-capped: the CLI evicts the lowest-scoring clusters to fit, and reports honesty in `stats`.

### The `steer status --json` contract (`status_version: 2`)

Step 1 reads this snapshot for posture and the full class inventory. The unit is
the failure class and its verdict, not the hook count. This schema is byte-shared
with the root `SKILL.md`; change both together (see CLAUDE.md Key Contracts).
Fields:

- `status_version` — the schema version. `--json` is a stable agent contract.
- `posture` — the arming state: `instrument` (reviewer model / runs / prompt
  digest), `reviewer`, `ledger` path, and `commit_gate[]` / `denials[]`. Each is
  a scope row with `registered` and `effective`.
- `interventions[]` — the **full** scored inventory. It carries every live
  class, `effective` ones included. Each row: `id`, `class`, `match` (the regex
  that defines the class), `tier`, `target`, `status` (`active` or `rejected`),
  `verdict`, `before` / `after` incidence (`hits`, `units`, `rate`), `p_value`,
  `instrument_drift` (a measurement window spanning two reviewer prompts; the
  verdict stands, the flag rides beside it), `firings_applicable` (true only for the `guard` tier, whose master hook writes the audit log that `audit_firings` counts; check, instruction and review write none), and for
  a refused class `reason`, `reopen` and `rejected_ts`. This is the dedup source
  — a class here is decided.
- `summary` — `total` plus `counts` by verdict.
- `chains[]` — recent review chains, findings split `accepted` / `rejected`.
- `attention` — the subset needing action: `interventions[]` (verdict
  `ineffective` or `class-grew`), `candidates[]` (no `match` yet), and
  `unscored[]` (session-unit ids the engine cannot score).
- `telemetry` — corpus sizes: `review_chains`, `denials_rows`, `audit_firings`.
- `corpora[]` — one row per corpus with `present`, `size` and `path`.
- `hooks[]` — the registered managed hooks in the active scope: `name`, `scope`,
  `source`, `rule`.
- `presets[]` — the catalog roster: `name`, `bundle`, `description`, `enabled`,
  `drifted`.
- `team` — the repo-committed team lockfile summary: `present`, `path`,
  `hooks_locked`, `catalog_version`.

### 2b. Commit-review findings

If this machine has a commit-review ledger, dump the findings. If not, say "no commit-review corpus on this machine" in one line and skip to Step 3.

```bash
steer commit-gate mine findings --kind accepted > "${TMPDIR:-/tmp}/gate-findings.txt"
steer commit-gate mine findings --kind rejected > "${TMPDIR:-/tmp}/gate-rejected.txt"
```

Read each file **once**. Do not cat them and do not re-read.

The queue's review candidate names how many chains carry an accepted finding that no class regex — active or rejected — yet covers (`undecided_chains`). That is the pool the classes below come from; the review side has no per-class threshold the tier can compute, because a new class has no regex until you author one.

Output is grouped by chain — one chain per commit attempt, findings deduplicated within it. The header line gives the denominator (`N chains reviewed`), which is what every rate on the review side is over.

Above that header the dump lists every class already decided on this corpus, each with its incidence **now** and, for a rejection, its reason and reopen condition. Read that block before you cluster. It is the one place a past refusal reaches you at the moment you would otherwise re-derive it.

Pass `--since 60` to limit the window, `--strict-joins` to restrict to rows whose ledger join is reliable. Default is the full corpus, which is right for a first pass.

## Step 3: Analyze and cluster — this is your job, not the script's

Both corpora are mechanical evidence. All semantic judgment — what is hookable, how to phrase it — happens now, in this session. Tag every candidate with the corpus it came from.

### Transcript candidates — the hookability filter

A steer hook is a PreToolUse Python script doing regex/AST detection on file edits or Bash commands. A transcript candidate is hookable only if a concrete textual pattern can detect the violation.

- Hookable: "don't use console.log", "always python3 not python", "never commit .env files", "no rm -rf".
- Not hookable: "be more concise", "ask before refactoring", tone/judgment/workflow complaints. Discard these silently — do not pad the report.

Then:

1. **CLAUDE.md gap analysis first.** Directives are the highest-precision source: the user already wrote the rule down; it just isn't enforced. Check each hookable directive against `steer_rules` and `hook_blocks` — skip anything already covered.
2. **Corrections and failures second.** Weight by `sessions` over `count` (friction across sessions beats one bad session). Read the `examples` — the pattern key is normalized and lossy; the examples carry the user's actual words.
3. **Deduplicate across sources.** A CLAUDE.md directive corroborated by correction clusters is one candidate with stronger evidence, not two.
4. **Phrase each candidate as a steer rule** in `detect | suggest` syntax, e.g. `never use python bare | use python3 instead`. Reuse the user's own wording where possible.

### Review candidates — class qualification

The script does no semantic grouping by design. Read the findings and name the classes yourself. A review class qualifies only when all of these hold:

- It appears on **3 or more distinct chains**. Two is a coincidence.
- It spans **more than one commit and more than one file**. A defect that only ever hit one file is that file's bug, not a class.
- It names a **concrete, detectable tell** — a pattern in the source or the commit message, not a quality ("insufficient error handling").

Do the same pass over the rejected dump. A recurring **false-positive** class is equally actionable — it goes to the reviewer prompt as a suppression, and it answers whether the rejected fraction is a prompt problem or reviewer noise.

Everything else is the irreducible cost of an independent reader. One-off findings are the gate working as designed; do not manufacture classes to fill a report.

Cap the whole report at the **top 5–7 candidates across both corpora**. Finding fewer — or none — is a valid result; say so plainly rather than padding with weak candidates.

## Step 4: Route each candidate to the cheapest tier that can catch it

Prefer the highest tier on this ladder that can actually detect the class. Output quality converges to the oracles you can reach, and a prose rule only shifts a probability while an exit code does not.

| Tier | Use when | Where it lands |
|---|---|---|
| `check` | A repo-local script can detect it deterministically | that repo's `check.sh`, via the `check-oracle` skill |
| `steer` | It is detectable at edit time by regex/AST on a file or command | a PreToolUse hook, via the `steer` skill |
| `prompt` | It is a recurring reviewer false positive | the reviewer prompt (`cli/internal/gate/prompts/review.md`) |
| `prose` | Genuinely not mechanizable but frequent enough to earn a line | CLAUDE.md |

`prose` is the weakest tier and the easiest to reach for. Use it only after arguing why the class cannot be detected, and expect `score` to come back `ineffective` more often than not — that outcome is information, not failure.

Transcript candidates almost always route to `steer` (edit-time regex) or `check` (a repo script). Scope each one project vs global — a rule seen across projects or from global CLAUDE.md suggests global scope. If nothing routes, say so. "No candidate qualified this pass" is a correct result.

## Step 5: Present, then act

Show each candidate with:

- The corpus tag and evidence in one plain line: `corrected 4 times across 6 sessions` (transcript) or `flagged on 5 of 40 reviewed commits` (review findings).
- The proposed rule or class name, the proposed tier and target, and one or two verbatim example quotes.
- For transcript candidates, the `detect | suggest` rule and the scope recommendation.

Then ask which candidates to act on (multi-select). Create nothing unasked.

### Transcript candidates — hand off to steer

For each accepted transcript candidate, follow the steer skill's generation protocol (the root `SKILL.md` of this plugin) with the phrased rule — specialist prompt selection, hook generation, registration via `steer register`, and mandatory self-testing all apply unchanged. Do not duplicate or shortcut that protocol here.

### Review candidates — author the regex, install, register

For each accepted review candidate:

1. **Author the regex and test it before writing any fix:**

   ```bash
   steer commit-gate mine match 'YOUR REGEX' --kind accepted
   ```

   `match` also reports every recorded class that covers chains your candidate matches. A high share against a rejected class means you are re-proposing it: read the reason before going further.

   Iterate until it matches the class members and nothing else. This regex is the class's permanent definition — `score` re-measures with it months from now, so an over-broad regex will report a fix as ineffective forever, and a commit-specific one will report success it did not earn. Prefer the defect's vocabulary over any single commit's identifiers.

2. **Install the fix** via the owning skill (`check-oracle` or `steer`), or by editing the reviewer prompt. Verify it fires on the real case.

3. **Register it**, after the fix is in:

   ```bash
   steer commit-gate mine register \
     --class "stale test-count claim" \
     --match 'YOUR REGEX' \
     --kind defect \
     --tier check --target check.sh \
     --scope-repo 'steer' --scope-path '\.go$' \
     --note "why this tell was chosen"
   ```

   `register` computes the pre-install baseline and refuses a regex with no baseline, since an unmatched class can never be scored. Use `--kind false-positive` for reviewer suppressions; it scores against rejected findings instead of accepted ones.

   **A scope is required.** It names the opportunity window the class is measured over, and it is frozen at register time. Pass `--scope-all` for every chain, or one or both of `--scope-repo REGEX` (matches the repository root basename) and `--scope-path REGEX` (matches a repo-relative changed path); two predicates combine with AND. Scope repo and path metadata only — never the finding text. Selecting chains on the failure content is the bias the scope exists to remove, so a `check`-tier fix in Go code scopes to `--scope-path '\.go$'`, not to the finding's words. The baseline `register` reports is already restricted to the scope; widen the scope if it reads as zero.

   **`--tier steer` additionally requires `--hook-id`** — the steer-manifest key, which is also the `hook_name` in `hooks/steer-audit.jsonl`. That id makes the audit-log join deterministic. Reconstructing it later from `target` means searching the manifest for a path, and the manifest stops describing a hook the moment this loop renames or retires it. `register` also copies the rule text and `rule_hash` from the manifest, so the join survives a rename.

   The audit log is a second, independent measurement: it says whether the hook ever *fired*. A steer intervention with falling incidence and zero fires did not cause the fall, and incidence alone cannot tell you that.

   `register` refuses a candidate that shares half or more of its chains with a rejected class. Two escapes, and they mean different things: `--supersedes <id>` says the refusal is overturned and marks that record `reopened`, while `--force` says the two classes only look alike. Prefer the first.

For each review candidate you examine and refuse, record the refusal:

```bash
steer commit-gate mine reject \
  --class "within-chain repeat finding" \
  --match 'YOUR REGEX' \
  --scope-all \
  --reason "why no gate was installed" \
  --reopen "the observation that would change this answer"
```

Author the regex the same way as for `register`, and test it with `match` first — a rejection whose regex matches nothing cannot recognise the class when it returns, so `reject` refuses one, exactly as `register` does. A rejection also takes a frozen scope, on the same `--scope-all` / `--scope-repo` / `--scope-path` rule as `register`: it is the window the class's growth is watched over if the refusal is ever revisited.

A legacy record written before scopes existed carries no scope. `steer commit-gate mine scope <id> --scope-…` installs one: it sets the scope once, recomputes the baseline over the eligible chains, and opens a fresh after-window at the migration moment so inspecting the record's own history cannot pick a scope that flatters it. An installed scope is frozen — correct one by retiring the record and registering afresh.

`--reason` and `--reopen` are both required, and the second is the load-bearing one. "It is not mechanizable" is a verdict on today's instrument, not a permanent property of the class. Name what would have to change: a signal that starts being persisted, a rate that triples, a tier that becomes cheap. The durable version of that reasoning belongs in a durable design note; the record here is what fires at re-proposal time.

Record a refusal only for a candidate you actually examined against the corpus. Every one-off finding is not a rejected class, and a log padded with them buries the four or five refusals that matter.

Register only after the fix exists. The install timestamp is the split point for every future measurement, so registering early credits the fix with commits it was never present for.

### Clear the queue

After you register, reject, rework, or remove anything, run the refresh once more:

```bash
steer report refresh --days 30
```

This rewrites the queue and its timestamp from the new state, so the SessionStart nudge stops pointing at work you just finished. Skip it only when you changed nothing this pass.

## Reading the numbers honestly

- **The adjudication is self-reported.** The review verdicts come from the agent that received the findings, so a class the agent systematically misjudges is invisible here. Treat incidence as a strong hint, never as ground truth.
- **The instrument moves under the measurement.** Every ledger row is stamped with the reviewer that produced it (`model/runs/prompt-digest`), and `score` reports `INSTRUMENT DRIFT` when the after-window was reviewed by something other than what the regex was authored against. On a drift flag, re-read the findings before believing the verdict — a regex leaning on the reviewer's framing verbs ("hardcodes", "still advertises") is far more exposed than one leaning on code vocabulary, which is a reason to prefer the latter when authoring.
  - The stamp cannot catch vendor-side drift. An external model alias whose behaviour changes with no local diff makes an unchanged instrument string weaker evidence than a changed one.
- **Workload confounds the split.** A `check`-tier fix scoped to Python cannot recur in a month of shell commits, and that reads as success. Check the date span and the kind of work in the after-window before believing a clean drop.
- **`insufficient-data` is the normal state of a fresh intervention.** The gate reviews on the order of a few commits a week. Re-score on a cadence rather than the day after installing.
- **Never inject this history into the reviewer or the authoring session.** A reviewer handed past findings re-finds them instead of searching, and a list of old defects is a gauge rather than a directive. The output of this loop is a gate, not a reminder.

## Headless mode

When the invocation contains `--headless`: skip the gate, ask nothing, install nothing, and emit only JSON as the final message — the scored interventions and the proposed candidates from both corpora:

```json
{
  "digest_version": 1,
  "stats": { "sessions_scanned": 0, "truncated": false },
  "interventions": [
    {
      "class": "stale test-count claim",
      "verdict": "partial",
      "before": "4/82",
      "after": "1/29"
    }
  ],
  "candidates": [
    {
      "corpus": "transcript-friction",
      "rule": "never use console.log | use logger instead",
      "scope": "project",
      "tier": "steer",
      "sources": ["claude_md", "user_corrections"],
      "evidence": "corrected in 4 sessions; directive in CLAUDE.md §Conventions"
    },
    {
      "corpus": "review-findings",
      "class": "unchecked error return",
      "tier": "check",
      "target": "check.sh",
      "evidence": "flagged on 5 of 40 reviewed commits"
    }
  ]
}
```

Report an empty corpus as an empty array, never an error. Headless mode reports candidates only — it never creates hooks.
