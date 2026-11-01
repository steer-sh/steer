{round_note}{count} independent reviewers ({reviewer}) red-teamed this commit before it landed. Every reviewer's raw output is below, unfiltered. They ran under identical instructions, so the same defect often appears more than once — adjudicate each distinct concern exactly once, and adjudicate every distinct concern.

They have LESS context than you: they saw the diff, the commit message, and whatever they chose to read — not this conversation, and not the constraints you already resolved while writing the change.

Independent reviewers miss different things, which is why every run is shown rather than voted on. A concern raised by one reviewer is not weaker than one raised by every reviewer, and one raised by every reviewer is not stronger — adjudicate each on its merits.

If two findings cannot both be true, at least one of them is false. Say so and rule on the pair explicitly rather than accepting both.

--- REVIEWS ---
{review}
--- END REVIEWS ---

For each finding: state its load-bearing premise in one sentence, then test THAT premise against the code — not against whether the finding sounds plausible. Open the file where the answer is in the file.

{rerun_note}Before editing, record:

- **Needed by:** the user's requirement or a requirement at the base commit, with its source, affected input class and caller path. This change's own claims, comments, notes or tests cannot establish necessity, including notes written while making it. A general correctness statement is insufficient.
- **Failure if unchanged:** the concrete consequence, including harmful side effects and recoverability.
- **Response:** repair | safely refuse or narrow | remove optional machinery | leave unfixed.

Repair failures of required use, including false refusals. Include crafted inputs at relevant untrusted boundaries without requiring an observed attack; respect explicit threat-model exclusions.

A constructed input with no demonstrated support obligation may remain ACCEPTED, not fixed when it safely stops before harmful effects and permits recovery. A failure that completes a harmful effect, such as committing wrong or incomplete data, is not a safe stop, even when a later run could repair it. Check the relevant caller and contract first; missing examples establish neither unreachability nor a support obligation.

Prefer the smallest response that addresses the failure class while preserving required behavior. Refusal, narrowing or removal of optional machinery can qualify; rejecting required inputs or merely documenting harmful behavior cannot. When the defect sits in a mechanism this change introduced by choice, and the requirement does not demand that mechanism, removing or narrowing it beats repairing it.

Investigate uncertainty during adjudication by reading the relevant code and requirements. Remaining uncertainty alone does not justify new machinery: ask the user or record the concern and uncertainty. Where that investigation establishes a credible path to irreversible harm within scope, withhold the affected operation or use the smallest safe containment, preferably an explicit refusal.

Record deferred true concerns as ACCEPTED, not fixed. REJECTED means a false premise. Then make only the decided changes. If no repairs are necessary, retry the reviewed diff unchanged. Acceptance and the reviewer's severity label do not themselves require a repair.

Reporting — this is not optional
--------------------------------
THE USER CANNOT SEE ANY OF THIS. The reviews above went to you alone; the user gets no review text and has no way to read it. To the user, a commit simply took longer. So this chain MUST end with the ledger below, written to be understood by someone who has read NONE of the review text.

ONE LEDGER PER CHAIN, and it is the last thing the chain produces. Do not write it now. Write it as soon as the commit SUCCEEDS, as text in that reply and before your next tool call, with one section per round the chain fired, in order. The gate's note on that commit repeats the header line. Round {firing} is a section of that ledger, never a ledger of its own.

Keep your round-{firing} verdicts where you can find them again. A round reconstructed from memory two rounds later is how a section ends up under the wrong number.

Each line is SELF-CONTAINED. Never refer to a finding by a bare label — "E2", "the third finding", "R1.2" alone — because those identifiers exist only in text the user does not have. Say what the finding CLAIMED, in your own words, before ruling on it.

Emit exactly this shape, ahead of your other commentary. Copy the header line as it stands, sequence ID and subject included — it is what files this ledger against THIS commit, and one turn often commits more than once to the same repo. Every round that fired then gets a section, and its round number is not optional either — that is what files the section against the round it answers:

**Commit gate fired — review sequence{chain}, {subject}**

**Round 1, blocking** — <N> reviewers, <M> raw findings, <K> distinct concerns.

- **ACCEPTED** — <the concern in one clause> → needed by <the requirement>; <the change you made, file:line>
- **ACCEPTED, not fixed** — <the concern in one clause> → <why no supported use needs the repair, or the user's instruction>
- **REJECTED** — <the concern in one clause> → <the premise, and why it is false>
- Weakest surviving: <the concern> — <why it still stands, or that none survived>

A finding whose premise holds is ACCEPTED even when you leave it unfixed. REJECTED means its premise is false, never that fixing it is out of scope.

**Round 2, blocking** — <N> reviewers, <M> raw findings, <K> distinct concerns.

- one section in this shape for every round that fired, up to and including the last.

One line per DISTINCT concern, not per reviewer. If every finding was rejected, say so plainly and give the reasons — "the gate fired and I rejected all four, here is why" is a complete and expected outcome, not a failure to report.

A round with no section reads as a round you never adjudicated, so the number of sections must equal the number of rounds that fired.

Do NOT put this ledger in the commit message.

This gate re-fires on your next `git commit` if the diff CHANGED, because the reviewers then read code none of them has seen — your fixes. It stops the moment a retry's diff is identical to the one already reviewed, and after {cap} firings in any case. So an unchanged retry ends the chain immediately, whether you rejected every finding or accepted some and left them unfixed: it costs you nothing and buys you nothing, and the only reader of that decision is the user.

Ending the chain at the {cap}-firing cap does not skip the review of your last fixes. That round gets one final review which cannot block the commit, and which you must adjudicate and report in the same way.
