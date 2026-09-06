# Plan: familiar language for Steer's user experience

## Position

Steer explains its work in words users already know: review, problem, fix,
guard, and result. A user decides what to change without learning the
vocabulary of the measurement system.

The product description:

> Steer reviews your agent's code, spots recurring problems, helps you choose
> fixes, and tracks whether those problems happen less often.

**Fix** replaces **intervention** in every human-facing sentence. A fix names
an intended purpose; its result says whether it worked. "An untested fix" and
"this fix did not help" are ordinary English. The word carries no success claim.

This plan applies familiar-language, consistency, progressive-disclosure and
explicit-action principles. It does not claim that testing established these
labels as optimal. Comprehension testing is part of the rollout.

## What the code establishes

Every fact here was read from the repository at commit `baf008a`.

- **No label map exists.** Terminal (`cli/cmd/status.go:301-307`), HTML
  (`templates/dashboard.html.tmpl:230,336,369`) and TUI
  (`cli/internal/tui/classes.go:53-58`) print the raw verdict token. The label
  table in this plan is new code.
- **The action table is total, and an unknown token reads as `accept` with an
  empty sentence.** `ActionFor` (`action.go:92-101`) falls back to `ActionAccept`;
  `ActionSentence` (`action.go:108-113`) returns `""` for a missing key.
- **Verdict tokens are computed, never stored.** `interventions.jsonl` carries no
  verdict. Tokens appear in four read contracts: `steer status --json`
  (`StatusVersion = 9`), the cadence queue (`QueueVersion = 4`,
  `refresh.go:65`), `cadence.AttentionVerdicts` (`refresh.go:251-254`), and both
  SKILL.md files (about 70 lines across the two).
- **The code already treats `class-grew` and `class-faded` as directional
  claims.** `claimsAnEffect` (`action.go:78-80`) lists them, and the `class-grew`
  sentence (`action.go:70`) reads "the refused class grew". The range is withheld
  for them (`TestRangePrintsOnlyUnderAClaim`), the direction is not.
- **"Verdict" already has two meanings.** `deny.md:23` and `ledgertext.go` use it
  for the agent's ACCEPTED / REJECTED decision on one finding; the scorer uses it
  for the measurement result. This is a reason for the rename below.
- **`--tier` is documented, not validated.** `intervention.go:188` lists
  `check|steer|prose|prompt`; no code checks the value. `TierPhrase`
  (`format.go:232-251`) and `RemovalPhrase` (`action.go:245-264`) read both that
  vocabulary and `check|guard|instruction|review`.
- **The alias pattern exists.** `cli/cmd/dashboard.go:9-11` carries
  `Aliases: []string{"top"}`. `newInterventionCommands` (`intervention.go:118`)
  builds both the `intervention` tree and `commit-gate mine`.
- **Exposure is concentrated.** The word "intervention" appears about 90 times in
  `status/`, 23 in `tui/`, 28 in `cmd/`, 7 in the HTML template and 3 in the
  README. These are grep counts over source, so they include identifiers; treat
  them as upper bounds. The README carries none of `class`, `verdict`,
  `confound`, `corpus` or `chain`.

## Design rules

1. **Lead with the user's problem and choice.** Say what happened, what a change
   would do, and what decision is needed.
2. **Use familiar words even when they are less exact.** Restore precision where
   it changes the meaning or the consequence of a decision.
3. **Explain locally.** A screen or an agent message supplies the meaning needed
   to act. A glossary is supporting reference.
4. **Name the object of an action.** "Accept this finding", "apply this fix" and
   "stop tracking this fix" are different operations. A bare "accept" is not enough.
5. **Separate installation from results.** A guard can be installed while its
   benefit is unknown. A result can change as evidence arrives.
6. **Name the limits in everyday language.** Say why Steer cannot tell whether
   something helped, and whether the user can do anything about it.
7. **Keep technical detail reachable.** Evidence sources, eligibility rules,
   identifiers and exact commands go in details, for inspection and repair.
8. **A state is not an action.** A label names what the measurement found. The
   action sentence names what the reader does. Commit `c8af1af` drew this line
   and every label in this plan keeps it.
9. **Keep meanings consistent across surfaces.** CLI output, dashboard, HTML,
   README and skill speech describe the same objects and actions.

## Core concepts

| User-facing term | Meaning and use |
|---|---|
| Review | An independent examination of a proposed code change before it commits. |
| Finding | A concern a reviewer raised that still needs to be checked against the code. |
| Decision on a finding | The coding agent's accept or reject of one finding. Replaces "adjudication" and the per-finding sense of "verdict". |
| Recurring problem | A pattern of trouble found in review history or session history. Show concrete examples. |
| Suggested fix | A proposed change intended to reduce a recurring problem. Use only once an actual change is proposed. |
| Fix | A change intended to address a problem: a check, a guard, an instruction, or a review-prompt change. |
| Check | An executable test of repository content. |
| Guard | A check on an agent's action before it runs. It can block the action and say how to correct it. |
| Instruction | Written guidance for the coding agent. Name the file and the behavior. |
| Review change | A change to the review prompt aimed at a recurring reviewer mistake. Reviewer settings are a separate operation. |
| Rule | A statement of desired behavior. Say whether a guard enforces it or an instruction communicates it. |
| Result | What Steer can conclude now about the tracked problem after the fix. Replaces the measurement sense of "verdict". |

Introduce a concept when its task appears; a user studies none of them before a
first review.

Keep "commit gate" as the feature and command name. Introduce it as "review
before commit". At setup, name the roles: Steer runs an installed external
reviewer; the coding agent decides each finding against the code.

The tier values `check`, `steer`, `prose` and `prompt` stay as command values
and display as check, guard, instruction and review change. Changing reviewer
settings never registers a tracked fix, and copy must not imply it.

## Terminology

This table gives contextual wording, not a search-and-replace. The **Surface**
column names where the term reaches a person today, from the exposure counts
above and from `terms.md`. `agent` means it reaches only the skill prompt.

| Current term | Default user-facing wording | Surface | Detail to preserve |
|---|---|---|---|
| intervention | fix / tracked fix | README, help, status, TUI, HTML | Separate the change from the record that tracks it. |
| class | recurring problem / problem | status, TUI, HTML | Name the behavior, not the category. |
| candidate | problem to consider; suggested fix once one exists | TUI, agent | A cluster is not yet a solution. |
| tier | type of fix | status, TUI, HTML | Name check, guard, instruction or review change. |
| adjudication | decision on a finding | README, status | Say who decided and which way. |
| rejected finding | finding the agent rejected | README, agent | One rejection does not justify suppressing similar findings. |
| baseline | how often this happened before | README, status, HTML | Show what work was counted and when. |
| effect | change in how often the problem occurs | status, HTML | State the comparison limits. |
| verdict (measurement) | result | status, TUI, HTML, JSON | The token stays in JSON and the queue. |
| verdict (finding) | decision on a finding | agent | Same rename as adjudication. |
| confound | conditions changed, so we cannot fairly compare | status, TUI, HTML | Name the changed condition when known. |
| composition | the mix of work changed / review settings changed | status | A caution beside the result, never in place of it. |
| drift | describe the actual change | README, status | Installation mismatch and changed measurement conditions are separate. |
| evidence / corpus | review history / session history / blocked-action history | status, TUI, agent | Preserve source boundaries in counts. |
| ledger | review history and decisions | README, agent | Keep storage details for troubleshooting. |
| friction | recurring problems in agent sessions | agent | Name corrections, failed commands, blocked actions. |
| correction | your correction to the agent | help, agent | Name the behavior corrected. |
| tool failure | failed command / failed test | help | Use the actual failure type. |
| denial | action blocked by Claude Code auto mode | help | Records classifier denials only. |
| audit firing | this guard blocked an action | agent | The audit holds blocks, not every evaluation. |
| harness | coding agent | status, TUI, help | Name Claude Code, Codex or Pi. |
| transcript | session history | README, agent | Explain local availability when it limits a result. |
| session | agent session | agent | Root-session counting goes in details. |
| chain | review sequence | status, agent | Repeated rounds count together; never relabel as commits. |
| unit | the item counted | status, agent | Say review sequences or agent sessions in the count. |
| instrument | how the evidence was produced | status, agent | Show reviewer or parser detail only when a comparison changed. |
| scope | where it applies / work included in this comparison | README, status, TUI | Installation reach and measurement eligibility are different. |
| channel | where reviews run | help | Name the agent hook or the Git pre-commit hook. |
| hook / master hook | hook in setup details; guard when it is one; guard runner in troubleshooting | README, help | Not every hook is a guard. |
| matcher | what this guard checks / how this problem is identified | TUI | Guard detection and evidence matching differ. |
| preset | built-in guard | README, help | Keep the command name in executable text. |
| posture / readiness | setup status / ready to run | status | State concrete facts, not a label. |
| trust review | approve the hooks in `/hooks` | README | Use the host's exact action and location. |
| cadence | automatic refresh / reminder | help | Describe the behavior. |
| resolver / backfill | find recorded review decisions in session history | help, status | It cannot invent a decision the agent never made. |

## Results

### One table, three fields per token

`cli/internal/status/action.go` gains a label beside each token's action and
sentence, as one struct per token rather than a third map. A token then cannot
carry a label without a sentence. Terminal, HTML and TUI read the label from
this table and nothing else.

Each result has three presentation forms:

- **Compact label:** a few words for a badge or a narrow cell.
- **Explanation:** what this means for this problem, with the reason and the
  period examined.
- **Action:** a separate instruction, when the user can act.

### The label table

| Token | Compact label | Action (existing) | Explanation or action text |
|---|---|---|---|
| effective | Fewer with fix | settled | Name the problem, the evidence boundary and the supported range. |
| harmful | More with fix | harmful | Review the fix. Removing it and stopping its tracking are different. |
| ineffective | Target not met | repair | Explain the reduction target plainly; a smaller benefit may exist. |
| collecting | Too early to tell | wait | More comparable work is needed. Give a wait estimate only when supported. |
| inconclusive | No clear result | accept | Say what stays uncertain. Do not promise that waiting resolves it. |
| confounded | Not comparable | accept | Say what changed and which choices remain. |
| unattributed | Benefit unconfirmed | repair | The problem fell, but no guard block was recorded in the compared work after installation. |
| not-triggered | No blocks recorded | repair | Compared work after installation only. Inspect what the guard checks. |
| evidence-incomplete | Missing evidence | adjudicate, by cause | Name what is missing. The sentence and the command depend on the cause; see below. |
| no-baseline | No earlier examples | repair | Inspect how the problem is identified and what history exists. |
| legacy-unscoped | Comparison undefined | repair | No saved rule for which work counts; defining one starts a new period. |
| class-grew | Grew, fix declined | repair | The problem grew after a decision not to install a fix. Reconsider that decision. |
| class-faded | Fewer, fix declined | settled | The problem fell after a fix was declined. Credit nothing. |
| class-steady | Steady, fix declined | settled | Within the scorer's stability bounds; not exactly unchanged. |
| class-inconclusive | No clear trend, fix declined | accept | The evidence does not show how the problem changed. |
| unscored | Not assessed | accept | No result is available; show the reason when known. |
| *(unknown)* | Unknown result | accept | This version of Steer cannot interpret this result. Show the token in details. |

Rules the table follows:

- **Every token has its own label.** `effective` and `class-faded` were one
  label in the draft. Both file under `settled`, but they are different states:
  one measures an installed fix, the other reports a class after a fix was
  declined. A badge loses the adjacent applied-or-declined field, so the
  qualifier "with fix" / "fix declined" carries that field into the label.
  "Fix declined" names the recorded decision (`TrendFor` classifies "a rejected
  class by how its incidence moved after the refusal", `stats.go:86`) and claims
  nothing about whether another fix exists.
- **`evidence-incomplete` has four causes and one command fixes one of them.**
  Chain corpus: ledger rows the resolver closed as gone, or chains with no scope
  metadata (`interventions.go:440-455`). Session corpus: sessions with no scope
  metadata, or a guard whose audit log is absent (`session.go:355-399`). Only
  the first is repaired by `steer commit-gate backfill`. The coverage counts that
  separate the causes already reach the renderer, so the sentence and the
  command are chosen from them, with no new token. This is a behavior change
  to `action.go` and lands as its own commit in step 1.
- **The six section headings are in scope.** `ActionHeading` (`action.go:32-38`)
  organizes the whole screen, and "Accept — no repair settles these; read the
  counts and decide" names no object. Each heading gets replacement text with
  an example, evaluated together with the labels, sentences and actions under it.
  `TestEveryActionIsOrderedAndHeaded` already pins that every action has one.
- **A label names a state.** "Reconsider the refusal" is an action and belongs
  in the sentence, where `action.go:70` already puts it.
- **Directional words appear only where the scorer claimed a direction.** Those
  are the four bound-crossing tokens: `effective`, `harmful`, `class-grew`,
  `class-faded`. Every other label states no direction, for the reason `c8af1af`
  removed the range from withheld verdicts: a figure or a word beside a withheld
  result states the point claim the scorer refused.
- **Soft `composition` warnings keep their own label** — "Work mix changed" or
  "Review settings changed" by cause — shown beside the result. The scorer
  permits those comparisons, so "Not comparable" would misstate them.
- Fit labels to the real TUI and HTML widths. Keep the full explanation reachable
  by keyboard; meaning never depends on hover, color or truncation alone.

### The mechanical test

A table test over the label table, run by `check.sh`, pins:

- every token in `actionFor` carries a non-empty label;
- no two tokens share a label;
- every non-settled token carries a non-empty action sentence;
- no label contains a directional word (`less`, `more`, `fewer`, `grew`,
  `faded`, `rose`, `fell`, `up`, `down`), matched on word boundaries, unless the
  token is one of the four above. This list encodes the `c8af1af` rule
  mechanically. A false positive costs a word choice; a miss costs a lie on
  screen, so the list stays;
- every token in `actionFor` is present, which is the pin that the tokens did
  not move (`TestJSONContractIsStable` pins one fixture and cannot serve);
- every action in `ActionHeading` carries a heading that names an object;
- the unknown-token path returns the "Unknown result" label and a non-empty
  sentence.

### Actions

- **Apply this fix:** make the described change. Name the files, agents or
  configuration affected, and whether tracking starts in the same operation.
- **Keep this fix:** leave the change in place. This claims no measured benefit.
- **Skip this suggestion:** make no change; say what decision is recorded.
- **Stop tracking this fix:** end its measurement. Say the change stays in place.
  This is `retire`, which touches no hook, check or instruction file.
- **Remove this guard:** remove the guard. Name the installation reach and,
  separately, what happens to its tracking record. Custom-guard removal and
  built-in preset disabling stay distinct.
- **Accept / reject this finding:** the coding agent's decision. `/steer-report`
  asks which suggestions to apply or skip; those choices decide no finding.

Copy describes the operation that exists today. "Apply", "keep" and "skip" are
workflow labels, not commands. Installation and tracking registration are
separate steps, and each interface maps its label to the steps it runs. A
combined "remove and stop tracking" needs an operation that does both.

## The `/steer-report` decision experience

The skill's reading vocabulary stays on the field names and tokens in force.
Only the generated report uses the plain wording. The skill instructions carry
the token-to-label table once, so the report speaks the label and the JSON
keeps the token.

Each proposal answers, in connected prose:

1. What problem keeps happening? Show a representative example.
2. What supports that? Name the source, the amount of relevant work, the limits.
3. What change is proposed? Say the behavior affected and where it applies.
4. What is the tradeoff? Name legitimate work it could block.
5. What decision is needed? State the action and its consequence.

Illustrative proposal, hypothetical evidence:

> The agent used `python` where this project requires `python3` in three
> recorded sessions. I suggest a guard that blocks the bare command and tells
> the agent to use `python3`. It applies in this project to the named coding
> agents. It could also block intentional uses of `python`. Apply this guard and
> track whether the problem becomes less frequent?

Illustrative result:

> Too early to tell whether this guard helps. The problem appeared in three of
> ten sessions before installation and one of four since. More comparable
> sessions are needed. The guard stays installed; no action is needed now.

## Compatibility: what moves and what stays

- **Verdict tokens stay.** They are read by the skill from the queue and the
  snapshot, and by a person from `--json`. A label such as "Grew, fix declined"
  can never be a token, so a slug set exists either way. Moving them buys a bump
  of `StatusVersion` and `QueueVersion`, a rewrite of about 70 skill lines, and
  new slugs that still differ from the labels. The label table is the bridge.
  Reopen only if a token misleads a `--json` reader in practice.
- **Stored fields stay.** Each rename buys a permanent legacy-read branch;
  `baseline.chains` to `baseline.units` is the precedent.
- **CLI commands: rename with aliases.** `steer fix` becomes the primary tree
  and `steer intervention` the alias, through the one factory. Open point: `steer
  fix score` reads as an imperative; `steer fixes` is the alternative to decide
  before the rename lands. Every command `status` prints in a sentence
  (`format.go:200`, `readiness.go`) switches to the primary name.
- **Printed strings, help text, README, templates: rename.**
- **Go symbols: defer.** About 90 occurrences in `status/` alone, and 112
  trailered commits name the current symbols; a rename breaks the match between
  those trailers and the code they describe. Decide it separately once the noun
  is proven on screen. Until then, one comment on the `Intervention` type names
  the user-facing word.
- **Validate `--tier` at write time, over both vocabularies.** Once tiers
  display as names, a typo tier falls through `TierPhrase` and renders as
  itself. The accepted set is the union of `check|steer|prose|prompt` and
  `check|guard|instruction|review`, held in one place that `guardscope.go:59`,
  `TierPhrase` and `RemovalPhrase` all read; today each enumerates its own.

## Implementation sequence

1. **Label table and wiring.** Add the label to the `action.go` table, the
   unknown-token label and sentence, the mechanical test, and read the label in
   terminal, HTML and TUI. Rename the printed strings and the six headings on
   those surfaces. One commit; `check.sh` is the oracle. A second commit splits
   `evidence-incomplete` by cause, because that changes which command the
   reader is given.
2. **Skill speech.** Rewrite `/steer-report` proposals, results and lifecycle
   prose. Add the token-to-label table to the skill instructions. Build a
   report-scenario runner under `tests/`: the existing harness
   (`tests/skill_test.py`) generates guards through the steer skill and checks
   them against fixtures; it generates no reports. The runner feeds six saved
   snapshots to `/steer-report` headlessly and stores the output beside them.
   It runs from `check-full.sh`, never from `check.sh`.
3. **Commands.** Add `steer fix` (or `steer fixes`) as primary with the old tree
   as alias; switch printed commands; validate `--tier`. Rebuild binaries.
4. **Onboarding, help, README, reminders, errors.** Align to the same table.
5. **Go symbol rename.** A separate decision after step 4 ships.

## Validation

Two halves, two oracles.

- **Mechanical.** Terminal, HTML and TUI render from one table, so the label
  test plus `action_test.go`, `html_test.go` and `status_test.go` cover them.
  The label test's token-presence check is what proves the tokens did not move;
  `TestJSONContractIsStable` pins one fixture carrying `effective` only.
- **Comprehension, which is not a test.** Six saved scenarios: a new suggestion,
  an installed guard awaiting evidence, a changed comparison, missing review
  decisions, a declined suggestion, a request to remove a guard. Regenerate the
  report over them and read side by side with people unfamiliar with Steer.
  Record misunderstandings and revise the copy. Keep the checklist in the repo.

Acceptance: without a glossary, a user can say

- what problem Steer found and where the evidence came from;
- what applying a suggested fix changes and where;
- whether a guard is installed, whether it ran, and whether its benefit is known;
- why a result is uncertain and whether any action helps;
- whether an action removes a change or only stops measuring it.

Technical acceptance: every token keeps its action, except that
`evidence-incomplete` names the command its cause needs; a withheld result gains
no improvement claim; counts keep their denominator; every displayed command runs.

## Evidence

- [terms.md](terms.md): the current vocabulary inventory by audience.
- [README.md](README.md): review-first positioning and the limit that fewer
  findings alone prove nothing.
- [action.go](cli/internal/status/action.go): the total action table, the
  `accept` fallback with an empty sentence, `claimsAnEffect`, `RemovalPhrase`.
- [format.go](cli/internal/status/format.go): `TierPhrase`, the printed commands.
- [refresh.go](cli/internal/cadence/refresh.go): the queue carries the token.
- [stats.go](cli/internal/gate/mine/stats.go): `class-grew` / `class-faded` are
  bound crossings.
- [deny.md](cli/internal/gate/prompts/deny.md) and
  [ledgertext.go](cli/internal/gate/ledgertext.go): the per-finding "verdict".
- Commit `c8af1af`: a verdict names a state and an action names what to do; the
  range prints only under `effective` and `harmful` because a figure beside a
  withheld verdict states the point claim the scorer refused. Its trailers also
  reject a certification ladder and reject crediting a silent guard's streak.
  This plan keeps all three.
- Commit `9c43034`: the table is total, unknown reads as `accept`, and an
  unreachable window overrides `wait` alone.

Any change to scoring, authorization, removal behavior or command structure is
a separate behavioral change and is reviewed as one.

## Decisions record

Resolution of the review recorded in `terms-astra-plan.md`.

| Item | Decision |
|---|---|
| Position, rules, actions, `confounded` label, composition warnings | Accepted as written. |
| 1. Four labels | Accepted, with state labels instead of the action-shaped candidates. |
| 2. Directional words | Accepted, permitted on four tokens, not two: the code already claims direction for `class-grew` and `class-faded`. |
| 3. Move the tokens | Rejected. Tokens are read by the skill and by `--json`; labels cannot be tokens; the label table is the bridge. |
| 4. Rename by layer | Accepted for commands and strings; Go symbols deferred. |
| 5. Split the validation | Accepted; the snapshots ride the existing headless harness. |
| 6. Skill reads field names | Accepted; the skill carries the token-to-label table. |
| 7. Surface per row | Accepted; the Surface column above. |
| 8. Correct the citation | Not a correction: the draft's description of `c8af1af` was accurate. Both parts are now cited. |

## Feedback: Astra review with Luna code verification (resolved)

Use this plan as the implementation basis after the corrections below. It makes
concrete choices about labels, commands and compatibility while preserving the
familiar-language goal. Separate labels, explanations and actions provide the
right structure for retaining distinctions users need when acting.

### Substantive gaps

- **“Without a fix” claims too much.** A rejected record establishes that a
  proposed response was declined; it does not establish that no other fix exists.
  `mine.Reject` records that decision, and a proposal can explicitly say no suitable
  intervention was identified. Prefer a short result such as “Less frequent” or
  “More frequent” with separate “Suggestion declined” context. If a badge travels
  alone, carry that context with it. Requiring every token to have a unique label
  is an implementation preference, not a UX requirement. See
  [intervention.go](cli/cmd/intervention.go) and
  [intervention_test.go](cli/internal/gate/mine/intervention_test.go).
- **“Missing evidence” needs a cause-specific action.** Missing evidence can mean
  absent audit logs or eligibility metadata, with no finding to decide. Routing
  every such result to `adjudicate` can misdirect the user even when the explanation
  names the right cause. Revise “every token keeps its action” to preserve correct
  consequences while allowing the response to depend on what is missing. See
  [session.go](cli/internal/gate/mine/session.go),
  [interventions.go](cli/internal/gate/mine/interventions.go) and
  [action.go](cli/internal/status/action.go).
- **Report scenario testing requires new harness work.** The existing headless
  harness generates guards and tests them against positive and negative fixtures;
  it does not generate the proposed six report scenarios. Budget and describe the
  report-generation and snapshot support explicitly. See
  [check-full.sh](check-full.sh), [skill_test.py](tests/skill_test.py) and
  [run_tests.py](tests/run_tests.py).

### UX recommendation: specify the section headings

The plan improves badges but leaves “Adjudicate”, “Repair”, “Accept” and “Settled”
insufficiently specified. “Accept” especially needs an object: accept what?
These headings organize the whole screen and deserve explicit replacement text
and examples. Otherwise scientific language can become ambiguous ordinary
language while the decision remains difficult. Evaluate each heading together
with the result, explanation and actual action underneath it.

### Smaller factual corrections

- `effective` and `class-faded` both map to `settled`. They are different states,
  but the paragraph claiming different actions contradicts both the label table
  and [action.go](cli/internal/status/action.go).
- New tier validation must preserve supported aliases rather than validate only
  the spellings advertised in help. Reconcile the accepted vocabulary across
  [format.go](cli/internal/status/format.go),
  [guardscope.go](cli/internal/guardscope/guardscope.go) and removal wording before
  adding validation; readers do not all enumerate the same aliases today.
- `TestJSONContractIsStable` checks representative output, not the complete token
  vocabulary. Add explicit token coverage if the requirement is to establish
  that every token remains unchanged. See
  [status_test.go](cli/internal/status/status_test.go).

### Validation recommendation

Replace the directional-word blacklist with tests of approved labels and their
state mappings. A blacklist can reject an innocent phrase while missing a
misleading implication. The useful invariant is that each displayed result
accurately describes its state. Retain human comprehension checks for the meaning
of the complete presentation, including the headings and available actions.

With these changes, the plan is ready to implement. The remaining work is chiefly
making the user's next decision clear; another broad terminology rethink would
add little. These are review recommendations, not changes to the decisions or
implementation recorded above.

### Resolution

Each point was checked against the code before it was applied.

| Point | Decision | Where it landed |
|---|---|---|
| "Without a fix" claims too much | Accepted in part. The qualifier now reads "fix declined", which names the recorded decision. Unique labels stay: a badge alone in a narrow cell is the case the rule exists for. | Label table; "Every token has its own label". |
| "Missing evidence" needs a cause-specific action | Accepted. Four causes verified in `session.go` and `interventions.go`; one command fixes one of them. Sentence and command now vary by cause, as a behavior-change commit in step 1. | Label table; new rule; step 1; technical acceptance. |
| Report scenarios need new harness work | Accepted. `tests/skill_test.py` generates guards, not reports. Step 2 now budgets a report-scenario runner in `check-full.sh`. | Step 2. |
| Specify the section headings | Accepted. The six `ActionHeading` strings join the rewrite scope and the mechanical test. | New rule; mechanical test; step 1. |
| `effective` and `class-faded` both map to `settled` | Accepted. The sentence claiming different actions was wrong and is rewritten. | "Every token has its own label". |
| Tier validation must preserve aliases | Accepted. The set is the union of both vocabularies, in one place. | Compatibility. |
| `TestJSONContractIsStable` pins a fixture, not the token set | Accepted. The label test's token-presence check is the pin; the validation text is corrected. | Mechanical test; Validation. |
| Replace the directional-word list with approved-label tests | Rejected. An approved-label test pins what the author wrote and catches nothing unforeseen. The word list encodes the `c8af1af` rule; matching on word boundaries handles the innocent-phrase case. | Mechanical test. |
