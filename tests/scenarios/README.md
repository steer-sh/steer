# Report scenarios

Saved snapshots covering the decisions a reader arrives at `/steer-report`
with. `scenarios.py` builds each one over `base_status.json` as a status snapshot,
its `steer fix score --json` rows and a queue; `../report_scenarios.py` feeds them to
the skill headlessly and writes the reports into `out/`.

There is no mechanical oracle for prose quality. The runner explicitly requests
a Read of the working-tree skill and records successful skill-file reads in each
report's first line. A missing `SKILL.md` read invalidates the run; `audit.md` is
also required when the snapshot holds an `interventions[]` or `hooks[]` row or nonzero
`telemetry.audit_firings` or `telemetry.check_blocks`, and the steer skill's
`prompts/routing.md`, copied in beside it, when the queue holds candidates. Model errors and sandbox
leaks invalidate the run too. The runner exits nonzero and writes no replacement
report on failure; an older output may still be present. A person judges valid reports.

| Scenario | The reader's question |
|---|---|
| `new-suggestion` | Should I apply this fix? |
| `awaiting-evidence` | What has been observed since this guard was installed? |
| `changed-comparison` | Who counted the two windows, and what can these counts establish? |
| `missing-decisions` | What is missing, and can I do anything about it? |
| `declined-suggestion` | We said no to this — has that aged badly? |
| `remove-a-guard` | How do I get rid of this, and what does that remove? |
| `first-run` | Nothing is installed and no commit was reviewed yet: what now? |
| `busy-report` | What needs attention among several quiet fixes, a fresh fix, a tracking error and a weak candidate? |
| `agent-feedback` | Is the agent's report of blocked valid work confirmed or still unchecked? |
| `agent-feedback-confirmed` | What should change after inspection confirms a wrong block? |
| `guard-without-record` | I installed this guard myself: is it working, and why is nothing tracked? |

## The comprehension checklist

Read the reports side by side with somebody who has never used Steer. Without a
glossary, and reading only the report, they should be able to say:

- [ ] the overall recommendation and the decisions needed, without reading an inventory;
- [ ] which details can wait for a requested explanation without changing the conclusion;
- [ ] whether agent feedback is unchecked or confirmed, and whether an error is in tracking or enforcement;
- [ ] what problem Steer found, and where the evidence came from;
- [ ] what applying a suggested fix changes, and where it applies;
- [ ] whether a guard is installed, whether it ran, and whether its benefit is known;
- [ ] why a result is uncertain, and whether any action of theirs helps;
- [ ] whether an action removes a change or only stops measuring it;
- [ ] what an empty history means, and what collects it from now on.

Record every place they were misled, revise the copy, and regenerate. A report that
needs a spoken explanation has failed the pass, whatever it says.

The builders emit the status, score and queue contracts; selected scenarios also
provide saved audit output and inspected facts through `supporting_evidence`. The removal scenario includes
an explicit user decision to remove the guard; its field counts do not justify that
decision. A report must distinguish recurrence from causal benefit, a session's
classifier match from an adjudicated finding, and empty opportunity from a quiet
window with eligible work. Reviewer identities name provenance without grading drift.

For the busy report, quiet fixes should be grouped and the Windows proposal should
retain its cleanup cost and uncertain relevance. Feedback scenarios should use
plain language and preserve the distinction between a complaint and a confirmed
wrong block. Audit totals and byte budgets need not appear in the executive view.
