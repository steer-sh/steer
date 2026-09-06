# Proposal: familiar language for Steer's user experience

## Position

Steer should explain its work in language users already understand: review,
problem, suggested fix, guard, and result. Users should be able to decide what to
change without learning the vocabulary of the measurement system.

The proposed product description is:

> Steer reviews your agent's code, spots recurring problems, helps you choose
> fixes, and tracks whether those problems happen less often.

Prefer **fix** to **intervention** in human-facing language. A fix describes an
intended purpose; its status describes whether it worked. “Suggested fix,”
“untested fix,” and “this fix didn't help” are ordinary, understandable phrases.
The modest loss of technical precision is worth the reduction in learning burden.

This is a proposal based on Jeremy's discussion and the repository sources below.
It applies familiar-language, consistency, progressive-disclosure, and explicit-action
principles. It is not a claim that usability testing has established these exact
labels as optimal. Comprehension testing is part of the rollout.

## What the starting document establishes

[terms.md](terms.md) inventories current usage and separates user documentation
from agent documentation. Many substantive definitions occur only in skill files
or CLAUDE.md. Those definitions explain intended semantics to the agent; they do
not explain the product to the person reading a dashboard or answering a question
from `/steer-report`.

The current inventory also excludes ordinary words. The language proposal must
include them: words such as “accept,” “remove,” and “retire” can determine whether
a user correctly predicts an action's consequences.

The working hypothesis is that the cumulative vocabulary creates a substantial
learning burden. Each unfamiliar noun introduces another concept to remember;
sentences combining several of them require the user to translate the system
before making a decision. The extent of that burden remains to be tested with
users unfamiliar with Steer.

## Design rules

1. **Lead with the user's problem and choice.** Describe what happened, what a
   proposed change would do, and what decision is needed.
2. **Use familiar words even when they are less technically exact.** Restore
   precision where it changes the interpretation or consequence of a decision.
3. **Explain locally.** A screen or agent message must supply the meaning needed
   to act. A glossary or skill definition is supporting reference.
4. **Name the object of an action.** “Accept this finding,” “apply this fix,” and
   “stop tracking this fix” are different operations. Bare “accept” is insufficient.
5. **Separate installation from results.** A guard can be installed while its
   benefit remains unknown. A measured result can change as more evidence arrives.
6. **Name the limits in everyday language.** Explain why Steer cannot tell whether
   something helped, and whether the user can do anything about it.
7. **Keep technical detail available.** Evidence sources, eligibility rules,
   methods, identifiers, and exact commands belong in supporting details when
   they help someone inspect or repair a result.
8. **Keep meanings consistent across surfaces.** CLI output, dashboards, README,
   and skill-generated explanations should describe the same objects and actions.

## Core concepts

| User-facing term | Meaning and use |
|---|---|
| Review | An independent examination of a proposed code change before it commits. |
| Finding | A concern raised by a reviewer that still needs to be checked against the code. Use “review finding” when context is missing. |
| Recurring problem | A pattern of trouble found in review or session history. Show concrete examples; recurrence alone does not establish the cause. |
| Suggested fix | A proposed change intended to reduce a recurring problem. Use only when an actual change has been proposed. |
| Fix | A change intended to address a problem, such as a check, guard, instruction, or review change. The word does not certify success. |
| Check | An executable test or verification of repository content. |
| Guard | A check on an agent's action before it runs, which can block the action and explain how to correct it. |
| Instruction | Written guidance for the coding agent. Describe the file and behavior being changed. |
| Review change | A change to the review prompt intended to address recurring reviewer mistakes. General reviewer configuration changes are separate settings operations. |
| Rule | A statement of desired behavior. Explain whether a guard enforces it or an instruction communicates it. |
| Result | What Steer can currently conclude about the tracked problem after the fix. |

These concepts provide a small foundation. Users should not have to study all of
them before their first review. Introduce a concept when its corresponding task
appears.

Keep “commit gate” available as the existing feature and command name; introduce
it as “review before commit.” Describe its actual blocking and review behavior
where users enable it. Avoid adding another branded name for the same feature.

At setup, explain the roles: Steer runs an installed external reviewer; the coding
agent checks its findings against the code. “Steer reviews” remains useful product
shorthand, but must not obscure reviewer selection or who decides a finding holds.

The current tier vocabulary maps `check` to check, `steer` to guard, `prose` to
instruction, and `prompt` to review-prompt change. Preserve those command values
while simplifying the displayed names. Do not imply that changing reviewer settings
automatically registers a tracked fix.

## Proposed terminology changes

This table covers the starting glossary plus missing concepts raised in the
discussion. It proposes contextual wording, not a global search-and-replace.

| Current term | Default user-facing wording | Detail to preserve |
|---|---|---|
| intervention | fix / tracked fix | Separate the change from the record tracking its results. |
| class | recurring problem / problem | Name the actual behavior, not just its category. |
| candidate | problem to consider; suggested fix once a fix exists | An evidence cluster is not yet a proposed solution. |
| tier | type of fix | Name check, guard, instruction, or review change. |
| adjudication | decision on a review finding | Say who checked the concern and whether it was accepted or rejected. |
| rejected finding | finding the agent rejected | An individual rejection does not automatically justify suppressing similar findings. |
| baseline | how often this happened before | Show what work was counted and when. |
| effect | change in how often the problem occurs | State the problem and comparison limits; avoid implying overall code quality. |
| verdict | result | Keep the exact result token available in technical details. |
| confound | conditions changed, so we can't fairly compare | Name the changed condition when known. |
| composition | the mix of work changed; review settings changed | Distinguish a caution from a change that prevents comparison. |
| drift | describe the actual change | Installation mismatch and changed measurement conditions need separate explanations. |
| evidence | evidence / examples / history | Prefer the concrete source where possible. |
| corpus | review history / session history / blocked-action history | Preserve source boundaries in counts and comparisons. |
| ledger | review history and decisions | Retain storage details for troubleshooting and export. |
| friction | recurring problems in agent sessions | Name corrections, failed commands, or blocked actions when known. |
| correction | your correction to the agent | Identify the concrete behavior corrected. |
| tool failure | failed command / failed test / failed action | Use the actual failure type. |
| denial / auto-denials corpus | actions blocked by Claude Code auto mode / history of those blocks | This source records auto-mode classifier denials, not every Claude Code block. |
| audit / audit firing | guard blocks / this guard blocked an action | The audit records block decisions, not every guard evaluation or allowed action. |
| harness | coding agent / agent application | Name Claude Code, Codex, or Pi; distinguish the application from its model when needed. |
| transcript | session history | Explain local availability when it limits the result. |
| session | agent session | Explain root-session counting in measurement details. |
| chain | review sequence | Explain that repeated review rounds are counted together; do not relabel chains as commits. |
| unit | the actual item counted | Say review sequences or agent sessions in the count itself. |
| signal kind | type of problem recorded | Name the specific event category. |
| instrument | how the evidence was produced | Show relevant reviewer or parser details when explaining a changed comparison. |
| scope | where it applies / work included in this comparison | Always distinguish installation reach from measurement eligibility. |
| channel | where reviews run | Name the agent hook or Git pre-commit hook when setup requires it. |
| hook | hook in setup details; guard when it is a guard | Not every hook is a guard; lifecycle hooks may run reminders or reconciliation. |
| master hook | guard runner, in troubleshooting | Usually omit this implementation object from the main task. |
| matcher | what this guard checks / how this problem is identified | Guard detection and evidence matching are different functions. |
| preset | built-in guard | Preserve the existing command name in executable instructions. |
| posture | setup status / what is active | State concrete facts instead of a general posture label. |
| readiness | ready to run / setup needed | Distinguish configured, available, trusted, and observed where the system can establish them. |
| trust review | approve the hooks in `/hooks` | Use the host application's exact action and location. |
| cadence | automatic refresh / reminder | Describe the behavior; usually omit the internal entry-point name. |
| resolver / backfill | find recorded review decisions in session history | Link existing decisions to review records; this cannot invent decisions the agent never made. Keep exact commands in troubleshooting. |

## Results and actions

Show a short result followed by its reason and a specific next action. Preserve
distinct internal states even when the first line uses similar plain language.

### Compact labels, explanations, and actions

The terminology table above supplies meanings and contextual wording. Its long
phrases are not proposed tag text. Give each result three presentation forms:

- **Compact label:** a few familiar words for a tag, badge, or narrow table cell.
- **Explanation:** what this means for the specific problem, including the reason
  and the period examined. Place it beside the result or in accessible details.
- **Action:** a separate instruction when the user can usefully act.

For `confounded`, use the compact label **Not comparable**. An explanation could
say “The reviewer changed between the before and after periods, so Steer cannot
fairly compare them.” The next action depends on the actual reason and available
repair; the label alone should not prescribe one.

Soft `composition` warnings retain a separate label, such as **Work mix changed**
or **Review settings changed**, according to the cause. The scorer still permits
these comparisons, so “Not comparable” would misstate them. Display the warning
beside the result rather than replace the result with it.

Fit labels to actual TUI and HTML layouts and test narrow widths. Preserve the
full explanation through a details view reachable by keyboard; essential meaning
must not depend solely on hover, color, or a truncated sentence.

| Existing result | Proposed compact label | Supporting explanation or action |
|---|---|---|
| effective | Less frequent | Name the tracked problem, evidence boundary, and supported range. |
| harmful | More frequent | Review the fix; distinguish removing it from stopping its tracking. Avoid an unsupported causal claim in the label. |
| ineffective | Target not met | Explain the reduction target in everyday terms; a smaller benefit may still exist. |
| collecting | Too early to tell | More comparable work is needed; give a wait estimate only when supported. |
| inconclusive | No clear result | Explain what remains uncertain; do not promise that waiting will resolve it. |
| confounded | Not comparable | State what changed and which choices remain available. |
| unattributed | Benefit unconfirmed | The problem became less frequent, but no guard blocks were recorded in the compared work after installation. |
| not-triggered | No blocks recorded | Qualify this as the compared work after installation, not the guard's lifetime. Inspect what it checks and whether relevant work occurred. |
| evidence-incomplete | Missing evidence | Name missing review decisions, eligibility metadata, or guard audit evidence as applicable. Explain whether recovery from available history is possible; adjudicating findings is not a universal repair. |
| no-baseline | No earlier examples | Inspect how the problem is identified and what earlier history is available. |
| legacy-unscoped | Comparison undefined | This record has no saved rule for which work to include; defining one starts a new comparison period. Earlier examples may still exist. |
| class-grew | More frequent | This problem grew after a decision not to install a fix; reconsider that decision. |
| class-faded | Less frequent | This problem declined after a decision not to install a fix; do not credit an uninstalled fix. |
| class-steady | Broadly unchanged | The measured range stays within the scorer's stability bounds; this does not mean exactly the same frequency. |
| class-inconclusive | No clear trend | The evidence does not establish how this problem changed after the decision not to install a fix. |
| unscored | Not assessed | No measured result is available; show the reason when known. |

Keep each result alongside whether a fix was applied or declined: shared labels
such as “Less frequent” need that context. A declined fix must not appear as an installed fix. An unknown
result should say it cannot be interpreted and offer details, rather than imply a
repair or success. This is proposed behavior: the current unknown-token fallback
uses the `accept` action with no explanatory action sentence.

Use these action distinctions consistently:

- **Apply this fix:** make the described change. Name the affected files, agents,
  or configuration and whether tracking starts as part of the operation.
- **Keep this fix:** leave the change in place; this need not claim a measured benefit.
- **Skip this suggestion:** make no proposed change; explain any recorded decision.
- **Stop tracking this fix:** end its measurement. State that the change stays in place.
- **Remove this guard:** remove the guard. State the affected installation reach
  and separately describe what happens to its tracking record. Keep custom-guard
  removal distinct from built-in preset disabling and its operator-only restriction.
- **Accept/reject this finding:** describe the coding agent's decision about a
  review concern. `/steer-report` asks the user which suggestions to apply or skip;
  those choices do not adjudicate findings or authorize suppressing future concerns.

Copy must describe the operation implemented today. A combined “remove and stop
tracking” action requires an operation that actually performs both steps.
“Apply,” “keep,” and “skip” are proposed workflow labels, not new CLI commands or
existing atomic operations. Installation and measurement registration are separate
steps today; each interface must map its label to the concrete steps it performs.

## The `/steer-report` decision experience

Skill files need explicit instructions for how the agent speaks to the user.
Internal technical instructions can retain exact field names and commands while
the generated report uses the vocabulary above.

Each proposal should answer these questions in connected, concrete language:

1. What problem keeps happening? Show a representative example.
2. What supports that claim? Name the source, amount of relevant work, and limits.
3. What change is proposed? Explain the affected behavior and where it applies.
4. What is the tradeoff? Name legitimate work it could block or other relevant costs.
5. What decision is needed? State the proposed action and its consequences.

Illustrative proposal, with hypothetical evidence:

> The agent used `python` where this project requires `python3` in three recorded
> sessions. I suggest a guard that blocks the bare command and tells the agent to
> use `python3`. It would apply in this project to the named coding agents. It
> could also block intentional uses of `python`. Apply this guard and track
> whether the problem becomes less frequent?

Illustrative result:

> Too early to tell whether this guard helps. The problem appeared in three of
> ten sessions before installation and one of four since. More comparable
> sessions are needed. The guard remains installed; no action is needed now.

Keep the evidence definition, comparison method, exact commands, and identifiers
available in details. The agent should handle technical formulation and ask the
user about behavior, applicability, and tradeoffs that require their judgment.

## Implementation sequence

1. **Audit actual user exposure.** Extend the inventory to CLI help and output,
   status, HTML, dashboard, prompts for decisions, errors, and skill-generated
   reports. Mark each occurrence as human-facing, agent-only, or internal.
   Treat a definition in a skill as agent documentation only.
2. **Set the language contract.** Agree the core nouns and action consequences;
   add approved contextual phrases and examples. Include ordinary action words.
   Keep one canonical vocabulary reference, with compact instructions in each
   skill so normal user output does not depend on a separate lookup.
3. **Rewrite the decision surfaces first.** Start with `/steer-report` proposals,
   results, and lifecycle actions. Change whole messages, not isolated labels.
   Use the shared status presentation layer for terminal, HTML, and dashboard
   wording. Then align onboarding, help, reminders, and errors.
4. **Preserve compatibility deliberately.** Retain stored fields, verdict tokens,
   existing commands, and flags during the language pass. Agent instructions
   must map plain-language concepts to current commands. Any friendlier command
   aliases are a separate implementation decision; never show an invented command
   as executable guidance.
5. **Validate comprehension and behavior.** Review representative screens and
   generated reports with Jeremy and people unfamiliar with Steer. Check every
   changed result mapping and action consequence. Run the repository oracle for
   implementation changes and rebuild binaries when CLI changes are committed.

## Acceptance criteria

Without reading a glossary, a user should be able to explain:

- What problem Steer found and where that evidence came from.
- What applying a suggested fix will change and where it will apply.
- Whether a guard is installed, whether it has run, and whether its benefit is known.
- Why a result is uncertain and whether any useful action is available.
- Whether an action removes a change or only stops measuring it.

Test those questions with a new suggestion, an installed guard awaiting evidence,
a changed comparison, missing review decisions, a declined suggestion, and a
request to remove a guard. Record misunderstandings and revise the copy. Successful
recall of technical definitions is not the success criterion.

Technical validation should also establish that every result retains its correct
action, a withheld result gains no unsupported improvement claim, counts retain
their actual denominator, and all displayed commands remain valid.

## Evidence and decision continuity

- [terms.md](terms.md): the current vocabulary and audience-specific explanation inventory.
- [README.md](README.md): review-first positioning, recurring-problem workflow, and
  the limit that fewer findings alone do not prove improvement.
- [CLAUDE.md](CLAUDE.md): measurement semantics, installation boundaries, and shared
  presentation architecture. These are implementation constraints, not user copy.
- [action.go](cli/internal/status/action.go): existing result-to-action mappings,
  recurrence wording, and the explicit separation between retirement and removal.
- [session.go](cli/internal/gate/mine/session.go),
  [interventions.go](cli/internal/gate/mine/interventions.go), and
  [stats.go](cli/internal/gate/mine/stats.go): missing-evidence conditions,
  period-specific guard attribution, and applied/declined result semantics.
- [confound.go](cli/internal/gate/mine/confound.go): hard changes prevent a
  comparison; soft composition warnings retain the result.
- [runtime.py](cli/internal/guard/runtime.py) and [denials.go](cli/cmd/denials.go):
  guard audit records contain blocks, and the denials source names auto mode.
- Commit `c8af1af`: separates measurement state from action and rejects a permanent
  certification ladder and success claims based on silent guards. This proposal
  preserves those decisions while simplifying the words users encounter.
- Commit `9c43034`: establishes complete result mapping and preserves meaningful
  repair actions even when a measurement would take too long. Plain wording must
  preserve that behavior.

The proposal changes the language and information users must process. Any change
to scoring, automatic authorization, removal behavior, or command structure should
be identified and reviewed as a separate behavioral change.

## Feedback

This section records the review of the proposal and the decisions taken on it.
Numbered items are changes to make before the plan is implemented.

### Accepted as written

The position stands. **fix** replaces **intervention** in human-facing language.
The objection that ordinary English "fix" certifies success was raised and
withdrawn: "an unproven fix" and "the fix did not work" are ordinary. The word
carries no success claim that needs a technical term to avoid.

The design rules stand. The action distinctions in "Results and actions" are the
most valuable part of the proposal and need no rename to ship.

`confounded` to **Not comparable** stands, and so does keeping soft `composition`
warnings as a separate label beside the result rather than in place of it.

### 1. Four verdicts need four labels

The table gives "Less frequent" to both `effective` and `class-faded`, and "More
frequent" to both `harmful` and `class-grew`. Those pairs are different states:
one measures an installed fix, the other reports a class after a decision not to
install one. `cli/internal/status/action.go` files them under different actions —
`settled`, `harmful` and `repair`.

The proposal resolves the collision by keeping the applied-or-declined status
beside the label. A compact label is written for a badge and a narrow table cell,
which is where an adjacent field is lost. Give each verdict token its own label.

Candidate wording, to be settled during the rewrite:

| Verdict | Candidate label |
|---|---|
| effective | Less frequent |
| harmful | More frequent |
| class-grew | Reconsider the refusal |
| class-faded | Refusal held |
| class-steady | Unchanged without a fix |
| class-inconclusive | No clear trend |

### 2. Reserve directional words to the two verdicts that claim a direction

Commit `c8af1af` removed the range from every verdict but `effective` and
`harmful`, because "a percentage beside a withheld verdict states the point claim
the scorer refused". A label reading "Less frequent" is that same claim in words.
Applying it to `class-faded` restores through the badge what the range change
removed.

Make this mechanical rather than remembered. Add a table test over the label map
that pins:

- every verdict token carries a compact label;
- no two tokens share a label;
- every non-settled token carries an action sentence;
- no label contains a directional word unless the token is `effective` or
  `harmful`.

Run it from `check.sh`.

### 3. Move the verdict tokens with the labels

Compatibility rule 4 retains the stored verdict tokens. Its justification is
`c8af1af`: `--json` keeps the token "so a user asking a skill 'why is this
confounded' shares its vocabulary". This proposal removes the word `confounded`
from the screen, which retires that justification. A retained token becomes a
third vocabulary rather than a shared one.

Move the tokens to the new words. Bump `StatusVersion`, and change
`cli/internal/status/status.go`, the root `SKILL.md` and
`skills/steer-report/SKILL.md` in one commit. `TestJSONContractIsStable` fails
if the three separate.

### 4. Rename by layer, not uniformly

Rule 4's blanket retention is wrong for two of the four layers.

- **Go symbols: rename.** The compiler is the oracle and nothing outside the
  repository reads a type name. Leaving `Intervention` in the code while the
  product says "fix" carries the plan's own learning burden into the source.
- **CLI commands: rename with aliases.** A user types these, and
  `action.go` prints them inside instruction sentences. `steer fix` becomes the
  primary name and `steer intervention` an alias; `cli/cmd/dashboard.go:11`
  already carries this pattern for `dashboard` / `top`.
- **Stored fields: leave them.** Each rename buys a permanent legacy-read
  branch. `baseline.chains` to `baseline.units` is the precedent, and
  `mine.baselineUnits` never goes away.
- **`--json` keys and verdict tokens: move**, per item 3.

### 5. Split the validation, and label the half that is not a test

Step 5 asks for comprehension review across the whole change. The change has two
halves with different oracles.

- The terminal, HTML and TUI surfaces all render from `action.go`'s tables, so
  their correctness is mechanical. Item 2's table test covers it, plus the
  existing `action_test.go`, `html_test.go` and `status_test.go`.
- The `/steer-report` speech has no oracle and should not claim one. Fix the six
  scenarios named under "Acceptance criteria" as saved snapshots, regenerate the
  report over them after the rewrite, and read them side by side. Record this as
  a checklist.

### 6. Keep the skill's reading vocabulary on the field names

Lines 217-219 already separate internal instructions from generated output. Make
it explicit that the instructions name the keys that are in force after item 3,
and that only the generated report uses plain wording.

### 7. Mark each terminology row with the surface it appears on

`terms.md` reports `none` in the user-documentation column for roughly half the
inventoried terms. Step 1 of the implementation sequence produces the true
exposure list; the terminology table was written before it. Annotate each row
with the surface that shows the term, so the rewrite is scoped by evidence rather
than by the inventory's length.

### 8. Correct one evidence citation

The entry for commit `c8af1af` describes it as rejecting "a permanent
certification ladder and success claims based on silent guards". Its own message
states a narrower and more useful rule: the range prints only under `effective`
and `harmful`, because a figure beside a withheld verdict states the point claim
the scorer refused. Cite the rule that item 2 enforces.
