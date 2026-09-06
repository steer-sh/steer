# Report scenarios

Six saved snapshots covering the six decisions a reader arrives at `/steer-report`
with. `scenarios.py` builds each one over `base_status.json`; `../report_scenarios.py`
feeds them to the skill headlessly and writes the reports into `out/`.

There is no mechanical oracle here. The runner regenerates; a person judges.

| Scenario | The reader's question |
|---|---|
| `new-suggestion` | Should I apply this fix? |
| `awaiting-evidence` | Is this guard working yet? |
| `changed-comparison` | Why can't Steer tell me whether it helped? |
| `missing-decisions` | What is missing, and can I do anything about it? |
| `declined-suggestion` | We said no to this — has that aged badly? |
| `remove-a-guard` | How do I get rid of this, and what does that remove? |

## The comprehension checklist

Read the six reports side by side with somebody who has never used Steer. Without a
glossary, and reading only the report, they should be able to say:

- [ ] what problem Steer found, and where the evidence came from;
- [ ] what applying a suggested fix changes, and where it applies;
- [ ] whether a guard is installed, whether it ran, and whether its benefit is known;
- [ ] why a result is uncertain, and whether any action of theirs helps;
- [ ] whether an action removes a change or only stops measuring it.

Record every place they were misled, revise the copy, and regenerate. A report that
needs a spoken explanation has failed the pass, whatever it says.
