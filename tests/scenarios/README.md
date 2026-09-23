# Report scenarios

Seven saved snapshots covering the seven decisions a reader arrives at `/steer-report`
with. `scenarios.py` builds each one over `base_status.json`; `../report_scenarios.py`
feeds them to the skill headlessly and writes the reports into `out/`.

There is no mechanical oracle for the prose. The runner regenerates; a person judges.
Each report's first line names the skill files the run opened beside `SKILL.md`. A
scenario whose snapshot holds an `interventions[]` row or a nonzero
`telemetry.audit_firings` must name `audit.md`; `first-run` must name none.

| Scenario | The reader's question |
|---|---|
| `new-suggestion` | Should I apply this fix? |
| `awaiting-evidence` | What has been observed since this guard was installed? |
| `changed-comparison` | Who counted the two windows, and what can these counts establish? |
| `missing-decisions` | What is missing, and can I do anything about it? |
| `declined-suggestion` | We said no to this — has that aged badly? |
| `remove-a-guard` | How do I get rid of this, and what does that remove? |
| `first-run` | Nothing is installed and no commit was reviewed yet: what now? |

## The comprehension checklist

Read the seven reports side by side with somebody who has never used Steer. Without a
glossary, and reading only the report, they should be able to say:

- [ ] what problem Steer found, and where the evidence came from;
- [ ] what applying a suggested fix changes, and where it applies;
- [ ] whether a guard is installed, whether it ran, and whether its benefit is known;
- [ ] why a result is uncertain, and whether any action of theirs helps;
- [ ] whether an action removes a change or only stops measuring it;
- [ ] what an empty history means, and what collects it from now on.

Record every place they were misled, revise the copy, and regenerate. A report that
needs a spoken explanation has failed the pass, whatever it says.

The fixtures use Status v11 and Queue v5 observations. The removal scenario includes
an explicit user decision to remove the guard; its field counts do not justify that
decision. A report must distinguish recurrence from causal benefit, a session's
classifier match from an adjudicated finding, and empty opportunity from a quiet
window with eligible work. Reviewer identities name provenance without grading drift.
